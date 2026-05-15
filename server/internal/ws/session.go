package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

type Session struct {
	clientID string
	conn     *websocket.Conn
	yamux    *yamux.Session
	ctrl     *yamux.Stream
	hub      *Hub

	mu     sync.Mutex
	closed bool
}

func (s *Session) run() {
	defer s.close()

	// Start WS-level ping ticker (keeps CDN alive)
	go s.wsPinger()

	// Start control stream ping/stats reader
	go s.ctrlReader()

	// Block until yamux session is closed
	<-s.yamux.CloseChan()
}

func (s *Session) wsPinger() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			s.conn.WriteMessage(websocket.PingMessage, nil)
			s.mu.Unlock()
		case <-s.yamux.CloseChan():
			return
		}
	}
}

func (s *Session) ctrlReader() {
	// Send periodic application-level pings via control stream
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				msg, _ := json.Marshal(PingMsg{Type: MsgPing})
				s.sendCtrl(msg)
			case <-s.yamux.CloseChan():
				return
			}
		}
	}()

	dec := json.NewDecoder(s.ctrl)
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return
		}
		var base struct {
			Type MsgType `json:"type"`
		}
		if err := json.Unmarshal(raw, &base); err != nil {
			continue
		}
		switch base.Type {
		case MsgPong:
			// alive
		case MsgStats:
			var stats StatsMsg
			if err := json.Unmarshal(raw, &stats); err == nil {
				for _, e := range stats.Tunnels {
					if err := s.hub.db.AddTraffic(e.ID, e.RxBytes, e.TxBytes); err != nil {
						log.Printf("stats update tunnel %d: %v", e.ID, err)
					}
				}
			}
		}
	}
}

func (s *Session) sendCtrl(msg []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.ctrl.SetWriteDeadline(time.Now().Add(5 * time.Second))
	s.ctrl.Write(msg)
	s.ctrl.SetWriteDeadline(time.Time{})
}

func (s *Session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.ctrl.Close()
	s.yamux.Close()
	s.conn.Close()
}
