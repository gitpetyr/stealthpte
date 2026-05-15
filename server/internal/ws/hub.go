package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
	"github.com/stealthpte/server/internal/db"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:      func(r *http.Request) bool { return true },
	ReadBufferSize:   32 * 1024,
	WriteBufferSize:  32 * 1024,
}

type Hub struct {
	db      *db.DB
	mu      sync.RWMutex
	clients map[string]*Session // clientID → session

	onConnect    func(clientID string, session *yamux.Session)
	onDisconnect func(clientID string)
}

func NewHub(database *db.DB) *Hub {
	return &Hub{
		db:      database,
		clients: make(map[string]*Session),
	}
}

func (h *Hub) OnConnect(fn func(clientID string, session *yamux.Session)) {
	h.onConnect = fn
}

func (h *Hub) OnDisconnect(fn func(clientID string)) {
	h.onDisconnect = fn
}

// ServeHTTP handles incoming WebSocket connections.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	go h.handleConn(conn)
}

func (h *Hub) handleConn(conn *websocket.Conn) {
	conn.SetReadLimit(1 << 20)

	// Read hello
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	var hello HelloMsg
	if err := json.Unmarshal(data, &hello); err != nil || hello.Type != MsgHello {
		conn.Close()
		return
	}

	client, err := h.db.GetClientByToken(hello.Token)
	if err != nil || client == nil || client.ID != hello.ClientID {
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "auth failed"))
		conn.Close()
		return
	}

	tunnels, err := h.db.ListTunnels(client.ID)
	if err != nil {
		conn.Close()
		return
	}

	infos := make([]TunnelInfo, 0, len(tunnels))
	for _, t := range tunnels {
		if t.Enabled {
			infos = append(infos, tunnelToInfo(t))
		}
	}

	ack, _ := json.Marshal(HelloAckMsg{Type: MsgHelloAck, Tunnels: infos})
	if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
		conn.Close()
		return
	}

	// Wrap WS conn as net.Conn for yamux
	wsConn := newWSConn(conn)
	yamuxSrv, err := yamux.Server(wsConn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		return
	}

	// Open control stream (stream 1)
	ctrlStream, err := yamuxSrv.OpenStream()
	if err != nil {
		yamuxSrv.Close()
		return
	}

	sess := &Session{
		clientID: client.ID,
		conn:     conn,
		yamux:    yamuxSrv,
		ctrl:     ctrlStream,
		hub:      h,
	}

	h.mu.Lock()
	if old, ok := h.clients[client.ID]; ok {
		old.close()
	}
	h.clients[client.ID] = sess
	h.mu.Unlock()

	log.Printf("client connected: %s (%s)", client.Name, client.ID)

	if h.onConnect != nil {
		h.onConnect(client.ID, yamuxSrv)
	}

	sess.run()

	h.mu.Lock()
	if h.clients[client.ID] == sess {
		delete(h.clients, client.ID)
	}
	h.mu.Unlock()

	if h.onDisconnect != nil {
		h.onDisconnect(client.ID)
	}
	log.Printf("client disconnected: %s (%s)", client.Name, client.ID)
}

func (h *Hub) IsOnline(clientID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[clientID]
	return ok
}

func (h *Hub) NotifyTunnelAdd(clientID string, t *db.Tunnel) {
	h.mu.RLock()
	sess, ok := h.clients[clientID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	msg, _ := json.Marshal(TunnelAddMsg{
		Type:       MsgTunnelAdd,
		ID:         t.ID,
		Proto:      t.Proto,
		ServerPort: t.ServerPort,
		Target:     t.TargetAddr,
	})
	sess.sendCtrl(msg)
}

func (h *Hub) NotifyTunnelDel(clientID string, tunnelID int64) {
	h.mu.RLock()
	sess, ok := h.clients[clientID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	msg, _ := json.Marshal(TunnelDelMsg{Type: MsgTunnelDel, ID: tunnelID})
	sess.sendCtrl(msg)
}

// GetYamux returns the yamux session for a client (used by tunnel manager).
func (h *Hub) GetYamux(clientID string) *yamux.Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if s, ok := h.clients[clientID]; ok {
		return s.yamux
	}
	return nil
}
