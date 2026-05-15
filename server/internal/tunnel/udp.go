package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/stealthpte/server/internal/db"
)

const udpSessionTimeout = 30 * time.Second

type udpSession struct {
	stream    *yamux.Stream
	lastSeen  time.Time
}

type udpListener struct {
	tunnel   *db.Tunnel
	conn     *net.UDPConn
	getYamux func(clientID string) *yamux.Session

	mu       sync.Mutex
	sessions map[string]*udpSession // "ip:port" → session
	done     chan struct{}
}

func newUDPListener(t *db.Tunnel, getYamux func(string) *yamux.Session) (*udpListener, error) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", t.ServerPort))
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	return &udpListener{
		tunnel:   t,
		conn:     conn,
		getYamux: getYamux,
		sessions: make(map[string]*udpSession),
		done:     make(chan struct{}),
	}, nil
}

func (l *udpListener) serve() {
	defer l.conn.Close()
	go l.reaper()

	buf := make([]byte, 65535)
	for {
		n, srcAddr, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])
		go l.handlePacket(srcAddr, payload)
	}
}

func (l *udpListener) handlePacket(src *net.UDPAddr, payload []byte) {
	key := src.String()

	l.mu.Lock()
	sess, ok := l.sessions[key]
	if ok {
		sess.lastSeen = time.Now()
	}
	l.mu.Unlock()

	if !ok {
		yamuxSess := l.getYamux(l.tunnel.ClientID)
		if yamuxSess == nil {
			return
		}
		stream, err := yamuxSess.OpenStream()
		if err != nil {
			log.Printf("udp tunnel %d open stream: %v", l.tunnel.ID, err)
			return
		}

		// Write tunnel header: [4 bytes tunnel ID]
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(l.tunnel.ID))
		stream.Write(hdr[:])

		sess = &udpSession{stream: stream, lastSeen: time.Now()}
		l.mu.Lock()
		l.sessions[key] = sess
		l.mu.Unlock()

		// Read responses from stream and forward to src
		go l.readResponses(stream, src)
	}

	// Encode: [2-byte length BE][payload]
	pkt := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(pkt[:2], uint16(len(payload)))
	copy(pkt[2:], payload)
	sess.stream.Write(pkt)
}

func (l *udpListener) readResponses(stream *yamux.Stream, src *net.UDPAddr) {
	defer stream.Close()
	for {
		var lenBuf [2]byte
		if _, err := io.ReadFull(stream, lenBuf[:]); err != nil {
			return
		}
		size := binary.BigEndian.Uint16(lenBuf[:])
		buf := make([]byte, size)
		if _, err := io.ReadFull(stream, buf); err != nil {
			return
		}
		l.conn.WriteToUDP(buf, src)
	}
}

func (l *udpListener) reaper() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			for k, s := range l.sessions {
				if now.Sub(s.lastSeen) > udpSessionTimeout {
					s.stream.Close()
					delete(l.sessions, k)
				}
			}
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}

func (l *udpListener) close() {
	close(l.done)
	l.conn.Close()
}
