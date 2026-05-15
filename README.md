# StealthPTE

抗审查的 WebSocket 内网穿透工具，支持 TCP/UDP 端口映射，可套 Cloudflare CDN。

## 架构

```
[内网客户端 (Windows 服务)]  ──── WSS 443 ────  [CDN]  ────  [公网服务器 (Go)]
                                yamux 多路复用                   Caddy TLS 终止
                                单条出站连接                      Web UI /admin/
```

## 快速部署（服务端）

```bash
cd deploy
# 修改 docker-compose.yml 中的 ADMIN_PASS
# 修改 Caddyfile 中的域名
docker compose up -d
```

## 客户端安装（Windows）

1. 从 [Releases](../../releases) 下载 `stealthclient.exe`
2. 在管理后台创建客户端，获取 `client_id` 和 `token`
3. 在 exe 同目录创建 `config.toml`：
   ```toml
   server_url = "wss://your-domain.com/api/v1/stream"
   client_id  = "your-client-id"
   token      = "your-token"
   ```
4. 以管理员身份运行：
   ```
   stealthclient.exe --install
   ```

## 目录结构

```
server/     Go 服务端
client/     Rust Windows 客户端
deploy/     Docker Compose + Caddyfile
.github/    CI/CD workflows
```
