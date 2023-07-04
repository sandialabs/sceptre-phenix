package main

import (
	"encoding/gob"
	"net"
	"net/http"

	ft "phenix/web/forward/forwardtypes"
)

type AddHeaderTransport struct {
	T http.RoundTripper

	Headers http.Header
}

func (this AddHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range this.Headers {
		for _, e := range v {
			req.Header.Add(k, e)
		}
	}

	return this.T.RoundTrip(req)
}

type MessageType string

const (
	LISTENERS  MessageType = "LISTENERS"
	CREATE     MessageType = "CREATE"
	DELETE     MessageType = "DELETE"
	MOVE       MessageType = "MOVE"
	ACTIVATE   MessageType = "ACTIVATE"
	DEACTIVATE MessageType = "DEACTIVATE"
)

type LocalListener struct {
	ft.Listener

	ID int

	Listening bool
	listener  net.Listener
}

type Message struct {
	MID     int
	Type    MessageType
	Payload any
	Error   string
}

type Listeners []LocalListener

func init() {
	gob.Register(Listeners{})
}
