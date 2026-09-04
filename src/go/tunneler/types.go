package main

import (
	"encoding/json"
	"net"
	"net/http"

	ft "phenix/web/forward/forwardtypes"
)

type AddHeaderTransport struct {
	T http.RoundTripper

	Headers http.Header
}

func (t AddHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.Headers {
		for _, e := range v {
			req.Header.Add(k, e)
		}
	}

	return t.T.RoundTrip(req)
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

	ID int `json:"id"`

	Listening bool `json:"listening"`
	listener  net.Listener
}

type Message struct {
	MID     int             `json:"mid"`
	Type    MessageType     `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type Listeners []LocalListener

type listenerAction struct {
	ID   int `json:"id"`
	Port int `json:"port,omitempty"`
}
