use bytes::{BufMut, BytesMut};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpStream, UdpSocket};
use tokio_util::compat::FuturesAsyncReadCompatExt;
use tracing::{debug, warn};
use yamux::Stream;

use crate::protocol::TunnelInfo;

pub async fn handle_tcp_stream(stream: Stream, target: String) {
    let stream = stream.compat();
    let mut tcp = match TcpStream::connect(&target).await {
        Ok(c) => c,
        Err(e) => {
            warn!("tcp connect {target}: {e}");
            return;
        }
    };
    if let Err(e) = tcp.set_nodelay(true) {
        debug!("set_nodelay: {e}");
    }

    let (mut sr, mut sw) = tokio::io::split(stream);
    let (mut tr, mut tw) = tcp.split();

    tokio::select! {
        _ = tokio::io::copy(&mut sr, &mut tw) => {}
        _ = tokio::io::copy(&mut tr, &mut sw) => {}
    }
}

pub async fn handle_udp_stream(stream: Stream, target: String) {
    let sock = match UdpSocket::bind("0.0.0.0:0").await {
        Ok(s) => s,
        Err(e) => {
            warn!("udp bind: {e}");
            return;
        }
    };
    if let Err(e) = sock.connect(&target).await {
        warn!("udp connect {target}: {e}");
        return;
    }

    let sock = Arc::new(sock);
    let sock2 = sock.clone();
    let stream = stream.compat();
    let (mut read_half, mut write_half) = tokio::io::split(stream);

    let mut fwd = tokio::spawn(async move {
        let mut len_buf = [0u8; 2];
        loop {
            if read_half.read_exact(&mut len_buf).await.is_err() {
                break;
            }
            let size = u16::from_be_bytes(len_buf) as usize;
            let mut buf = vec![0u8; size];
            if read_half.read_exact(&mut buf).await.is_err() {
                break;
            }
            if sock2.send(&buf).await.is_err() {
                break;
            }
        }
    });

    let mut recv_buf = vec![0u8; 65535];
    loop {
        tokio::select! {
            n = sock.recv(&mut recv_buf) => {
                match n {
                    Ok(n) => {
                        let len = n as u16;
                        let mut pkt = BytesMut::with_capacity(2 + n);
                        pkt.put_u16(len);
                        pkt.extend_from_slice(&recv_buf[..n]);
                        if write_half.write_all(&pkt).await.is_err() { break; }
                    }
                    Err(_) => break,
                }
            }
            _ = &mut fwd => break,
        }
    }
}

pub struct TunnelRegistry {
    tunnels: HashMap<u64, TunnelInfo>,
}

impl TunnelRegistry {
    pub fn new() -> Self {
        Self { tunnels: HashMap::new() }
    }

    pub fn add(&mut self, t: TunnelInfo) {
        self.tunnels.insert(t.id, t);
    }

    pub fn remove(&mut self, id: u64) -> Option<TunnelInfo> {
        self.tunnels.remove(&id)
    }

    pub fn get(&self, id: u64) -> Option<&TunnelInfo> {
        self.tunnels.get(&id)
    }
}
