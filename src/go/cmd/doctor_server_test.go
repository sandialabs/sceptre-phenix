package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/iotest"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"
	"golang.org/x/sys/unix"
	"pgregory.net/rapid"

	"phenix/version"
)

type doctorRoundTrip func(*http.Request) (*http.Response, error)

func (f doctorRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// doctorServer is a client whose requests rt answers.
func doctorServer(rt doctorRoundTrip) func(string, time.Duration) *http.Client {
	return func(_ string, timeout time.Duration) *http.Client {
		return &http.Client{Transport: rt, Timeout: timeout}
	}
}

// doctorServerAnswer answers every request with status, the version header
// (none when empty) and body.
func doctorServerAnswer(status int, serverVersion, body string) func(string, time.Duration) *http.Client {
	return doctorServer(func(req *http.Request) (*http.Response, error) {
		header := http.Header{}
		if serverVersion != "" {
			header.Set("X-Phenix-Version", serverVersion)
		}

		return &http.Response{
			StatusCode: status, Status: http.StatusText(status), Header: header,
			Body: io.NopCloser(strings.NewReader(body)), Request: req,
		}, nil
	})
}

// doctorServerDialError fails every request as a dial on the socket failing with errno.
func doctorServerDialError(errno syscall.Errno) func(string, time.Duration) *http.Client {
	return doctorServer(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", errno)}
	})
}

// doctorMinimega answers args with args and mesh status with a mesh of size
// nodes, and fails any other command, recording each one.
type doctorMinimega struct {
	mu       sync.Mutex
	args     map[string]string
	size     string
	commands []string
	answers  map[string][]*minicli.Response // the answers to other commands, by command
}

func (m *doctorMinimega) run(_, command string, _ time.Duration) ([]*minicli.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.commands = append(m.commands, command)

	switch command {
	case "args":
		header, values := make([]string, 0, len(m.args)), make([]string, 0, len(m.args))

		for _, name := range slices.Sorted(maps.Keys(m.args)) {
			header, values = append(header, name), append(values, m.args[name])
		}

		return []*minicli.Response{{Host: "mm1", Header: header, Tabular: [][]string{values}}}, nil
	case "mesh status":
		return []*minicli.Response{{
			Host: "mm1", Header: []string{"size", "degree", "peers"}, Tabular: [][]string{{m.size, "2", "2"}},
		}}, nil
	}

	if answer, ok := m.answers[command]; ok {
		return answer, nil
	}

	if answer, ok := doctorEmptyNamespace[command]; ok && m.answers == nil {
		return answer, nil
	}

	return nil, errors.New("invalid command")
}

// doctorEmptyNamespace is what a minimega with only its minimega namespace, and no VMs, taps or captures, answers.
//
//nolint:gochecknoglobals // constant answers
var doctorEmptyNamespace = map[string][]*minicli.Response{
	"namespace": {
		{Host: "mm1", Header: []string{"namespace", "vlans", "active"}, Tabular: [][]string{{"minimega", "", "true"}}},
	},
	"namespace minimega vm info": {{Host: "mm1", Header: []string{"id", "name", "state", "pid", "tap"}}},
	"namespace minimega tap":     {{Host: "mm1", Header: []string{"bridge", "tap", "vlan"}}},
	"namespace minimega capture": {{Host: "mm1", Header: []string{"bridge", "type", "interface", "mode", "compress", "path"}}},
	"namespace minimega vlans":   {{Host: "mm1", Header: []string{"alias", "vlan"}}},
	"namespace minimega vlans range": {
		{Host: "mm1", Header: []string{"min", "max", "next"}, Tabular: [][]string{{"101", "4096", "101"}}},
	},
	"namespace minimega vlans blacklist": {{Host: "mm1", Header: []string{"vlan"}}},
	"mesh list all":                      {{Host: "mm1", Response: "mm1"}},
}

func doctorMinimegaArgs(dir string) map[string]string {
	return map[string]string{
		"base": filepath.Join(dir, "minimega"), "filepath": filepath.Join(dir, "files"),
		"degree": "0", "abssnapshot": "true", "logfile": "",
	}
}

const doctorTestOptions = `{"bridge-mode":"manual","deploy-mode":"no-headnode","use-gre-mesh":false}`

// The server check asks phenix ui on the unix socket for its options, warns
// when it is not there, its version or modes differ, and fails only when this
// user may not connect.
func TestServerChecks(t *testing.T) {
	t.Parallel()

	const socket = "/run/phenix.sock"

	hung := doctorServer(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()

		return nil, req.Context().Err()
	})

	cutOff := doctorServer(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{},
			Body: io.NopCloser(iotest.ErrReader(errors.New("connection reset"))), Request: req,
		}, nil
	})

	tests := map[string]struct {
		socket         string
		client         func(string, time.Duration) *http.Client
		mode           os.FileMode // of the file at socket; 0 is none
		bridge, deploy string
		want           []doctorCheck
		wantUp         bool // a phenix ui answered
	}{
		"answer cut off": {
			socket: socket, client: cutOff, wantUp: true,
			want: []doctorCheck{row("", doctorWarn, "reading the answer of phenix ui on /run/phenix.sock: connection reset")},
		},
		"no socket": {
			want: []doctorCheck{row("", doctorPass, "unix-socket is empty, so phenix commands ask no phenix ui for its modes")},
		},
		"answers": {
			socket: socket, client: doctorServerAnswer(http.StatusOK, version.API(), doctorTestOptions), bridge: "manual",
			wantUp: true,
			want: []doctorCheck{
				row("", doctorPass, "phenix ui "+version.API()+" answers on /run/phenix.sock"),
				row("server bridge-mode", doctorPass, "phenix commands use manual, as phenix ui does"),
				row("server deploy-mode", doctorPass, "phenix commands use no-headnode, from phenix ui"),
			},
		},
		"not running": {
			socket: socket, client: doctorServerDialError(syscall.ENOENT),
			want: []doctorCheck{row("", doctorWarn,
				"no phenix ui on /run/phenix.sock: phenix ui is not running, or serves another unix socket")},
		},
		"stale socket": {
			socket: socket, client: doctorServerDialError(syscall.ECONNREFUSED), mode: os.ModeSocket | 0o755,
			want: []doctorCheck{row("", doctorWarn, "/run/phenix.sock is a stale socket file: no phenix ui listens on it")},
		},
		"regular file, refused as on Linux": {
			socket: socket, client: doctorServerDialError(syscall.ECONNREFUSED), mode: 0o600,
			want: []doctorCheck{row("", doctorWarn, "/run/phenix.sock is not a socket, so no phenix ui listens on it")},
		},
		"regular file, refused as on macOS": {
			socket: socket, client: doctorServerDialError(syscall.ENOTSOCK), mode: 0o600,
			want: []doctorCheck{row("", doctorWarn, "/run/phenix.sock is not a socket, so no phenix ui listens on it")},
		},
		"permission denied": {
			socket: socket, client: doctorServerDialError(syscall.EACCES),
			want: []doctorCheck{row("", doctorFail,
				"this user cannot connect to /run/phenix.sock: dial unix: connect: permission denied")},
		},
		"no answer": {
			socket: socket, client: hung,
			want: []doctorCheck{row("", doctorWarn, "phenix ui on /run/phenix.sock did not answer within 50ms")},
		},
		"refused": {
			socket: socket, wantUp: true, client: doctorServerAnswer(http.StatusForbidden, version.API(), `{}`),
			want: []doctorCheck{row("", doctorWarn,
				"phenix ui on /run/phenix.sock answered GET /api/v1/options with Forbidden")},
		},
		"bad options": {
			socket: socket, wantUp: true, client: doctorServerAnswer(http.StatusOK, version.API(), `[`),
			want: []doctorCheck{row("", doctorWarn,
				"phenix ui on /run/phenix.sock answered with options that do not parse: unexpected end of JSON input")},
		},
		"other version and modes": {
			socket: socket, wantUp: true, bridge: "manual", deploy: "all",
			client: doctorServerAnswer(http.StatusOK, "9.9.9", `{"bridge-mode":"auto","deploy-mode":"no-headnode"}`),
			want: []doctorCheck{
				row("", doctorWarn, "phenix ui on /run/phenix.sock is 9.9.9, and this phenix is "+version.API()),
				row("server bridge-mode", doctorWarn, "phenix commands use auto, from phenix ui, in place of the manual set here"),
				row("server deploy-mode", doctorWarn, "phenix commands use all, set here, in place of phenix ui's no-headnode"),
			},
		},
		"auto in phenix ui, bridge-mode not set here": {
			socket: socket, client: doctorServerAnswer(http.StatusOK, version.API(), `{"bridge-mode":"auto","deploy-mode":"all"}`),
			wantUp: true,
			want: []doctorCheck{
				row("", doctorPass, "phenix ui "+version.API()+" answers on /run/phenix.sock"),
				row("server bridge-mode", doctorPass, "phenix commands use auto, from phenix ui"),
				row("server deploy-mode", doctorPass, "phenix commands use all, from phenix ui"),
			},
		},
		"no version, auto set here, bad deploy mode": {
			socket: socket, wantUp: true, bridge: "auto", deploy: "some",
			client: doctorServerAnswer(http.StatusOK, "", doctorTestOptions),
			want: []doctorCheck{
				row("", doctorWarn, "phenix ui on /run/phenix.sock does not give its version, and this phenix is "+version.API()),
				row("server bridge-mode", doctorWarn, "phenix commands use auto, set here, and phenix ui uses manual"),
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lstat := func(string) (os.FileInfo, error) {
				if tc.mode == 0 {
					return nil, os.ErrNotExist
				}

				return doctorEnvMode{FileInfo: nil, mode: tc.mode}, nil
			}

			got, up := serverChecks(t.Context(), tc.socket, 50*time.Millisecond, tc.client, lstat, tc.bridge, tc.deploy)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("serverChecks() mismatch (-want +got):\n%s", diff)
			}

			if up != tc.wantUp {
				t.Errorf("serverChecks() answered = %t, want %t", up, tc.wantUp)
			}
		})
	}
}

// Whatever the two modes, the mode rows name the one every other phenix
// command uses, as the root command picks it, and warn exactly when a mode
// set here differs from phenix ui's.
func TestServerModeChecksProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		localBridge := rapid.SampledFrom([]string{"", "manual", "auto", "AUTO"}).Draw(t, "localBridge")
		serverBridge := rapid.SampledFrom([]string{"manual", "auto"}).Draw(t, "serverBridge")
		localDeploy := rapid.SampledFrom([]string{"", "all", "no-headnode", "only-headnode"}).Draw(t, "localDeploy")
		serverDeploy := rapid.SampledFrom([]string{"all", "no-headnode", "only-headnode"}).Draw(t, "serverDeploy")

		// the root command: the local bridge mode, unless phenix ui's is auto;
		// phenix ui's deploy mode, unless one is set locally
		bridge := strings.ToLower(localBridge)
		if bridge == "" {
			bridge = "manual"
		}

		used := bridge
		if serverBridge == "auto" {
			used = "auto"
		}

		check, ok := serverBridgeModeCheck(localBridge, serverBridge)
		if !ok || !strings.HasPrefix(check.Detail, "phenix commands use "+used+",") {
			t.Fatalf("serverBridgeModeCheck(%q, %q) = %+v, want it to name %s", localBridge, serverBridge, check, used)
		}

		if warns := check.Status == doctorWarn; warns != (localBridge != "" && bridge != serverBridge) {
			t.Fatalf("serverBridgeModeCheck(%q, %q) status = %s", localBridge, serverBridge, check.Status)
		}

		usedDeploy := localDeploy
		if usedDeploy == "" {
			usedDeploy = serverDeploy
		}

		check, ok = serverDeployModeCheck(localDeploy, serverDeploy)
		if !ok || !strings.HasPrefix(check.Detail, "phenix commands use "+usedDeploy+",") {
			t.Fatalf("serverDeployModeCheck(%q, %q) = %+v, want it to name %s", localDeploy, serverDeploy, check, usedDeploy)
		}

		if warns := check.Status == doctorWarn; warns != (usedDeploy != serverDeploy) {
			t.Fatalf("serverDeployModeCheck(%q, %q) status = %s", localDeploy, serverDeploy, check.Status)
		}
	})
}

// Each way a server row warns or fails has its own hint.
func TestServerHint(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		check doctorCheck
		want  string
	}{
		"permission": {check: row("server", doctorFail, "this user cannot connect"), want: socketPermissionHint},
		"stale":      {check: row("server", doctorWarn, "x is a stale socket file"), want: "start phenix ui, which replaces"},
		"no answer":  {check: row("server", doctorWarn, "phenix ui on x did not answer within 1s"), want: "check that phenix ui"},
		"version":    {check: row("server", doctorWarn, "x is 1, and this phenix is 2"), want: "run the same phenix version"},
		"missing":    {check: row("server", doctorWarn, "no phenix ui on x"), want: "start phenix ui, or set unix-socket"},
		"not socket": {check: row("server", doctorWarn, "x is not a socket, so no"), want: "start phenix ui, or set unix-socket"},
		"bridge":     {check: row(serverBridgeRow, doctorWarn, "phenix commands use auto"), want: "set bridge-mode"},
		"deploy":     {check: row(serverDeployRow, doctorWarn, "phenix commands use all"), want: "unset deploy-mode"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := serverHint(tc.check); !strings.HasPrefix(got, tc.want) {
				t.Errorf("serverHint(%+v) = %q, want prefix %q", tc.check, got, tc.want)
			}
		})
	}
}

// A failed connection to minimega's socket names its likely cause, and the
// hint says what to do about it.
func TestMinimegaDialProblem(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err         error
		wantProblem string
		wantHint    string
	}{
		"no socket": {
			err:         errors.New("unable to dial: dial unix /tmp/mm/minimega: connect: no such file or directory"),
			wantProblem: "no socket there: minimega is not running, or base-dir.minimega names another directory",
			wantHint:    "start minimega",
		},
		"stale": {
			err:         errors.New("unable to dial: dial unix /tmp/mm/minimega: connect: connection refused"),
			wantProblem: "a stale socket file: minimega stopped without removing it",
			wantHint:    "restart minimega",
		},
		"permission": {
			err:         errors.New("unable to dial: dial unix /tmp/mm/minimega: connect: permission denied"),
			wantProblem: "this user may not connect to it",
			wantHint:    "run phenix doctor as the user",
		},
		"other": {err: errors.New("invalid command"), wantHint: "start minimega"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := minimegaDialProblem(tc.err); got != tc.wantProblem {
				t.Errorf("minimegaDialProblem(%v) = %q, want %q", tc.err, got, tc.wantProblem)
			}

			check := minimegaCheck(t.Context(), "/tmp/mm", time.Second, func(time.Duration) (string, error) { return "", tc.err })
			if got := minimegaHint(check); !strings.HasPrefix(got, tc.wantHint) {
				t.Errorf("minimegaHint(%+v) = %q, want prefix %q", check, got, tc.wantHint)
			}
		})
	}

	timedOut := row("minimega", doctorFail, "no answer on /tmp/mm/minimega within 3s")
	if got := minimegaHint(timedOut); !strings.HasPrefix(got, "check that minimega answers requests") {
		t.Errorf("minimegaHint(%+v) = %q, want the timeout hint", timedOut, got)
	}
}

// minimega-env reads minimega's flags only after minimega gave its version,
// with global commands only, and checks the files directory, base and mesh.
func TestMinimegaEnvChecks(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	files := filepath.Join(dir, "files")
	readOnly := func(path string, mode uint32) error {
		if path == files && mode&unix.W_OK != 0 {
			return unix.EACCES
		}

		return nil
	}

	tests := map[string]struct {
		up     bool
		args   map[string]string
		access func(string, uint32) error
		want   []doctorCheck
		wantMM []string
	}{
		"minimega down": {
			want: []doctorCheck{row("", doctorSkip, "not checked, because minimega gave no version")},
		},
		"all good": {
			up: true,
			want: []doctorCheck{
				row(minimegaFilesRow, doctorPass, files+" (minimega's -filepath)"),
				row(minimegaBaseRow, doctorPass, filepath.Join(dir, "minimega")),
				row(minimegaMeshRow, doctorPass, "degree 0, abssnapshot true"),
			},
			wantMM: []string{"args"},
		},
		"mesh": {
			up: true, args: map[string]string{"degree": "2"},
			want: []doctorCheck{
				row(minimegaFilesRow, doctorPass, files+" (minimega's -filepath)"),
				row(minimegaBaseRow, doctorPass, filepath.Join(dir, "minimega")),
				row(minimegaMeshRow, doctorPass, "degree 2, abssnapshot true, mesh of 3 nodes"),
			},
			wantMM: []string{"args", "mesh status"},
		},
		"files elsewhere, other base": {
			up: true, args: map[string]string{"filepath": "/elsewhere/files", "base": "/var/minimega"},
			want: []doctorCheck{
				row(minimegaFilesRow, doctorFail,
					"minimega's -filepath /elsewhere/files does not exist here, so phenix cannot find the images minimega launches"),
				row(minimegaBaseRow, doctorWarn, "minimega runs with -base /var/minimega and base-dir.minimega is "+
					filepath.Join(dir, "minimega")+". The Hosts page measures disk usage under "+filepath.Join(dir, "minimega")),
				row(minimegaMeshRow, doctorPass, "degree 0, abssnapshot true"),
			},
			wantMM: []string{"args"},
		},
		"files read-only": {
			up: true, access: readOnly,
			want: []doctorCheck{
				row(minimegaFilesRow, doctorWarn, "minimega's -filepath "+files+
					" is not writable by this user, so phenix cannot upload, clone, rename or delete disk images in it"),
				row(minimegaBaseRow, doctorPass, filepath.Join(dir, "minimega")),
				row(minimegaMeshRow, doctorPass, "degree 0, abssnapshot true"),
			},
			wantMM: []string{"args"},
		},
		"relative files": {
			up: true, args: map[string]string{"filepath": "files"},
			want: []doctorCheck{
				row(minimegaFilesRow, doctorWarn,
					"minimega's -filepath files is relative, so phenix uses <base-dir.phenix>/images in its place"),
				row(minimegaBaseRow, doctorPass, filepath.Join(dir, "minimega")),
				row(minimegaMeshRow, doctorPass, "degree 0, abssnapshot true"),
			},
			wantMM: []string{"args"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			args := doctorMinimegaArgs(dir)
			maps.Copy(args, tc.args)

			mm := &doctorMinimega{mu: sync.Mutex{}, args: args, size: "3", commands: nil}
			in := fakeDoctorInput(dir, nil)
			in.minimega = mm.run
			in.found = &doctorFound{minimegaUp: tc.up, minimegaArgs: nil}

			if tc.access != nil {
				in.env.access = tc.access
			}

			got := minimegaEnvChecks(t.Context(), in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("minimegaEnvChecks() mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantMM, mm.commands); diff != "" {
				t.Errorf("minimega commands mismatch (-want +got):\n%s", diff)
			}

			if tc.up && in.found.minimegaArgs["filepath"] != args["filepath"] {
				t.Errorf("found args = %v, want minimega's", in.found.minimegaArgs)
			}
		})
	}
}

// minimega-env warns when minimega gives no flags, as one before 3.0 does,
// or does not answer in time.
func TestMinimegaEnvChecksNoArgs(t *testing.T) {
	t.Parallel()

	dir := newDoctorTestDir(t)
	hung := make(chan struct{})
	t.Cleanup(func() { close(hung) })

	tests := map[string]struct {
		run  minimegaRunner
		want string
	}{
		"old minimega": {
			run: func(string, string, time.Duration) ([]*minicli.Response, error) {
				return nil, errors.New("invalid command")
			},
			want: "minimega did not give its flags (args): invalid command",
		},
		"empty answer": {
			run:  func(string, string, time.Duration) ([]*minicli.Response, error) { return nil, nil },
			want: "minimega's answer to args holds no flags",
		},
		"hangs": {
			run:  func(string, string, time.Duration) ([]*minicli.Response, error) { <-hung; return nil, nil },
			want: "minimega did not give its flags (args) within 50ms",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(dir, nil)
			in.minimega, in.timeout = tc.run, 50*time.Millisecond
			in.found = &doctorFound{minimegaUp: true, minimegaArgs: nil}

			want := []doctorCheck{row("", doctorWarn, tc.want)}
			if diff := cmp.Diff(want, minimegaEnvChecks(t.Context(), in)); diff != "" {
				t.Errorf("minimegaEnvChecks() mismatch (-want +got):\n%s", diff)
			}

			if got := minimegaEnvHint(row(name, doctorWarn, tc.want)); !strings.HasPrefix(got, "run minimega 3.0") {
				t.Errorf("minimegaEnvHint() = %q, want the args hint", got)
			}
		})
	}
}

// Any flags minimega's args gives come back by name.
func TestMinimegaArgsProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		want := rapid.MapOf(rapid.StringMatching(`[a-z]{1,10}`), rapid.String()).Draw(t, "args")

		header, values := make([]string, 0, len(want)), make([]string, 0, len(want))
		for name, value := range want {
			header, values = append(header, name), append(values, value)
		}

		resps := []*minicli.Response{{Host: "mm1", Header: header, Tabular: [][]string{values}}}

		got, ok := minimegaArgs(resps)
		if len(want) == 0 {
			if ok {
				t.Fatalf("minimegaArgs() of no flags = %v, true", got)
			}

			return
		}

		if diff := cmp.Diff(want, got); !ok || diff != "" {
			t.Fatalf("minimegaArgs() mismatch (-want +got):\n%s", diff)
		}
	})
}

// runMinimegaAt sends the command as it is, outside any namespace, and
// returns minimega's responses.
func TestRunMinimegaAt(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		commands []string
	)

	base := fakeMinimega(t, func(req miniclient.Request) []miniclient.Response {
		mu.Lock()
		commands = append(commands, req.Command)
		mu.Unlock()

		return []miniclient.Response{{Resp: minicli.Responses{{
			Host: "mm1", Header: []string{"base", "filepath"}, Tabular: [][]string{{"/tmp/minimega", "/phenix/images"}},
		}}}}
	})

	resps, err := runMinimegaAt(base, "args", time.Second)
	if err != nil {
		t.Fatalf("runMinimegaAt(args) error = %v", err)
	}

	args, _ := minimegaArgs(resps)
	if diff := cmp.Diff(map[string]string{"base": "/tmp/minimega", "filepath": "/phenix/images"}, args); diff != "" {
		t.Errorf("args mismatch (-want +got):\n%s", diff)
	}

	mu.Lock()
	defer mu.Unlock()

	// mmcli keeps its commands out of minimega's history, and names no namespace
	if diff := cmp.Diff([]string{".record false args"}, commands); diff != "" {
		t.Errorf("commands sent mismatch (-want +got):\n%s", diff)
	}
}

// boundedCall gives the call's answer, or errNoAnswer once the time bound
// passes or the context ends.
func TestBoundedCall(t *testing.T) {
	t.Parallel()

	hung := make(chan struct{})
	t.Cleanup(func() { close(hung) })

	answer := []*minicli.Response{{Host: "mm1", Response: "7"}}
	answers := func() ([]*minicli.Response, error) { return answer, nil }
	hangs := func() ([]*minicli.Response, error) { <-hung; return nil, nil }

	if got, err := boundedCall(t.Context(), time.Second, answers); err != nil || len(got) != 1 || got[0] != answer[0] {
		t.Errorf("boundedCall(answers) = %v, %v, want the answer", got, err)
	}

	if _, err := boundedCall(t.Context(), 10*time.Millisecond, hangs); !errors.Is(err, errNoAnswer) {
		t.Errorf("boundedCall(hangs) error = %v, want errNoAnswer", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := boundedCall(ctx, time.Minute, hangs); !errors.Is(err, errNoAnswer) {
		t.Errorf("boundedCall(canceled) error = %v, want errNoAnswer", err)
	}
}

// A failed request names its cause, with or without the [url.Error] the client
// wraps it in, and a file that is not a socket comes before the error.
func TestDoctorServerDialCheck(t *testing.T) {
	t.Parallel()

	const socket = "/run/phenix.sock"

	dial := func(errno syscall.Errno) error {
		return &url.Error{Op: "Get", URL: "http://unix/api/v1/options", Err: &net.OpError{
			Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", errno),
		}}
	}

	tests := map[string]struct {
		err   error
		lstat error       // of the socket file; nil is a file of mode
		mode  os.FileMode // of the socket file
		want  doctorCheck
	}{
		"not running": {
			err: dial(syscall.ENOENT), lstat: os.ErrNotExist,
			want: warnCheck("no phenix ui on /run/phenix.sock: phenix ui is not running, or serves another unix socket"),
		},
		"permission": {
			err: dial(syscall.EACCES), mode: os.ModeSocket,
			want: failCheck("this user cannot connect to /run/phenix.sock: dial unix: connect: permission denied"),
		},
		"stale": {
			err: dial(syscall.ECONNREFUSED), mode: os.ModeSocket,
			want: warnCheck("/run/phenix.sock is a stale socket file: no phenix ui listens on it"),
		},
		"not a socket": {
			err: dial(syscall.EACCES), mode: 0o600,
			want: warnCheck("/run/phenix.sock is not a socket, so no phenix ui listens on it"),
		},
		"lstat fails": {
			err: dial(syscall.ECONNREFUSED), lstat: fs.ErrPermission,
			want: warnCheck("/run/phenix.sock is a stale socket file: no phenix ui listens on it"),
		},
		"deadline": {
			err: context.DeadlineExceeded, mode: os.ModeSocket,
			want: warnCheck("phenix ui on /run/phenix.sock did not answer within 2s"),
		},
		"network timeout": {
			err: &url.Error{Op: "Get", URL: "http://unix/", Err: os.ErrDeadlineExceeded}, mode: os.ModeSocket,
			want: warnCheck("phenix ui on /run/phenix.sock did not answer within 2s"),
		},
		"other": {
			err: &url.Error{Op: "Get", URL: "http://unix/", Err: errors.New("malformed HTTP response")}, mode: os.ModeSocket,
			want: warnCheck("asking phenix ui on /run/phenix.sock: malformed HTTP response"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lstat := func(path string) (os.FileInfo, error) {
				if path != socket {
					return nil, fmt.Errorf("lstat(%s)", path)
				}

				if tc.lstat != nil {
					return nil, tc.lstat
				}

				return doctorEnvMode{FileInfo: nil, mode: tc.mode}, nil
			}

			got := serverDialCheck(socket, 2*time.Second, lstat, tc.err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("serverDialCheck(%v) mismatch (-want +got):\n%s", tc.err, diff)
			}

			if hint := serverHint(got); hint == "" {
				t.Errorf("serverHint(%+v) = \"\", want a hint", got)
			}
		})
	}
}

// The doctor's client has its time bound, and dials the socket without
// creating it.
func TestDoctorServerClient(t *testing.T) {
	t.Parallel()

	socket := filepath.Join(t.TempDir(), "p.sock")

	client := doctorServerClient(socket, 2*time.Second)
	if client.Timeout != 2*time.Second {
		t.Errorf("doctorServerClient() timeout = %s, want 2s", client.Timeout)
	}

	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://unix/api/v1/options", nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}

	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()

		t.Fatalf("doctorServerClient() request to a missing socket succeeded, want an error")
	}

	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("doctorServerClient() request error = %v, want one for a missing socket", err)
	}

	if _, err := os.Lstat(socket); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("os.Lstat(%s) after the request error = %v, want the socket not created", socket, err)
	}
}

// minimega's files directory must be an absolute directory here that this
// user can read and search; one it cannot write only warns.
func TestDoctorServerMinimegaFilesCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeTestFile(t, dir, "file", "")
	missing := filepath.Join(dir, "missing")

	denied := func(mode uint32) func(string, uint32) error {
		return func(_ string, m uint32) error {
			if m&mode != 0 {
				return unix.EACCES
			}

			return nil
		}
	}

	tests := map[string]struct {
		dir    string
		stat   func(string) (os.FileInfo, error)
		access func(string, uint32) error
		want   doctorCheck
	}{
		"good": {dir: dir, want: passCheck(dir + " (minimega's -filepath)")},
		"none": {want: warnCheck("minimega gives no -filepath, so phenix uses <base-dir.phenix>/images in its place")},
		"relative": {
			dir:  "files",
			want: warnCheck("minimega's -filepath files is relative, so phenix uses <base-dir.phenix>/images in its place"),
		},
		"missing": {
			dir: missing,
			want: failCheck("minimega's -filepath " + missing +
				" does not exist here, so phenix cannot find the images minimega launches"),
		},
		"stat fails": {
			dir: dir,
			stat: func(path string) (os.FileInfo, error) {
				return nil, &fs.PathError{Op: "stat", Path: path, Err: syscall.EACCES}
			},
			want: failCheck("stat " + dir + ": permission denied"),
		},
		"a file": {dir: file, want: failCheck("minimega's -filepath " + file + " is not a directory")},
		"not searchable": {
			dir: dir, access: denied(unix.X_OK),
			want: failCheck("minimega's -filepath " + dir +
				" is not readable and searchable by this user, so phenix cannot read its images"),
		},
		"not writable": {
			dir: dir, access: denied(unix.W_OK),
			want: warnCheck("minimega's -filepath " + dir +
				" is not writable by this user, so phenix cannot upload, clone, rename or delete disk images in it"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := fakeDoctorEnv(nil, false)
			env.access = func(string, uint32) error { return nil }

			if tc.stat != nil {
				env.stat = tc.stat
			}

			if tc.access != nil {
				env.access = tc.access
			}

			got := env.minimegaFilesCheck(tc.dir)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("minimegaFilesCheck(%q) mismatch (-want +got):\n%s", tc.dir, diff)
			}
		})
	}
}

// minimega's -base passes when it is base-dir.minimega, cleaned, and warns
// when it differs or minimega gives none.
func TestDoctorServerMinimegaBaseCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		setting, base string
		want          doctorCheck
	}{
		"same":                {setting: "/tmp/minimega", base: "/tmp/minimega", want: passCheck("/tmp/minimega")},
		"same, cleaned":       {setting: "/tmp/minimega/", base: "/tmp//minimega", want: passCheck("/tmp//minimega")},
		"no -base":            {setting: "/tmp/minimega", want: warnCheck("minimega gives no -base")},
		"no -base or setting": {want: warnCheck("minimega gives no -base")},
		"other": {
			setting: "/tmp/minimega", base: "/var/minimega",
			want: warnCheck("minimega runs with -base /var/minimega and base-dir.minimega is /tmp/minimega. " +
				"The Hosts page measures disk usage under /tmp/minimega"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tc.want.Name = minimegaBaseRow
			if diff := cmp.Diff(tc.want, minimegaBaseCheck(tc.setting, tc.base)); diff != "" {
				t.Errorf("minimegaBaseCheck(%q, %q) mismatch (-want +got):\n%s", tc.setting, tc.base, diff)
			}
		})
	}
}

// The mesh row asks for mesh status only when minimega meshes with other
// nodes, and warns when it fails or gives no size.
func TestDoctorServerMinimegaMeshCheck(t *testing.T) {
	t.Parallel()

	sized := []*minicli.Response{{Host: "mm1", Header: []string{"size", "degree"}, Tabular: [][]string{{"4", "2"}}}}

	tests := map[string]struct {
		degree  string
		resps   []*minicli.Response
		err     error
		want    doctorCheck
		wantRun bool
	}{
		"alone":      {degree: "0", want: passCheck("degree 0, abssnapshot true")},
		"no degree":  {want: passCheck("degree , abssnapshot true")},
		"bad degree": {degree: "two", want: passCheck("degree two, abssnapshot true")},
		"mesh": {
			degree: "2", resps: sized, want: passCheck("degree 2, abssnapshot true, mesh of 4 nodes"), wantRun: true,
		},
		"mesh status fails": {
			degree: "2", err: errors.New("invalid command"), wantRun: true,
			want: warnCheck("degree 2, abssnapshot true, but mesh status failed: invalid command"),
		},
		"no size": {
			degree: "3", resps: []*minicli.Response{{Host: "mm1", Response: "ok"}}, wantRun: true,
			want: warnCheck("degree 3, abssnapshot true, but mesh status gives no size"),
		},
		"size and an error": {
			degree: "2", resps: sized, err: errors.New("partial"), wantRun: true,
			want: warnCheck("degree 2, abssnapshot true, but mesh status failed: partial"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var commands []string

			run := func(command string) ([]*minicli.Response, error) {
				commands = append(commands, command)

				return tc.resps, tc.err
			}

			tc.want.Name = minimegaMeshRow
			args := map[string]string{"degree": tc.degree, "abssnapshot": "true"}

			if diff := cmp.Diff(tc.want, minimegaMeshCheck(args, run)); diff != "" {
				t.Errorf("minimegaMeshCheck(%v) mismatch (-want +got):\n%s", args, diff)
			}

			var want []string
			if tc.wantRun {
				want = []string{"mesh status"}
			}

			if diff := cmp.Diff(want, commands); diff != "" {
				t.Errorf("minimega commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Each minimega-env row that warns or fails has its own hint.
func TestDoctorServerMinimegaEnvHint(t *testing.T) {
	t.Parallel()

	const mountHint = "mount minimega's files directory at the same path for phenix (Docker Compose shares /phenix), " +
		"readable by this user"

	tests := map[string]struct {
		check doctorCheck
		want  string
	}{
		"skip": {check: row("", doctorSkip, "not checked"), want: ""},
		"base": {
			check: row(minimegaBaseRow, doctorWarn, "minimega gives no -base"),
			want:  "set base-dir.minimega to minimega's -base",
		},
		"mesh": {
			check: row(minimegaMeshRow, doctorWarn, "mesh status failed"),
			want:  "check minimega's mesh with minimega -e mesh status",
		},
		"not writable": {
			check: row(minimegaFilesRow, doctorWarn, "x is not writable by this user"),
			want:  "give this user write access to minimega's files directory",
		},
		"relative": {
			check: row(minimegaFilesRow, doctorWarn, "x is relative, so"),
			want:  "start minimega with an absolute -filepath",
		},
		"no filepath": {
			check: row(minimegaFilesRow, doctorWarn, "minimega gives no -filepath, so"),
			want:  "start minimega with an absolute -filepath",
		},
		"missing":   {check: row(minimegaFilesRow, doctorFail, "x does not exist here"), want: mountHint},
		"not a dir": {check: row(minimegaFilesRow, doctorFail, "x is not a directory"), want: mountHint},
		"no flags": {
			check: row("", doctorWarn, "minimega's answer to args holds no flags"),
			want:  "run minimega 3.0 or later, whose args command gives its flags",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := minimegaEnvHint(tc.check); got != tc.want {
				t.Errorf("minimegaEnvHint(%+v) = %q, want %q", tc.check, got, tc.want)
			}
		})
	}
}
