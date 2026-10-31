package cmd

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
	"github.com/google/go-cmp/cmp"
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

	tests := map[string]struct {
		socket         string
		client         func(string, time.Duration) *http.Client
		mode           os.FileMode // of the file at socket; 0 is none
		bridge, deploy string
		want           []doctorCheck
	}{
		"no socket": {
			want: []doctorCheck{row("", doctorPass, "unix-socket is empty, so phenix commands ask no phenix ui for its modes")},
		},
		"answers": {
			socket: socket, client: doctorServerAnswer(http.StatusOK, version.API(), doctorTestOptions), bridge: "manual",
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
			socket: socket, client: doctorServerAnswer(http.StatusForbidden, version.API(), `{}`),
			want: []doctorCheck{row("", doctorWarn,
				"phenix ui on /run/phenix.sock answered GET /api/v1/options with Forbidden")},
		},
		"bad options": {
			socket: socket, client: doctorServerAnswer(http.StatusOK, version.API(), `[`),
			want: []doctorCheck{row("", doctorWarn,
				"phenix ui on /run/phenix.sock answered with options that do not parse: unexpected end of JSON input")},
		},
		"other version and modes": {
			socket: socket, bridge: "manual", deploy: "all",
			client: doctorServerAnswer(http.StatusOK, "9.9.9", `{"bridge-mode":"auto","deploy-mode":"no-headnode"}`),
			want: []doctorCheck{
				row("", doctorWarn, "phenix ui on /run/phenix.sock is 9.9.9, and this phenix is "+version.API()),
				row("server bridge-mode", doctorWarn, "phenix commands use auto, from phenix ui, in place of the manual set here"),
				row("server deploy-mode", doctorWarn, "phenix commands use all, set here, in place of phenix ui's no-headnode"),
			},
		},
		"auto in phenix ui, bridge-mode not set here": {
			socket: socket, client: doctorServerAnswer(http.StatusOK, version.API(), `{"bridge-mode":"auto","deploy-mode":"all"}`),
			want: []doctorCheck{
				row("", doctorPass, "phenix ui "+version.API()+" answers on /run/phenix.sock"),
				row("server bridge-mode", doctorPass, "phenix commands use auto, from phenix ui"),
				row("server deploy-mode", doctorPass, "phenix commands use all, from phenix ui"),
			},
		},
		"no version, auto set here, bad deploy mode": {
			socket: socket, bridge: "auto", deploy: "some",
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

			got := serverChecks(t.Context(), tc.socket, 50*time.Millisecond, tc.client, lstat, tc.bridge, tc.deploy)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("serverChecks() mismatch (-want +got):\n%s", diff)
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
