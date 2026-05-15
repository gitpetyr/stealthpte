package ws

import "github.com/stealthpte/server/internal/db"

type MsgType string

const (
	MsgHello    MsgType = "hello"
	MsgHelloAck MsgType = "hello_ack"
	MsgPing     MsgType = "ping"
	MsgPong     MsgType = "pong"
	MsgStats    MsgType = "stats"
	MsgTunnelAdd MsgType = "tunnel_add"
	MsgTunnelDel MsgType = "tunnel_del"
)

type HelloMsg struct {
	Type     MsgType `json:"type"`
	ClientID string  `json:"client_id"`
	Token    string  `json:"token"`
	Version  string  `json:"version"`
}

type HelloAckMsg struct {
	Type    MsgType      `json:"type"`
	Tunnels []TunnelInfo `json:"tunnels"`
}

type TunnelInfo struct {
	ID         int64  `json:"id"`
	Proto      string `json:"proto"`
	ServerPort int    `json:"server_port"`
	Target     string `json:"target"`
}

type TunnelAddMsg struct {
	Type       MsgType `json:"type"`
	ID         int64   `json:"id"`
	Proto      string  `json:"proto"`
	ServerPort int     `json:"server_port"`
	Target     string  `json:"target"`
}

type TunnelDelMsg struct {
	Type MsgType `json:"type"`
	ID   int64   `json:"id"`
}

type PingMsg struct {
	Type MsgType `json:"type"`
}

type PongMsg struct {
	Type MsgType `json:"type"`
}

type StatEntry struct {
	ID      int64 `json:"id"`
	RxBytes int64 `json:"rx_bytes"`
	TxBytes int64 `json:"tx_bytes"`
	Conns   int   `json:"conns"`
}

type StatsMsg struct {
	Type    MsgType     `json:"type"`
	Tunnels []StatEntry `json:"tunnels"`
}

func tunnelToInfo(t *db.Tunnel) TunnelInfo {
	return TunnelInfo{
		ID:         t.ID,
		Proto:      t.Proto,
		ServerPort: t.ServerPort,
		Target:     t.TargetAddr,
	}
}
