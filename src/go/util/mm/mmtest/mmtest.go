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

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/common"
	"phenix/util/polltest"
)

// Command is one command as the fake minimega received it.
type Command struct {
	// Raw is the command as sent, with every prefix mmcli adds.
	Raw string
	// Namespace is the namespace the command was sent in, "" for none.
	Namespace string
	// Base is the command without its .record, .preprocess, namespace,
	// .columns and .filter prefixes.
	Base string
}

// Handler answers one command on its connection's goroutine, so it may block to hold the reply.
// No responses sends an empty success from "head"; [Disconnect] and [Truncated] close the connection.
type Handler func(cmd Command) []*minicli.Response

// fake is the process-wide fake minimega.
type fake struct {
	mu       sync.Mutex
	handle   Handler
	received []Command
	users    []string     // names of the tests in Use, outermost first
	tbs      []testing.TB // the tests in Use, in the order of users
}

//nolint:gochecknoglobals // mmcli keeps one connection to the fake for the process
var (
	startOnce sync.Once
	errStart  error
	current   = &fake{mu: sync.Mutex{}, handle: nil, received: nil, users: nil, tbs: nil}

	// disconnect and truncate mark the replies Disconnect and Truncated return.
	disconnect = &minicli.Response{Host: "mmtest: disconnect"} //nolint:exhaustruct // a marker, never sent
	truncate   = &minicli.Response{Host: "mmtest: truncate"}   //nolint:exhaustruct // a marker, never sent

	prefixes = regexp.MustCompile(
		`^(\.record false |\.preprocess false |namespace "([^"]*)" |\.columns ("[^"]*",)*"[^"]*" |\.filter ('[^']*'\S*|\S+) )`,
	)
)

// Use points mmcli at the fake minimega, which answers with handle (nil answers every command with an empty success)
// until the test ends, and returns a function listing the commands received since. As minimega runs [os.ExpandEnv] on
// every argument, the fake fails the test for a command with a "$" that expansion would change, and answers it with an
// error instead of calling handle, unless the command starts with ".preprocess false". The fake and mmcli's connection to it
// outlive a test, so tests using it must not run in parallel: Use fails a test while a test that is not it or its parent
// uses the fake.
func Use(tb testing.TB, handle Handler) func() []Command {
	tb.Helper()

	startOnce.Do(func() { errStart = start() })

	if errStart != nil {
		tb.Fatalf("starting fake minimega: %v", errStart)
	}

	name := tb.Name()

	current.mu.Lock()

	for _, user := range current.users {
		if user != name && !strings.HasPrefix(name, user+"/") {
			current.mu.Unlock()
			tb.Fatalf("mmtest.Use in %s while %s uses the fake minimega: tests using it must not run in parallel",
				name, user)
		}
	}

	current.users = append(current.users, name)
	current.tbs = append(current.tbs, tb)
	current.handle, current.received = handle, nil
	current.mu.Unlock()

	tb.Cleanup(func() {
		current.mu.Lock()
		current.handle = nil

		if i := slices.Index(current.users, name); i >= 0 {
			current.users = slices.Delete(current.users, i, i+1)
			current.tbs = slices.Delete(current.tbs, i, i+1)
		}

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

// Truncated is a Handler's reply that sends resps as the start of a longer answer and then
// closes the connection, as a minimega that went away mid-reply does.
func Truncated(resps ...*minicli.Response) []*minicli.Response {
	return append(slices.Clone(resps), truncate)
}

// Error is a response from host reporting that the command failed there with
// msg; other hosts' responses in the same reply may still succeed.
func Error(host, msg string) *minicli.Response {
	return &minicli.Response{Host: host, Error: msg} //nolint:exhaustruct // partial response
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

// expands reports whether minimega's [os.ExpandEnv] of the arguments would change raw. No environment is assumed: a
// variable that is not set expands to "".
func expands(raw string) bool {
	return os.Expand(raw, func(string) string { return "" }) != raw
}

// parse splits a command as mmcli sends it into its namespace and base, and reports whether it has a ".preprocess
// false" prefix.
func parse(raw string) (Command, bool) {
	cmd := Command{Raw: raw, Namespace: "", Base: ""}
	noPreprocess := false

	rest := raw

	for {
		m := prefixes.FindStringSubmatchIndex(rest)
		if m == nil {
			break
		}

		if m[4] >= 0 {
			cmd.Namespace = rest[m[4]:m[5]]
		}

		noPreprocess = noPreprocess || rest[:m[1]] == ".preprocess false "

		rest = rest[m[1]:]
	}

	cmd.Base = rest

	return cmd, noPreprocess
}

// start listens on a minimega command socket and points common.MinimegaBase
// at it. On Linux the socket has an abstract name (a leading "@") rather than
// a file, so nothing is left on disk once the test binary exits; elsewhere it
// is in a new temporary directory.
func start() error {
	base := fmt.Sprintf("@phenix-mmtest-%d-%x", os.Getpid(), rand.Uint64()) //nolint:gosec // G404 a unique test socket name

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

		cmd, noPreprocess := parse(req.Command)

		current.mu.Lock()
		current.received = append(current.received, cmd)
		handle := current.handle
		expanded := !noPreprocess && expands(req.Command)

		// report under the lock, so the test cannot end and leave Use before Errorf
		if expanded && len(current.tbs) > 0 {
			current.tbs[len(current.tbs)-1].Errorf(
				"mmtest: minimega would expand the $ in %q to %q", req.Command, os.ExpandEnv(req.Command),
			)
		}
		current.mu.Unlock()

		var resps []*minicli.Response

		switch {
		case expanded:
			resps = []*minicli.Response{{Host: "head", Error: "mmtest: minimega would expand the $ in this command"}}
		case handle != nil:
			resps = handle(cmd)
		}

		if slices.Contains(resps, disconnect) {
			return
		}

		if i := slices.Index(resps, truncate); i >= 0 {
			//nolint:exhaustruct // Rendered and Suggest are for interactive clients
			_ = enc.Encode(&miniclient.Response{Resp: slices.Delete(slices.Clone(resps), i, i+1), More: true})

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
