package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/util/common"
	ft "phenix/web/forward/forwardtypes"
)

func TestParseForwardDestination(t *testing.T) {
	tests := []struct {
		value    string
		wantDst  string
		wantPort int
		wantErr  error
	}{
		{value: "22", wantDst: "22", wantPort: 22},
		{value: "65535", wantDst: "65535", wantPort: 65535},
		{value: "vnc", wantDst: "VNC", wantPort: 5900},
		{value: "VNC", wantDst: "VNC", wantPort: 5900},
		{value: "0", wantErr: errBadForwardPort},
		{value: "65536", wantErr: errBadForwardPort},
		{value: "022", wantErr: errBadForwardPort},
		{value: "+22", wantErr: errBadForwardPort},
		{value: "ssh", wantErr: errBadForwardPort},
		{value: "", wantErr: errBadForwardPort},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			dst, port, err := parseForwardDestination(tc.value)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("parseForwardDestination(%q) error = %v, want %v", tc.value, err, tc.wantErr)
				}

				return
			}

			if err != nil || dst != tc.wantDst || port != tc.wantPort {
				t.Errorf("parseForwardDestination(%q) = %q, %d, %v, want %q, %d, nil", tc.value, dst, port, err, tc.wantDst, tc.wantPort)
			}
		})
	}
}

// Every port from 1 to 65535 parses back to itself, and no other number parses.
func TestParseForwardPortProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(-100, maxForwardPort+100).Draw(t, "port")

		got, err := parseForwardPort(strconv.Itoa(n))

		if n < 1 || n > maxForwardPort {
			if !errors.Is(err, errBadForwardPort) {
				t.Fatalf("parseForwardPort(%d) error = %v, want %v", n, err, errBadForwardPort)
			}

			return
		}

		if err != nil || got != n {
			t.Fatalf("parseForwardPort(%d) = %d, %v, want %d, nil", n, got, err, n)
		}
	})
}

func TestCheckForwardHost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.5", "::1", "fe80::1", "web", "web-1.lab.example"} {
		if err := checkForwardHost(host); err != nil {
			t.Errorf("checkForwardHost(%q) = %v, want nil", host, err)
		}
	}

	for _, host := range []string{"", "a b", "a;b", "-web", "web-", "a..b", "10.0.0.1 22", "$(id)", strings.Repeat("a", 64)} {
		if err := checkForwardHost(host); !errors.Is(err, errBadForwardHost) {
			t.Errorf("checkForwardHost(%q) = %v, want %v", host, err, errBadForwardHost)
		}
	}
}

// A host that checkForwardHost takes never holds a character that would end a
// minimega command argument.
func TestCheckForwardHostProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		host := rapid.String().Draw(t, "host")

		if checkForwardHost(host) == nil && strings.ContainsAny(host, " \t\r\n;\"'$`\\") {
			t.Fatalf("checkForwardHost(%q) = nil, want an error for a host with a separator", host)
		}
	})
}

// runVMForwardCmd runs phenix vm forward with args against the server at socket.
func runVMForwardCmd(t *testing.T, socket string, args ...string) (string, error) {
	t.Helper()

	original := common.UnixSocket
	common.UnixSocket = socket //nolint:reassign // point the command at the fake server

	t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

	cmd := newVMForwardCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

// The forward commands call the forward routes of the server's socket.
func TestVMForwardCommands(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSONBody(w, map[string][]ft.Listener{"listeners": {
				{Exp: "exp", VM: "web", SrcPort: 2222, DstHost: "127.0.0.1", DstPort: 22, Owner: "alice"},
			}})

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	out, err := runVMForwardCmd(t, fake.socket, "list", "exp", "web")
	if err != nil {
		t.Fatalf("vm forward list error = %v", err)
	}

	if !strings.Contains(out, "2222") || !strings.Contains(out, "alice") {
		t.Errorf("vm forward list = %q, want the forward of alice from 2222", out)
	}

	if _, err := runVMForwardCmd(t, fake.socket, "add", "exp", "web", "22", "--source-port", "2222"); err != nil {
		t.Fatalf("vm forward add error = %v", err)
	}

	if _, err := runVMForwardCmd(t, fake.socket, "add", "exp", "web", "vnc", "--host", "10.0.0.5"); err != nil {
		t.Fatalf("vm forward add vnc error = %v", err)
	}

	if _, err := runVMForwardCmd(t, fake.socket, "remove", "exp", "web", "vnc"); err != nil {
		t.Fatalf("vm forward remove error = %v", err)
	}

	path := "/api/v1/experiments/exp/vms/web/forwards"
	want := []recordedRequest{
		{method: http.MethodGet, path: path},
		{method: http.MethodPost, path: path, query: url.Values{"src": {"2222"}, "host": {"127.0.0.1"}, "dst": {"22"}}},
		{method: http.MethodPost, path: path, query: url.Values{"src": {"5900"}, "host": {"10.0.0.5"}, "dst": {"VNC"}}},
		{method: http.MethodDelete, path: path, query: url.Values{"host": {"127.0.0.1"}, "dst": {"5900"}}},
	}

	if diff := cmp.Diff(want, fake.recorded(), requestFields); diff != "" {
		t.Errorf("requests mismatch (-want +got):\n%s", diff)
	}
}

// A bad argument stops the command before it calls the server, and a server
// that does not run is named in the error.
func TestVMForwardRefusals(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	for _, args := range [][]string{
		{"add", "exp", "web", "0"},
		{"add", "exp", "web", "22", "--host", "a b"},
		{"add", "exp", "web", "22", "--source-port", "70000"},
		{"remove", "exp", "web", "ssh"},
	} {
		if _, err := runVMForwardCmd(t, fake.socket, args...); err == nil {
			t.Errorf("vm forward %q error = nil, want an error", args)
		}
	}

	if got := fake.recorded(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}

	_, err := runVMForwardCmd(t, shortSocketPath(t), "list", "exp", "web")
	if !errors.Is(err, errServerUnreachable) || !strings.Contains(err.Error(), "phenix ui is not running") {
		t.Errorf("vm forward list without a server error = %v, want %v naming phenix ui", err, errServerUnreachable)
	}
}
