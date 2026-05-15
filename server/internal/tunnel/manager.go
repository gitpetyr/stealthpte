package tunnel

import (
	"log"
	"sync"

	"github.com/hashicorp/yamux"
	"github.com/stealthpte/server/internal/db"
)

// Manager owns all active tunnel listeners.
type Manager struct {
	mu       sync.Mutex
	listeners map[int64]*tcpListener  // tunnelID → tcp listener
	udpSess   map[int64]*udpListener  // tunnelID → udp listener

	getYamux func(clientID string) *yamux.Session
}

func NewManager(getYamux func(clientID string) *yamux.Session) *Manager {
	return &Manager{
		listeners: make(map[int64]*tcpListener),
		udpSess:   make(map[int64]*udpListener),
		getYamux:  getYamux,
	}
}

// StartAll launches listeners for all enabled tunnels on startup.
func (m *Manager) StartAll(tunnels []*db.Tunnel) {
	for _, t := range tunnels {
		if t.Enabled {
			m.start(t)
		}
	}
}

func (m *Manager) start(t *db.Tunnel) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t.Proto == "tcp" {
		if _, ok := m.listeners[t.ID]; ok {
			return
		}
		l, err := newTCPListener(t, m.getYamux)
		if err != nil {
			log.Printf("tunnel %d tcp listen :%d: %v", t.ID, t.ServerPort, err)
			return
		}
		m.listeners[t.ID] = l
		go l.serve()
	} else {
		if _, ok := m.udpSess[t.ID]; ok {
			return
		}
		l, err := newUDPListener(t, m.getYamux)
		if err != nil {
			log.Printf("tunnel %d udp listen :%d: %v", t.ID, t.ServerPort, err)
			return
		}
		m.udpSess[t.ID] = l
		go l.serve()
	}
	log.Printf("tunnel %d started (%s :%d → %s)", t.ID, t.Proto, t.ServerPort, t.TargetAddr)
}

func (m *Manager) stop(tunnelID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.listeners[tunnelID]; ok {
		l.close()
		delete(m.listeners, tunnelID)
	}
	if l, ok := m.udpSess[tunnelID]; ok {
		l.close()
		delete(m.udpSess, tunnelID)
	}
}

// TunnelAdd is called when the admin API adds/enables a tunnel.
func (m *Manager) TunnelAdd(t *db.Tunnel) {
	m.start(t)
}

// TunnelDel is called when the admin API removes/disables a tunnel.
func (m *Manager) TunnelDel(tunnelID int64) {
	m.stop(tunnelID)
}

// ClientDisconnected stops all listeners whose client went offline.
func (m *Manager) ClientDisconnected(clientID string, tunnels []*db.Tunnel) {
	for _, t := range tunnels {
		m.stop(t.ID)
	}
}

// ClientConnected restarts listeners for a reconnected client.
func (m *Manager) ClientConnected(clientID string, tunnels []*db.Tunnel) {
	for _, t := range tunnels {
		if t.Enabled {
			m.start(t)
		}
	}
}
