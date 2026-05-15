# StealthPTE 设计文档

**日期**：2026-05-15  
**状态**：已确认

---

## 1. 项目概述

StealthPTE 是一个抗审查的基于 WebSocket 的内网穿透工具，支持将内网任意 TCP/UDP 端口映射到公网服务器端口。服务端支持套 CDN（Cloudflare 等），客户端以 Windows 服务形式运行，支持一键安装。

**威胁模型**：需同时对抗 GFW（国家级 DPI）和企业/校园网防火墙。

---

## 2. 整体架构

```
[内网机器]                      [CDN / Cloudflare]        [公网服务器]
  Rust Client (Windows服务)  ←── WSS 443 ──→  透传  ←── Go Server :8080
       │                                                      │
       │  yamux 多路复用（单条 WSS 连接）                       ├── Caddy :443 (TLS)
       ├── stream 1: 控制流（JSON 命令）                        ├── Web UI /admin/
       ├── stream 2: TCP 会话 A                               └── SQLite /data/db.sqlite
       └── stream N: UDP 会话 B

[外部用户] ──→ server:10080 (TCP/UDP, 直连，绕过 Caddy) ──→ yamux ──→ 内网目标
```

**核心设计原则**：
- 客户端到服务端仅需一条出站 WSS 443 连接
- 所有隧道数据通过 yamux 多路复用在同一 WS 连接上传输
- 服务端通过控制流主动下发隧道规则，客户端无需本地配置规则

---

## 3. 技术选型

| 组件 | 语言/技术 | 理由 |
|------|-----------|------|
| 服务端 | Go | 单二进制，goroutine 并发，go-yamux 成熟 |
| 客户端 | Rust | 单二进制 exe，tokio 异步，Windows 服务原生支持 |
| Web UI | HTML + Alpine.js | 无构建步骤，embed.FS 内嵌 Go 二进制 |
| 持久化 | SQLite | 单文件，无需外部数据库 |
| TLS/反代 | Caddy | 自动 Let's Encrypt，用户自行部署 |
| 容器化 | Docker + Compose | 服务端标准部署方式 |
| Windows 构建 | GitHub Actions | 交叉编译 x86_64-pc-windows-gnu |

---

## 4. 服务端设计（Go）

### 4.1 进程结构

```
Go Server (监听 :8080)
├── WebSocket 端点 GET /<ws-path>     → 接受客户端连接
├── Web UI        GET /admin/*        → 管理界面（需认证）
├── REST API      /admin/api/v1/      → Web UI 数据接口
├── 隧道端口管理器                      → 端口分配与冲突检测
└── SQLite 持久层
```

### 4.2 REST API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /admin/api/v1/clients | 客户端列表（含在线状态） |
| GET | /admin/api/v1/clients/:id/tunnels | 客户端隧道规则列表 |
| POST | /admin/api/v1/clients/:id/tunnels | 新增隧道规则 |
| PUT | /admin/api/v1/clients/:id/tunnels/:tid | 修改隧道规则 |
| DELETE | /admin/api/v1/clients/:id/tunnels/:tid | 删除隧道规则 |
| GET | /admin/api/v1/ports/check?port=10080 | 端口冲突检测 |

### 4.3 数据库 Schema

```sql
CREATE TABLE clients (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    token      TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

CREATE TABLE tunnels (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id   TEXT NOT NULL REFERENCES clients(id),
    proto       TEXT NOT NULL CHECK(proto IN ('tcp','udp')),
    server_port INTEGER NOT NULL UNIQUE,
    target_addr TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1
);
```

### 4.4 配置

通过环境变量或 `config.yaml` 提供：

```yaml
ws_path:    /api/v1/stream   # WebSocket 端点路径（可自定义）
port_range: 10000-20000      # 隧道端口可用范围
admin_pass: changeme         # Web UI 管理员密码
data_dir:   /data            # SQLite 和数据文件目录
```

### 4.5 Docker Compose

```yaml
services:
  caddy:
    image: caddy:latest
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
    networks:
      - internal

  server:
    image: ghcr.io/<repo>/stealthpte-server:latest
    ports:
      - "10000-20000:10000-20000/tcp"
      - "10000-20000:10000-20000/udp"
    volumes:
      - ./data:/data
    environment:
      - WS_PATH=/api/v1/stream
      - PORT_RANGE=10000-20000
      - ADMIN_PASS=changeme
      - DATA_DIR=/data
    networks:
      - internal

networks:
  internal:

volumes:
  caddy_data:
```

**Caddyfile 示例：**

```caddy
example.com {
    handle /api/v1/stream {
        reverse_proxy server:8080
    }
    handle /admin/* {
        reverse_proxy server:8080
    }
    respond 404
}
```

---

## 5. 客户端设计（Rust · Windows 服务）

### 5.1 命令行接口

```
stealthclient.exe --install    安装并启动服务
stealthclient.exe --uninstall  停止并卸载服务
stealthclient.exe --run        服务宿主模式（由 SCM 调用）
stealthclient.exe --status     查看当前服务状态
```

### 5.2 安装流程（--install）

```
1. 复制自身 → C:\Windows\System32\wnsvc.exe
2. 检查当前目录是否存在 config.toml
   ├── 存在 → 复制到 C:\ProgramData\wnsvc\config.toml
   └── 不存在 → 写入空模板，提示用户填写后重新执行 --install
3a. 检查配置完整性：server_url、client_id、token 三字段均非空则视为完整
3. 调用 Windows `CreateService` API（通过 windows-service crate，不 shell sc.exe）：
       ServiceName:  wnsvc
       DisplayName:  Windows 网络服务
       BinaryPath:   "C:\Windows\System32\wnsvc.exe" --run  （路径与参数分离传入）
       StartType:    SERVICE_AUTO_START
4. 配置完整则调用 `StartService` API 启动，否则跳过并提示
```

### 5.3 配置文件

路径：`C:\ProgramData\wnsvc\config.toml`

```toml
server_url = "wss://example.com/api/v1/stream"
client_id  = "client-abc123"
token      = "your-secret-token"
```

隧道规则不在本地持久化，由服务端在连接建立后通过控制流下发。

### 5.4 运行时结构

```
Windows SCM
  └── wnsvc.exe --run
        ├── windows-service crate → 响应 Start/Stop 信号
        ├── WSS 连接器（tokio-tungstenite + rustls）
        │   ├── 断线指数退避重连（1s → 2s → … → 60s 上限）
        │   └── 60s WebSocket ping frame 心跳
        ├── yamux 会话（yamux crate）
        │   ├── stream 1：控制流，收发 JSON
        │   └── stream N：数据流
        └── 隧道执行器
            ├── TCP：TcpListener + TCP_NODELAY
            └── UDP：UdpSocket，按 (src_ip:port) 追踪会话，30s 超时清理
```

### 5.5 GitHub Actions 交叉编译

```yaml
- name: Build Windows client
  run: |
    rustup target add x86_64-pc-windows-gnu
    cargo build --target x86_64-pc-windows-gnu --release
  # 产物：target/x86_64-pc-windows-gnu/release/stealthclient.exe
```

---

## 6. 通信协议

### 6.1 连接握手

```
Client                              Server
  │── WSS Upgrade (浏览器级 Headers) ──→ │
  │← 101 Switching Protocols ──────────  │
  │── yamux 会话 init ──────────────────→ │
  │                                      │
  │  控制流 (stream 1)                    │
  │── {"type":"hello",                   │
  │    "client_id":"abc",                │
  │    "token":"xxx",                    │
  │    "version":"1.0"} ───────────────→ │  验证 token
  │← {"type":"hello_ack",               │
  │    "tunnels":[...]} ───────────────  │  下发当前所有规则
```

### 6.2 控制流消息

```jsonc
// 服务端 → 客户端
{"type":"tunnel_add","id":1,"proto":"tcp","server_port":10080,"target":"192.168.1.10:80"}
{"type":"tunnel_add","id":2,"proto":"udp","server_port":10053,"target":"192.168.1.1:53"}
{"type":"tunnel_del","id":1}
{"type":"ping"}

// 客户端 → 服务端
{"type":"pong"}
{"type":"stats","tunnels":[{"id":1,"rx_bytes":102400,"tx_bytes":51200,"conns":3}]}
```

### 6.3 TCP 隧道数据流

```
外部用户 → 连接 server:10080
服务端：accept() → 打开新 yamux stream（携带 tunnel_id=1）
客户端：收到新 stream → 连接 192.168.1.10:80，设置 TCP_NODELAY
双向 copy：外部用户 ↔ yamux stream ↔ 内网服务
连接关闭 → 关闭对应 yamux stream
```

### 6.4 UDP 隧道数据流

```
外部用户 → 发 UDP 包到 server:10053
服务端：按 (src_ip:src_port) 查找/创建 yamux stream
        UDP 包格式：[2字节长度 big-endian][payload]
客户端：收包 → 发到 192.168.1.1:53 → 收响应 → 写回 stream
服务端：读响应 → sendto src_ip:src_port
UDP 会话 30s 无活动自动清理
```

---

## 7. 抗审查措施

### 7.1 传输层

- Caddy 处理 TLS 443，Let's Encrypt 自动证书
- 根路径返回 404（Caddy 直接响应）
- Go 服务端仅内网可达（:8080）

### 7.2 应用层伪装

WebSocket 握手 Headers 模拟浏览器：

```
User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36
Origin: https://example.com
Sec-WebSocket-Extensions: （留空，不协商压缩）
```

- WebSocket 路径可配置（默认 `/api/v1/stream`，外观像 API）
- 禁用 `permessage-deflate`（CDN 不透传压缩扩展）

### 7.3 心跳保活

- 控制流每 60s 发 `{"type":"ping"}` / 回 `{"type":"pong"}`
- 同时发 WebSocket Ping frame（保持 CDN 连接，Cloudflare 超时 100s）
- 双重心跳防止 CDN 和 GFW 空闲断连

### 7.4 CDN 注意事项

- Cloudflare Dashboard → Network → WebSockets 需手动开启
- 套 CDN 后服务端通过 `X-Forwarded-For` 获取真实客户端 IP
- 兼容 Cloudflare、AWS CloudFront 等支持 WebSocket 的 CDN

---

## 8. Web UI

**技术**：HTML + Alpine.js，embed.FS 内嵌 Go 二进制，无构建步骤。

**页面：**

- **登录页**：表单 + JWT Cookie，单管理员
- **总览页**：在线客户端列表（名称、连接时长、总流量）、全局统计
- **客户端详情页**：
  - 隧道规则表格（协议、服务端端口、内网目标、流量统计、启用/停用/删除）
  - 新增规则表单（协议选择、服务端端口实时冲突检测、目标地址）
  - 流量统计每 10s 轮询刷新

**端口冲突检测**：前端输入时调用 `/admin/api/v1/ports/check?port=X`，后端新增/修改时再次校验防并发冲突。

**流量统计**：客户端每 30s 上报增量 → 服务端累加入 SQLite → Web UI 轮询展示。

---

## 9. 构建与发布

| 产物 | 构建方式 | 触发条件 |
|------|----------|----------|
| Docker 镜像（服务端） | Go 交叉编译 linux/amd64 → Docker | push to main / tag |
| stealthclient.exe（客户端） | Rust cross to x86_64-pc-windows-gnu | push to main / tag |
| GitHub Release | 上传 .exe 产物 | tag v* |

---

## 10. 遗留决策

- Web UI 认证方式：表单 + JWT Cookie（单管理员，无需多用户）
- 服务端 WebSocket 路径默认值：`/api/v1/stream`（可通过环境变量覆盖）
- 客户端服务注册名：`wnsvc` / 显示名：`Windows 网络服务`
- 隧道端口范围默认值：`10000-20000`
