// Package mmtest runs a fake minimega for tests of code that sends minimega
// commands through mmcli. It is imported only by tests, as net/http/httptest
// is.
package mmtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"

	"phenix/util/common"
	"phenix/util/polltest"
)

// Command is one command as the fake minimega received it.
type Command struct {
	// Raw is the command as sent, with every prefix mmcli adds.
	Raw string
	// Namespace is the namespace the command was sent in, "" for none.
	Namespace string
	// Base is the command without its .record, namespace, .columns and
	// .filter prefixes.
	Base string
}

// Handler answers one command sent to the fake minimega. It runs on the
// goroutine serving the command's connection, so it may block to hold that
// connection's reply open. Returning no responses answers with one empty,
// successful response from host "head"; returning [Disconnect] closes the
// connection without answering.
type Handler func(cmd Command) []*minicli.Response

// fake is the process-wide fake minimega.
type fake struct {
	mu       sync.Mutex
	handle   Handler
	received []Command
}

//nolint:gochecknoglobals // mmcli keeps one connection to the fake for the process
var (
	startOnce sync.Once
	errStart  error
	current   = &fake{mu: sync.Mutex{}, handle: nil, received: nil}

	// disconnect marks the reply Disconnect returns.
	disconnect = &minicli.Response{Host: "mmtest: disconnect"} //nolint:exhaustruct // a marker, never sent

	prefixes = regexp.MustCompile(
		`^(\.record false |namespace "([^"]*)" |\.columns ("[^"]*",)*"[^"]*" |\.filter ('[^']*'\S*|\S+) )`,
	)
)

// Use points mmcli at the fake minimega, which answers with handle until the
// test ends, and returns a function listing the commands received since. A
// nil handle answers every command with an empty success.
//
// The fake is started once per process, and mmcli's shared connection to it
// outlives a test, so tests using it must not run in parallel.
func Use(tb testing.TB, handle Handler) func() []Command {
	tb.Helper()

	startOnce.Do(func() { errStart = start() })

	if errStart != nil {
		tb.Fatalf("starting fake minimega: %v", errStart)
	}

	current.mu.Lock()
	current.handle, current.received = handle, nil
	current.mu.Unlock()

	tb.Cleanup(func() {
		current.mu.Lock()
		current.handle = nil
		current.mu.Unlock()
	})

	return func() []Command {
		current.mu.Lock()
		defer current.mu.Unlock()

		return slices.Clone(current.received)
	}
}

// Await waits until the fake has received, since Use, a command whose base
// starts with prefix, failing the test if none arrives within a few seconds.
func Await(tb testing.TB, prefix string) {
	tb.Helper()

	polltest.Until(tb, fmt.Sprintf("fake minimega receives a %q command", prefix), func() bool {
		current.mu.Lock()
		defer current.mu.Unlock()

		return Count(current.received, prefix) > 0
	})
}

// Disconnect is a Handler's reply that closes the connection without
// answering, as a minimega that went away does.
func Disconnect() []*minicli.Response {
	return []*minicli.Response{disconnect}
}

// Tabular is a tabular response from host.
func Tabular(host string, header []string, rows ...[]string) *minicli.Response {
	return &minicli.Response{Host: host, Header: header, Tabular: rows} //nolint:exhaustruct // partial response
}

// Text is a plain response from host.
func Text(host, body string) *minicli.Response {
	return &minicli.Response{Host: host, Response: body} //nolint:exhaustruct // partial response
}

// Bases lists the commands' bases, in the order received.
func Bases(cmds []Command) []string {
	out := make([]string, 0, len(cmds))

	for _, cmd := range cmds {
		out = append(out, cmd.Base)
	}

	return out
}

// Count counts the commands whose base starts with prefix.
func Count(cmds []Command, prefix string) int {
	var count int

	for _, cmd := range cmds {
		if strings.HasPrefix(cmd.Base, prefix) {
			count++
		}
	}

	return count
}

// parse splits a command as mmcli sends it into its namespace and base.
func parse(raw string) Command {
	cmd := Command{Raw: raw, Namespace: "", Base: ""}

	rest := raw

	for {
		m := prefixes.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}

		if m[4] >= 0 {
			cmd.Namespace = rest[m[4]:m[5]]
		}

		rest = rest[m[1]:]
	}

	cmd.Base = rest

	return cmd
}

// start listens on a minimega command socket and points common.MinimegaBase
// at it. On Linux the socket has an abstract name (a leading "@") rather than
// a file, so nothing is left on disk once the test binary exits; elsewhere it
// is in a new temporary directory.
func start() error {
	base := fmt.Sprintf("@phenix-mmtest-%d-%x", os.Getpid(), rand.Uint64())

	if runtime.GOOS != "linux" {
		dir, err := os.MkdirTemp("", "phenix-mmtest")
		if err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}

		base = dir
	}

	var config net.ListenConfig

	// miniclient.Dial joins its base and "minimega" the same way.
	listener, err := config.Listen(context.Background(), "unix", path.Join(base, "minimega"))
	if err != nil {
		return fmt.Errorf("listening on socket: %w", err)
	}

	common.MinimegaBase = base //nolint:reassign // mmcli dials the socket under this base

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go serve(conn)
		}
	}()

	return nil
}

// serve answers the commands sent on conn until it is closed.
func serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)

	for {
		var req miniclient.Request

		if err := dec.Decode(&req); err != nil {
			return
		}

		cmd := parse(req.Command)

		current.mu.Lock()
		current.received = append(current.received, cmd)
		handle := current.handle
		current.mu.Unlock()

		var resps []*minicli.Response

		if handle != nil {
			resps = handle(cmd)
		}

		if slices.Contains(resps, disconnect) {
			return
		}

		if len(resps) == 0 {
			resps = []*minicli.Response{{Host: "head"}}
		}

		//nolint:exhaustruct // Rendered and Suggest are for interactive clients
		if err := enc.Encode(&miniclient.Response{Resp: resps, More: false}); err != nil {
			return
		}
	}
}
