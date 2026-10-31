package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"phenix/util/plog"
	"phenix/web/broker"
	"phenix/web/lifecycle"
	"phenix/web/scorch"
)

// SignalError is the cause of a Start context canceled by a signal.
type SignalError struct {
	Signal os.Signal
}

func (c SignalError) Error() string {
	return "received signal " + c.Signal.String()
}

// servedHTTP is an HTTP server Start runs on a listener it opened.
type servedHTTP struct {
	name  string
	srv   *http.Server
	ln    net.Listener
	serve func(srv *http.Server, ln net.Listener) error
}

// uiServer is what Start runs until its context is done.
type uiServer struct {
	grace   time.Duration
	servers []servedHTTP
	// the Unix socket's path, removed at shutdown; empty for none
	socket string
	// stops the background loops the servers share
	stopBackground context.CancelFunc
}

// run serves until ctx is done or a server fails, then shuts down. It returns
// the failure, or nil for a shutdown ctx asked for.
func (s *uiServer) run(ctx context.Context) error {
	lifecycle.Start()

	failed := make(chan error, len(s.servers))

	for _, h := range s.servers {
		go func() {
			if err := h.serve(h.srv, h.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				failed <- fmt.Errorf("serving %s: %w", h.name, err)
			}
		}()
	}

	select {
	case err := <-failed:
		plog.Error(plog.TypeSystem, "server failed", "err", err)
		s.shutdown(err)

		return err
	case <-ctx.Done():
		s.shutdown(context.Cause(ctx))

		return nil
	}
}

// closeListeners closes the listeners of servers that never served, and stops
// the background loops.
func (s *uiServer) closeListeners() {
	for _, h := range s.servers {
		_ = h.ln.Close()
	}

	s.stopBackground()
}

// shutdown stops everything the server runs within its grace period; jobs get
// half of it before they are canceled. Nothing waits past it.
//
//nolint:funlen // one step after another
func (s *uiServer) shutdown(cause error) {
	began := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), s.grace)
	defer cancel()

	var signal SignalError
	if errors.As(cause, &signal) {
		plog.Info(plog.TypeSystem, "shutting down server", "signal", signal.Signal.String(), "grace_period", s.grace.String())
	} else {
		plog.Info(plog.TypeSystem, "shutting down server", "reason", cause.Error(), "grace_period", s.grace.String())
	}

	lifecycle.Stop()

	var (
		wg sync.WaitGroup
		mu sync.Mutex

		cut           []string // servers whose requests the deadline cut off
		clients       int
		running       []string
		consolesLeft  int
		terminalsLeft int
	)

	wg.Add(len(s.servers))

	for _, h := range s.servers {
		go func() {
			defer wg.Done()

			if err := h.srv.Shutdown(ctx); err != nil {
				_ = h.srv.Close()

				mu.Lock()
				cut = append(cut, h.name)
				mu.Unlock()
			}
		}()
	}

	wg.Add(4) //nolint:mnd // the goroutines below

	go func() {
		defer wg.Done()

		clients = broker.Shutdown(ctx)
	}()

	go func() {
		defer wg.Done()

		for _, j := range jobs.ShutdownContext(ctx, s.grace/2) { //nolint:mnd // half the grace period
			running = append(running, j.Kind+" "+j.ID)
		}
	}()

	go func() {
		defer wg.Done()

		consolesLeft = consoles.endAll(ctx, consoleEndedShutdown)
	}()

	go func() {
		defer wg.Done()

		terminalsLeft = scorch.KillTerminals(ctx)
	}()

	handlers := lifecycle.Wait(ctx)

	wg.Wait()

	s.stopBackground()

	if s.socket != "" {
		if err := os.Remove(s.socket); err != nil && !errors.Is(err, fs.ErrNotExist) {
			plog.Warn(plog.TypeSystem, "removing Unix socket", "path", s.socket, "err", err)
		}
	}

	took := time.Since(began).Round(time.Millisecond).String()

	var left []any

	if len(cut) > 0 {
		slices.Sort(cut)
		left = append(left, "requests_cut_off", cut)
	}

	if clients > 0 {
		left = append(left, "websocket_clients", clients)
	}

	if len(running) > 0 {
		slices.Sort(running)
		left = append(left, "jobs", running)
	}

	if consolesLeft > 0 {
		left = append(left, "minimega_consoles", consolesLeft)
	}

	if terminalsLeft > 0 {
		left = append(left, "scorch_terminals", terminalsLeft)
	}

	if len(handlers) > 0 {
		left = append(left, "open_handlers", handlers)
	}

	if len(left) == 0 {
		plog.Info(plog.TypeSystem, "shutdown complete", "duration", took)

		return
	}

	plog.Warn(
		plog.TypeSystem, "shutdown grace period ended with work still running",
		append([]any{"duration", took, "grace_period", s.grace.String()}, left...)...,
	)
}
