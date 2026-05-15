use anyhow::{Context, Result};
use futures_util::io::AsyncReadExt;
use futures_util::{SinkExt, StreamExt};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::time::{sleep, timeout};
use tokio_tungstenite::connect_async_tls_with_config;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::http::header;
use tokio_tungstenite::tungstenite::Message;
use tracing::{info, warn};
use yamux::{Config as YamuxConfig, Connection, Mode};

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

        let mut registry = TunnelRegistry::new();
        for t in tunnels {
            registry.add(t);
        }

        self.yamux_loop(yamux_conn, registry).await
    }

    async fn yamux_loop(
        &self,
        mut conn: Connection<crate::wsconn::WsConn>,
        mut registry: TunnelRegistry,
    ) -> Result<()> {
        loop {
            match std::future::poll_fn(|cx| conn.poll_next_inbound(cx)).await {
                Some(Ok(mut stream)) => {
                    let mut hdr = [0u8; 4];
                    if stream.read_exact(&mut hdr).await.is_err() {
                        continue;
                    }
                    let tunnel_id = u32::from_be_bytes(hdr) as u64;
                    if let Some(t) = registry.get(tunnel_id) {
                        let target = t.target.clone();
                        let proto = t.proto.clone();
                        tokio::spawn(async move {
                            if proto == "tcp" {
                                handle_tcp_stream(stream, target).await;
                            } else {
                                handle_udp_stream(stream, target).await;
                            }
                        });
                    } else {
                        warn!("unknown tunnel id {tunnel_id}");
                    }
                }
                Some(Err(e)) => return Err(e.into()),
                None => return Ok(()),
            }
        }
    }
}
