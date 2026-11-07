package scorch

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// filebeatStopServer answers the Filebeat metrics requests with each of
// answers in turn, then the last; an empty answer breaks off its body.
func filebeatStopServer(t *testing.T, answers ...string) int {
	t.Helper()

	var asked atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		answer := answers[min(int(asked.Add(1))-1, len(answers)-1)]
		if answer == "" {
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("{"))

			panic(http.ErrAbortHandler)
		}

		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)

	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)

	return n
}

// filebeatClosedPort returns a local port with nothing listening on it.
func filebeatClosedPort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}

	port := l.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a TCP listener
	_ = l.Close()

	return port
}

// stopFilebeat waits for Filebeat to harvest its inputs, then interrupts it.
// It interrupts Filebeat at once when Filebeat cannot tell its progress, and
// when the context ends. The cases wait out the real Filebeat delays.
func TestFilebeatStopWhileRunning(t *testing.T) {
	t.Parallel()

	done := `{"filebeat.harvester.started":1,"filebeat.harvester.closed":1}`

	for name, tc := range map[string]struct {
		port     func(t *testing.T) int
		canceled bool
	}{
		"done after progress": {port: func(t *testing.T) int { t.Helper(); return filebeatStopServer(t, done) }},
		"metrics cut off":     {port: func(t *testing.T) int { t.Helper(); return filebeatStopServer(t, "") }},
		"no metrics":          {port: filebeatClosedPort},
		"context ends":        {port: filebeatClosedPort, canceled: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := exec.CommandContext(context.Background(), "/bin/sleep", "60")
			if err := cmd.Start(); err != nil {
				t.Fatalf("starting %v: %v", cmd.Args, err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			if tc.canceled {
				cancel()
			}

			defer cancel()

			port := tc.port(t)
			start := time.Now()

			Scorch{}.stopFilebeat(ctx, cmd, port)

			if took := time.Since(start); took < filebeatScanDelay {
				t.Errorf("stopFilebeat() returned after %v, want after the %v scan delay", took, filebeatScanDelay)
			}

			if tc.canceled {
				_ = cmd.Wait()
			}

			if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGINT {
				t.Errorf("stopFilebeat() left Filebeat in state %v, want interrupted", cmd.ProcessState)
			}
		})
	}
}
