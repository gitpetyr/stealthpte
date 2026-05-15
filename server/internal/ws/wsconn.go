package ws

import (
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn wraps a *websocket.Conn as a net.Conn so yamux can use it.
type wsConn struct {
	conn *websocket.Conn
	r    io.Reader
}

func newWSConn(c *websocket.Conn) net.Conn {
	return &wsConn{conn: c}
}

func (w *wsConn) Read(b []byte) (int, error) {
	for {
		if w.r != nil {
			n, err := w.r.Read(b)
			if err == io.EOF {
				w.r = nil
				continue
			}
			return n, err
		}
		_, r, err := w.conn.NextReader()
		if err != nil {
			return 0, err
		}
		w.r = r
	}
}

func (w *wsConn) Write(b []byte) (int, error) {
	err := w.conn.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

func (w *wsConn) Close() error                       { return w.conn.Close() }
func (w *wsConn) LocalAddr() net.Addr                { return w.conn.LocalAddr() }
func (w *wsConn) RemoteAddr() net.Addr               { return w.conn.RemoteAddr() }
func (w *wsConn) SetDeadline(t time.Time) error      { return w.conn.SetReadDeadline(t) }
func (w *wsConn) SetReadDeadline(t time.Time) error  { return w.conn.SetReadDeadline(t) }
func (w *wsConn) SetWriteDeadline(t time.Time) error { return w.conn.SetWriteDeadline(t) }
