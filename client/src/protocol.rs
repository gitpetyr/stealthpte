use serde::{Deserialize, Serialize};

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum ServerMsg {
    HelloAck { tunnels: Vec<TunnelInfo> },
    TunnelAdd(TunnelAdd),
    TunnelDel { id: u64 },
    Ping,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum ClientMsg {
    Hello {
        client_id: String,
        token: String,
        version: String,
    },
    Pong,
    Stats {
        tunnels: Vec<StatEntry>,
    },
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct TunnelInfo {
    pub id: u64,
    pub proto: String,
    pub server_port: u16,
    pub target: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct TunnelAdd {
    pub id: u64,
    pub proto: String,
    pub server_port: u16,
    pub target: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct StatEntry {
    pub id: u64,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub conns: u32,
}
