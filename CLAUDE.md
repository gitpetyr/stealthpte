# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Layout

Two independent components sharing one repo:

- `server/` — Go 服务端（`github.com/stealthpte/server`）
- `client/` — Rust Windows 客户端（`stealthclient`）
- `deploy/` — Dockerfile、docker-compose.yml、Caddyfile
- `.github/workflows/` — CI：`server.yml`（Go + Docker）、`client.yml`（Rust 交叉编译）

## Commands

Go is at `/usr/local/go/bin/go` (not in `$PATH` by default on this machine).

```bash
# Server — run from server/
export PATH=/usr/local/go/bin:$PATH
cd server

go build ./...                          # compile check
go test ./...                           # run tests
go run ./cmd/server                     # run locally (needs DATA_DIR to exist)
DATA_DIR=/tmp/pte go run ./cmd/server   # quick local run

# Rust client — cross-compile for Windows (requires CI or local MinGW)
cd client
cargo build --target x86_64-pc-windows-gnu --release
# output: client/target/x86_64-pc-windows-gnu/release/stealthclient.exe

# Deploy
cd deploy
docker compose up -d
```

## Architecture

### Data Flow (single connection model)

```
External User → server:PORT (TCP/UDP)
  → tunnel/Manager accepts connection
  → opens new yamux stream on the client's session
  → sends 4-byte tunnel ID header on the stream
  → client reads ID, connects to target, bidirectional copy
```

The client makes **one outbound WSS 443 connection**; all tunnel traffic multiplexes over it via yamux.

### Server Component Wiring (`cmd/server/main.go`)

`Hub` ← owns yamux sessions → `tunnel.Manager` uses `hub.GetYamux(clientID)` to open streams.

`Hub.OnConnect` / `Hub.OnDisconnect` callbacks trigger `Manager.ClientConnected` / `ClientDisconnected`, which start/stop all TCP and UDP listeners for that client.

`api.Handler` calls `hub.NotifyTunnelAdd/Del` to push live rule changes to connected clients over the control stream. The `api` package references hub via `HubInterface` (not the concrete `*ws.Hub`) to avoid an import cycle; `IsOnline` is reached via a type assertion inside `hubIsOnline()`.

### Control Stream Protocol

The server opens yamux stream #1 as the **control stream** immediately after auth. Messages are newline-delimited JSON. Server→Client: `tunnel_add`, `tunnel_del`, `ping`. Client→Server: `pong`, `stats` (incremental traffic counters every 30s).

Two heartbeat mechanisms run in parallel:
1. Application-level `ping`/`pong` over the yamux control stream (60s)
2. WebSocket-level `Ping` frame (60s) — keeps Cloudflare CDN alive (100s timeout)

### Tunnel Stream Header

Every data stream (TCP or UDP) carries a **4-byte big-endian tunnel ID** as its first bytes, written by the server before any payload. The client reads this header to look up the target address in its local registry.

UDP additionally wraps each datagram as `[2-byte length BE][payload]` in both directions.

### WebSocket→yamux Adapter

`ws/wsconn.go` (server) and `client/src/wsconn.rs` (client) each wrap a WebSocket connection as a `net.Conn` / `AsyncRead+AsyncWrite` so yamux can treat it as a byte stream. The server sends yamux binary frames as `BinaryMessage`; the client ignores all non-binary WS messages.

### Auth

JWT secret is **ephemeral** — regenerated on every server start. All admin sessions are invalidated on restart. The JWT is stored as an `HttpOnly` SameSite=Strict cookie named `session`. The `/admin/login` route is excluded from the JWT middleware check.

### Database

Pure-Go SQLite (`modernc.org/sqlite`, no CGO). WAL mode, `max_open_conns=1`. Schema is auto-migrated on open. `rx_bytes`/`tx_bytes` on the `tunnels` table are cumulative server-side totals, updated by `db.AddTraffic` when the client reports incremental stats.

### Client Windows Service

Service name: `wnsvc`, display name: `Windows 网络服务`, binary copied to `C:\Windows\System32\wnsvc.exe`. Config at `C:\ProgramData\wnsvc\config.toml`. The `--install` flow handles missing/incomplete config gracefully (writes template and exits early). Reconnection uses exponential backoff: 1s → 2s → … → 60s cap.

## Configuration

Server config (env vars take precedence over `config.yaml`):

| Env var | Default | Description |
|---------|---------|-------------|
| `WS_PATH` | `/api/v1/stream` | WebSocket endpoint path |
| `PORT_RANGE` | `10000-20000` | Allowed tunnel port range |
| `ADMIN_PASS` | `changeme` | Web UI password |
| `DATA_DIR` | `/data` | SQLite + data directory |
| `LISTEN` | `:8080` | HTTP listen address |
| `CONFIG_PATH` | `config.yaml` | Path to YAML config file |

## Key Design Constraints

- **No local tunnel config on client** — all rules come from the server via `hello_ack` and live `tunnel_add`/`tunnel_del` messages.
- **Port uniqueness enforced at DB level** (`UNIQUE` on `tunnels.server_port`) and validated in API handlers; Web UI also does a debounced pre-check via `GET /admin/api/v1/ports/check?port=X`.
- **Tunnel listeners stop when the client disconnects** and restart on reconnect — no queuing of external connections while the client is offline.
- **No `permessage-deflate`** negotiated on the WebSocket (CDN compatibility).
