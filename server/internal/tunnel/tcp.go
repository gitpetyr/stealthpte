package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"

	"github.com/hashicorp/yamux"
	"github.com/stealthpte/server/internal/db"
)

type tcpListener struct {
	tunnel   *db.Tunnel
	ln       net.Listener
	getYamux func(clientID string) *yamux.Session
}

func newTCPListener(t *db.Tunnel, getYamux func(string) *yamux.Session) (*tcpListener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", t.ServerPort))
	if err != nil {
		return nil, err
	}
	return &tcpListener{tunnel: t, ln: ln, getYamux: getYamux}, nil
}

func (l *tcpListener) serve() {
	defer l.ln.Close()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return
		}
		go l.handleConn(conn)
	}
}

func (l *tcpListener) handleConn(conn net.Conn) {
	defer conn.Close()

	sess := l.getYamux(l.tunnel.ClientID)
	if sess == nil {
		return
	}

	stream, err := sess.OpenStream()
	if err != nil {
		log.Printf("tunnel %d open stream: %v", l.tunnel.ID, err)
		return
	}
	defer stream.Close()

	// Send tunnel header: [4 bytes tunnel ID big-endian]
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(l.tunnel.ID))
	if _, err := stream.Write(hdr[:]); err != nil {
		return
	}

	go io.Copy(stream, conn)
	io.Copy(conn, stream)
}

func (l *tcpListener) close() {
	l.ln.Close()
}
