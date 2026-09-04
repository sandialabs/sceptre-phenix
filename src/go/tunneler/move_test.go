package main

import (
	"net"
	"testing"

	ft "phenix/web/forward/forwardtypes"
)

func TestMoveLocalListenerActiveListener(t *testing.T) {
	var (
		listener = newTestLocalListener(t)
		newPort  = freeTCPPort(t)
	)

	if err := moveLocalListener(listener, newPort); err != nil {
		t.Fatalf("moving listener: %v", err)
	}

	if listener.SrcPort != newPort {
		t.Fatalf("listener port = %d, want %d", listener.SrcPort, newPort)
	}

	if !listener.Listening {
		t.Fatal("listener is inactive after successful move")
	}

	deactivateTestListener(t, listener)
}

func TestMoveLocalListenerRollsBackOnBindFailure(t *testing.T) {
	var (
		listener = newTestLocalListener(t)
		oldPort  = listener.SrcPort
	)

	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("creating occupied listener: %v", err)
	}

	defer func() { _ = occupied.Close() }()

	targetPort := occupied.Addr().(*net.TCPAddr).Port

	if err := moveLocalListener(listener, targetPort); err == nil {
		t.Fatal("moving listener to occupied port returned nil error")
	}

	if listener.SrcPort != oldPort {
		t.Fatalf("listener port after rollback = %d, want %d", listener.SrcPort, oldPort)
	}

	if !listener.Listening {
		t.Fatal("listener is inactive after rollback")
	}

	deactivateTestListener(t, listener)
}

func TestMoveLocalListenerInactiveListener(t *testing.T) {
	var (
		listener = &LocalListener{Listener: ft.Listener{SrcPort: freeTCPPort(t)}}
		newPort  = freeTCPPort(t)
	)

	if err := moveLocalListener(listener, newPort); err != nil {
		t.Fatalf("moving inactive listener: %v", err)
	}

	if listener.SrcPort != newPort {
		t.Fatalf("listener port = %d, want %d", listener.SrcPort, newPort)
	}

	if listener.Listening {
		t.Fatal("inactive listener became active after move")
	}
}

func newTestLocalListener(t *testing.T) *LocalListener {
	t.Helper()

	listener := &LocalListener{Listener: ft.Listener{SrcPort: freeTCPPort(t)}}

	if err := activateLocalListener(listener); err != nil {
		t.Fatalf("activating test listener: %v", err)
	}

	return listener
}

func deactivateTestListener(t *testing.T, listener *LocalListener) {
	t.Helper()

	if err := deactivateLocalListener(listener); err != nil {
		t.Fatalf("deactivating test listener: %v", err)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("getting free TCP port: %v", err)
	}

	defer func() { _ = listener.Close() }()

	return listener.Addr().(*net.TCPAddr).Port
}
