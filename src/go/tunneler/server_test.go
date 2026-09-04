package main

import (
	"encoding/json"
	"net"
	"testing"

	ft "phenix/web/forward/forwardtypes"
)

func TestLocalListenerJSONUsesStableFieldNames(t *testing.T) {
	data, err := json.Marshal(LocalListener{
		ID:        7,
		Listening: true,
	})
	if err != nil {
		t.Fatalf("marshaling local listener: %v", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decoding local listener JSON: %v", err)
	}

	for _, field := range []string{"id", "listening"} {
		if _, ok := payload[field]; !ok {
			t.Errorf("listener JSON missing %q: %s", field, data)
		}
	}

	for _, field := range []string{"ID", "Listening"} {
		if _, ok := payload[field]; ok {
			t.Errorf("listener JSON unexpectedly contains %q: %s", field, data)
		}
	}
}

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
			message: Message{Type: MOVE, Payload: json.RawMessage(`{"id":1,"port":12345}`)},
		},
		{
			name:    "activate",
			message: Message{Type: ACTIVATE, Payload: json.RawMessage(`{"id":1}`)},
		},
		{
			name:    "deactivate",
			message: Message{Type: DEACTIVATE, Payload: json.RawMessage(`{"id":1}`)},
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
		listener = &LocalListener{Listener: ft.Listener{SrcPort: port}}
	)

	if err := activateLocalListener(listener); err == nil {
		t.Fatal("activating on an occupied port returned nil error")
	}

	if listener.Listening {
		t.Fatal("listener marked active after bind failure")
	}
}

func TestListenerOperationsRejectMalformedPayloads(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{name: "empty move payload", message: Message{Type: MOVE, Payload: json.RawMessage(`[]`)}},
		{name: "short move payload", message: Message{Type: MOVE, Payload: json.RawMessage(`[1]`)}},
		{name: "wrong move payload", message: Message{Type: MOVE, Payload: json.RawMessage(`"1,12345"`)}},
		{name: "wrong activate payload", message: Message{Type: ACTIVATE, Payload: json.RawMessage(`"1"`)}},
		{name: "wrong deactivate payload", message: Message{Type: DEACTIVATE, Payload: json.RawMessage(`"1"`)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := sendListenerMessage(t, test.message)
			if response.Error == "" {
				t.Fatal("malformed payload returned no error")
			}
		})
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

	if err := json.NewEncoder(client).Encode(message); err != nil {
		t.Fatalf("sending message: %v", err)
	}

	var response Message
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatalf("receiving response: %v", err)
	}

	<-done

	return response
}
