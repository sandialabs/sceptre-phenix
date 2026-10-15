package broker

import (
	"io"
	"time"
)

// wsConn is the part of a gorilla WebSocket connection a client uses, so tests
// can stand in a connection that stalls. Only the client's write loop writes
// data frames; WriteControl and Close may be called from any goroutine.
type wsConn interface {
	SetReadLimit(limit int64)
	SetReadDeadline(t time.Time) error
	SetPongHandler(h func(appData string) error)
	ReadMessage() (int, []byte, error)
	SetWriteDeadline(t time.Time) error
	NextWriter(messageType int) (io.WriteCloser, error)
	WriteControl(messageType int, data []byte, deadline time.Time) error
	Close() error
}
