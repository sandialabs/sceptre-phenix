package mm

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/common"
)

// fakeHandler answers one command from the fake minimega with the per-host
// responses a mesh would return.
type fakeHandler func(cmd string) minicli.Responses

// fake is the single fake minimega socket shared by this package's tests; the
// handler is swapped per test. One socket for the whole run keeps the mmcli
// shared connection valid across tests.
var fake struct { //nolint:gochecknoglobals // test fixture
	mu     sync.Mutex
	handle fakeHandler
	seen   []string
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "phenix-mm-test")
	if err != nil {
		panic(err)
	}

	listener, err := net.Listen("unix", filepath.Join(dir, "minimega"))
	if err != nil {
		panic(err)
	}

	common.MinimegaBase = dir //nolint:reassign // test fixture

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go serveFake(conn)
		}
	}()

	code := m.Run()

	_ = listener.Close()
	_ = os.RemoveAll(dir)

	os.Exit(code)
}

func serveFake(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	var (
		dec = json.NewDecoder(conn)
		enc = json.NewEncoder(conn)
	)

	for {
		var req miniclient.Request

		if err := dec.Decode(&req); err != nil {
			return
		}

		fake.mu.Lock()
		handle := fake.handle
		fake.seen = append(fake.seen, req.Command)
		fake.mu.Unlock()

		var resp minicli.Responses

		if handle != nil {
			resp = handle(req.Command)
		}

		if err := enc.Encode(&miniclient.Response{Resp: resp}); err != nil {
			return
		}
	}
}

// useFakeHandler installs handle for the duration of the test.
func useFakeHandler(t *testing.T, handle fakeHandler) {
	t.Helper()

	fake.mu.Lock()
	fake.handle, fake.seen = handle, nil
	fake.mu.Unlock()

	t.Cleanup(func() {
		fake.mu.Lock()
		fake.handle, fake.seen = nil, nil
		fake.mu.Unlock()
	})
}

// fakeSaw reports whether any command sent to the fake contained substr.
func fakeSaw(substr string) bool {
	fake.mu.Lock()
	defer fake.mu.Unlock()

	return slices.ContainsFunc(fake.seen, func(cmd string) bool {
		return strings.Contains(cmd, substr)
	})
}

func tabular(host string, header []string, rows ...[]string) *minicli.Response {
	return &minicli.Response{Host: host, Header: header, Tabular: rows}
}

func text(host, body string) *minicli.Response {
	return &minicli.Response{Host: host, Response: body}
}

func data(host string, v any) *minicli.Response {
	return &minicli.Response{Host: host, Data: v}
}

func errRow(host, msg string) *minicli.Response {
	return &minicli.Response{Host: host, Error: msg}
}
