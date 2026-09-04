package main

import (
	"encoding/gob"
	"net"
	"testing"

	ft "phenix/web/forward/forwardtypes"
)

func TestListenerOperationsReportUnknownIDs(t *testing.T) {
	previousManager := localListeners
	localListeners = newListenerManager()

	defer func() { localListeners = previousManager }()

	tests := []struct {
		name    string
		message Message
	}{
		{
			name:    "move",
			message: Message{Type: MOVE, Payload: []int{1, 12345}},
		},
		{
			name:    "activate",
			message: Message{Type: ACTIVATE, Payload: 1},
		},
		{
			name:    "deactivate",
			message: Message{Type: DEACTIVATE, Payload: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := sendListenerMessage(t, test.message)
			if response.Error != "listener 1 not found" {
				t.Fatalf("response error = %q, want listener-not-found error", response.Error)
			}
		})
	}
}

func TestActivateLocalListenerReportsOccupiedPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("creating occupied listener: %v", err)
	}

	defer func() { _ = occupied.Close() }()

	var (
		port     = occupied.Addr().(*net.TCPAddr).Port
		listener = &LocalListener{Listener: ft.Listener{SrcPort: port}} //nolint:exhaustruct // partial initialization
	)

	if err := activateLocalListener(listener); err == nil {
		t.Fatal("activating on an occupied port returned nil error")
	}

	if listener.Listening {
		t.Fatal("listener marked active after bind failure")
	}
}

func sendListenerMessage(t *testing.T, message Message) Message {
	t.Helper()

	server, client := net.Pipe()
	defer func() { _ = client.Close() }()

	done := make(chan struct{})

	go func() {
		handleConnection(server)
		close(done)
	}()

	if err := gob.NewEncoder(client).Encode(message); err != nil {
		t.Fatalf("sending message: %v", err)
	}

	var response Message
	if err := gob.NewDecoder(client).Decode(&response); err != nil {
		t.Fatalf("receiving response: %v", err)
	}

	<-done

	return response
}
