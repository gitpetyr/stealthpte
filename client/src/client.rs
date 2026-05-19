use anyhow::{Context, Result};
use futures_util::io::AsyncReadExt;
use futures_util::{SinkExt, StreamExt};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::sync::RwLock;
use tokio::time::{sleep, timeout};
use tokio_tungstenite::connect_async_tls_with_config;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::http::header;
use tokio_tungstenite::tungstenite::Message;
use tokio_util::compat::FuturesAsyncReadCompatExt;
use tracing::{info, warn};
use yamux::{Config as YamuxConfig, Connection, Mode, Stream};

use crate::config::Config;
use crate::protocol::{ClientMsg, ServerMsg, TunnelInfo};
use crate::tunnel::{handle_tcp_stream, handle_udp_stream, TunnelRegistry};

pub struct Client {
    cfg: Config,
    stop: Arc<AtomicBool>,
}

impl Client {
    pub fn new(cfg: Config, stop: Arc<AtomicBool>) -> Self {
        Self { cfg, stop }
    }

    pub async fn run(&self) {
        let mut backoff = Duration::from_secs(1);
        let max_backoff = Duration::from_secs(60);

        loop {
            if self.stop.load(Ordering::Relaxed) {
                return;
            }
            match self.connect_once().await {
                Ok(()) => {
                    info!("connection closed, reconnecting…");
                    backoff = Duration::from_secs(1);
                }
                Err(e) => {
                    warn!("connection error: {e:#}, retry in {:.0?}", backoff);
                }
            }
            if self.stop.load(Ordering::Relaxed) {
                return;
            }
            sleep(backoff).await;
            backoff = (backoff * 2).min(max_backoff);
        }
    }

    async fn connect_once(&self) -> Result<()> {
        let mut req = self.cfg.server_url.as_str().into_client_request()?;
        let headers = req.headers_mut();
        headers.insert(
            header::USER_AGENT,
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
                .parse()?,
        );
        let origin = self.cfg.server_url
            .replace("wss://", "https://")
            .replace("ws://", "http://");
        let origin_host = origin.split('/').take(3).collect::<Vec<_>>().join("/");
        headers.insert(header::ORIGIN, origin_host.parse()?);

        info!("connecting to {}", self.cfg.server_url);
        let (ws_stream, _) = connect_async_tls_with_config(req, None, false, None)
            .await
            .context("websocket connect")?;

        let (mut ws_tx, mut ws_rx) = ws_stream.split();

        let hello = serde_json::to_string(&ClientMsg::Hello {
            client_id: self.cfg.client_id.clone(),
            token: self.cfg.token.clone(),
            version: "1.0".into(),
        })?;
        ws_tx.send(Message::Text(hello.into())).await?;

        let ack_msg = timeout(Duration::from_secs(15), ws_rx.next())
            .await
            .context("hello_ack timeout")?
            .context("no hello_ack")?
            .context("ws recv")?;

        let tunnels: Vec<TunnelInfo> = match ack_msg {
            Message::Text(txt) => {
                let msg: ServerMsg = serde_json::from_str(&txt)?;
                match msg {
                    ServerMsg::HelloAck { tunnels } => tunnels,
                    _ => anyhow::bail!("expected hello_ack"),
                }
            }
            _ => anyhow::bail!("unexpected message type"),
        };

        info!("authenticated, {} tunnel(s) active", tunnels.len());

        let ws_conn = crate::wsconn::WsConn::new(ws_tx, ws_rx);
        let yamux_conn = Connection::new(ws_conn, YamuxConfig::default(), Mode::Client);

        let registry = Arc::new(RwLock::new(TunnelRegistry::new()));
        {
            let mut reg = registry.write().await;
            for t in tunnels {
                reg.add(t);
            }
        }

        self.yamux_loop(yamux_conn, registry).await
    }

    async fn yamux_loop(
        &self,
        mut conn: Connection<crate::wsconn::WsConn>,
        registry: Arc<RwLock<TunnelRegistry>>,
    ) -> Result<()> {
        // First inbound stream is the server's control stream
        let ctrl_stream = match std::future::poll_fn(|cx| conn.poll_next_inbound(cx)).await {
            Some(Ok(s)) => s,
            Some(Err(e)) => return Err(e.into()),
            None => return Ok(()),
        };

        let reg_ctrl = registry.clone();
        tokio::spawn(async move {
            run_ctrl(ctrl_stream, reg_ctrl).await;
        });

        loop {
            match std::future::poll_fn(|cx| conn.poll_next_inbound(cx)).await {
                Some(Ok(stream)) => {
                    let registry = registry.clone();
                    tokio::spawn(async move {
                        let mut stream = stream;
                        let mut hdr = [0u8; 4];
                        if stream.read_exact(&mut hdr).await.is_err() {
                            return;
                        }
                        let tunnel_id = u32::from_be_bytes(hdr) as u64;
                        let info = registry.read().await.get(tunnel_id).cloned();
                        if let Some(t) = info {
                            if t.proto == "tcp" {
                                handle_tcp_stream(stream, t.target).await;
                            } else {
                                handle_udp_stream(stream, t.target).await;
                            }
                        } else {
                            warn!("unknown tunnel id {tunnel_id}");
                        }
                    });
                }
                Some(Err(e)) => return Err(e.into()),
                None => return Ok(()),
            }
        }
    }
}

async fn run_ctrl(stream: Stream, registry: Arc<RwLock<TunnelRegistry>>) {
    let stream = stream.compat();
    let (read_half, mut write_half) = tokio::io::split(stream);
    let mut reader = BufReader::new(read_half);
    let mut line = String::new();

    loop {
        line.clear();
        match reader.read_line(&mut line).await {
            Ok(0) | Err(_) => break,
            Ok(_) => {}
        }
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }
        let msg: ServerMsg = match serde_json::from_str(trimmed) {
            Ok(m) => m,
            Err(e) => {
                warn!("ctrl parse: {e}");
                continue;
            }
        };
        match msg {
            ServerMsg::Ping => {
                let mut pong = serde_json::to_string(&ClientMsg::Pong).unwrap_or_default();
                pong.push('\n');
                if write_half.write_all(pong.as_bytes()).await.is_err() {
                    break;
                }
                let _ = write_half.flush().await;
            }
            ServerMsg::TunnelAdd(t) => {
                info!("tunnel_add id={} {} :{}", t.id, t.proto, t.server_port);
                registry.write().await.add(TunnelInfo {
                    id: t.id,
                    proto: t.proto,
                    server_port: t.server_port,
                    target: t.target,
                });
            }
            ServerMsg::TunnelDel { id } => {
                info!("tunnel_del id={}", id);
                registry.write().await.remove(id);
            }
            ServerMsg::HelloAck { .. } => {}
        }
    }
}
