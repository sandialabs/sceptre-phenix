package cmd

import (
	"bytes"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"phenix/util/common"
)

// Modes of the sockets the tests make.
const (
	// socketOwnerOnly is the mode of a socket only its owner may write to.
	socketOwnerOnly fs.FileMode = 0o700
	// socketGroup is the mode phenix ui --unix-socket-gid gives its socket.
	socketGroup fs.FileMode = 0o770
	// socketReadable is a socket every user may read, as umask 022 leaves
	// one.
	socketReadable fs.FileMode = 0o755
	// socketEveryone is a socket every user may write to.
	socketEveryone fs.FileMode = 0o777
)

// builderTestSocket listens on a unix socket with a short path, as macOS
// limits socket paths to 104 bytes, gives it mode, and closes it when the
// test ends.
func builderTestSocket(t *testing.T, mode fs.FileMode) string {
	t.Helper()

	path := shortSocketPath(t)

	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listening on %s: %v", path, err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("setting the mode of %s: %v", path, err)
	}

	return path
}

// TestBuilderCleartextTokenWarning checks that a token sent to an http URL
// of another host is a warning, and that https, loopback addresses and
// localhost, and no token, are not.
func TestBuilderCleartextTokenWarning(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		server string
		token  string
		warn   bool
	}{
		{server: "http://phenix.lab:3000", token: builderTestToken, warn: true},
		{server: "http://10.0.0.5/phenix", token: builderTestToken, warn: true},
		{server: "HTTP://phenix.lab", token: builderTestToken, warn: true},
		{server: "https://phenix.lab", token: builderTestToken, warn: false},
		{server: "http://phenix.lab", token: "", warn: false},
		{server: "http://localhost:3000", token: builderTestToken, warn: false},
		{server: "http://LOCALHOST", token: builderTestToken, warn: false},
		{server: "http://127.0.0.1:3000", token: builderTestToken, warn: false},
		{server: "http://127.4.5.6", token: builderTestToken, warn: false},
		{server: "http://[::1]:3000", token: builderTestToken, warn: false},
	} {
		warning := cleartextTokenWarning(test.server, test.token)

		if (warning != "") != test.warn {
			t.Errorf("cleartextTokenWarning(%q) = %q, want a warning: %t", test.server, warning, test.warn)
		}

		if test.token != "" && strings.Contains(warning, test.token) {
			t.Errorf("the warning for %q holds the token", test.server)
		}
	}

	// The command warns once, on standard error, without the token, and is
	// not refused.
	cmd := newBuilderDraftsListCmd()

	var stderr bytes.Buffer

	cmd.SetErr(&stderr)

	if err := cmd.ParseFlags([]string{"--url", "http://phenix.lab:3000", "--token", builderTestToken}); err != nil {
		t.Fatalf("parsing the flags: %v", err)
	}

	if _, err := builderClientFor(cmd); err != nil {
		t.Fatalf("builderClientFor returned error: %v", err)
	}

	text := stderr.String()

	if strings.Count(text, "warning: ") != 1 || !strings.Contains(text, "unencrypted to phenix.lab") ||
		strings.Contains(text, builderTestToken) {
		t.Errorf("standard error = %q, want one warning that the token travels unencrypted, without the token", text)
	}
}

// TestBuilderSocketCheck checks which files the commands send requests to as
// the socket of phenix ui: a socket owned by the user or by root that users
// outside its owner and group may not write to.
func TestBuilderSocketCheck(t *testing.T) {
	t.Parallel()

	for _, mode := range []fs.FileMode{socketOwnerOnly, socketGroup, socketReadable} {
		if err := checkBuilderSocket(builderTestSocket(t, mode)); err != nil {
			t.Errorf("a socket of mode %04o: %v, want it accepted", mode, err)
		}
	}

	everyone := builderTestSocket(t, socketEveryone)
	if err := checkBuilderSocket(everyone); err == nil || !strings.Contains(err.Error(), "can be written by every user") {
		t.Errorf("a socket every user may write to: error = %v, want it refused", err)
	}

	good := builderTestSocket(t, socketOwnerOnly)
	link := filepath.Join(filepath.Dir(good), "link")

	if err := os.Symlink(good, link); err != nil {
		t.Fatalf("linking to the socket: %v", err)
	}

	if err := checkBuilderSocket(link); err == nil || !strings.Contains(err.Error(), "it is a symbolic link") {
		t.Errorf("a link to a socket: error = %v, want it refused", err)
	}

	regular := writeTestFile(t, filepath.Dir(good), "file", []byte("not a socket"))
	if err := checkBuilderSocket(regular); err == nil || !strings.Contains(err.Error(), "it is a regular file") {
		t.Errorf("a regular file: error = %v, want it refused", err)
	}

	missing := filepath.Join(filepath.Dir(good), "none")
	if err := checkBuilderSocket(missing); !errors.Is(err, errServerUnreachable) || !strings.Contains(err.Error(), "start phenix ui") {
		t.Errorf("no file: error = %v, want the server unreachable and how to reach one", err)
	}

	info, err := os.Lstat(good)
	if err != nil {
		t.Fatalf("reading the socket: %v", err)
	}

	if err := builderSocketProblem(good, info, os.Getuid()); err != nil {
		t.Errorf("a socket of the user: %v, want it accepted", err)
	}

	// A socket of another user, unless that is root: the tests may run as
	// root, whose sockets every user accepts.
	if os.Getuid() != 0 {
		if err := builderSocketProblem(good, info, os.Getuid()+1); err == nil || !strings.Contains(err.Error(), "is owned by user ID") {
			t.Errorf("a socket of another user: error = %v, want it refused", err)
		}
	}
}

// TestBuilderSocketRefusedBeforeDialing checks that a command refuses a
// socket every user may write to before it sends any request there.
func TestBuilderSocketRefusedBeforeDialing(t *testing.T) {
	t.Setenv(builderURLEnv, "")
	t.Setenv(builderTokenEnv, "")

	socket := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := os.Chmod(socket.socket, socketEveryone); err != nil {
		t.Fatalf("setting the mode of the socket: %v", err)
	}

	previous := common.UnixSocket
	common.UnixSocket = socket.socket //nolint:reassign // the fake's socket

	t.Cleanup(func() { common.UnixSocket = previous }) //nolint:reassign // restore the socket

	_, _, err := runBuilderRemote("drafts", "list")
	wantExit(t, err, exitRefused)

	if !strings.Contains(err.Error(), "can be written by every user") {
		t.Errorf("error = %q, want it to say why the socket is refused", err)
	}

	if requests := socket.recorded(); len(requests) != 0 {
		t.Errorf("socket requests = %+v, want none", requests)
	}
}
