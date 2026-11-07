package livecapture

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
	"phenix/util/testenv"
)

func TestFileSourceRead(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a.pcap")

	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("writing the capture file: %v", err)
	}

	src := &fileSource{path: path, f: nil, buf: nil}
	defer src.Close()

	for _, tt := range []struct {
		off, n int
		want   string
	}{{2, 4, "2345"}, {8, 4, "89"}, {10, 4, ""}} {
		if got, err := src.Read(int64(tt.off), tt.n); err != nil || string(got) != tt.want {
			t.Errorf("Read(%d, %d) = %q, %v; want %q, nil", tt.off, tt.n, got, err, tt.want)
		}
	}

	if err := os.Truncate(path, 4); err != nil {
		t.Fatalf("truncating the capture file: %v", err)
	}

	if _, err := src.Read(10, 4); !errors.Is(err, errTruncated) {
		t.Errorf("Read(10, 4) of a file shrunk to 4 bytes error = %v, want %v", err, errTruncated)
	}
}

// A mesh read runs on a minimega connection of its own, so it is not held up
// by, and does not hold up, other minimega commands.
func TestMeshSourceRead(t *testing.T) { //nolint:paralleltest // uses the fake minimega
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "hold" {
			<-hold

			return nil
		}

		return []*minicli.Response{mmtest.Text("compute1", base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 0xff})+"\n")}
	})

	t.Cleanup(release)

	held := make(chan error, 1)

	go func() {
		cmd := mmcli.NewCommand()
		cmd.Command = "hold"

		held <- mmcli.ErrorResponse(mmcli.Run(cmd))
	}()

	mmtest.Await(t, "hold")

	src, err := newMeshSource("compute1", "/phenix/images/exp/files/vm-0.pcap")
	if err != nil {
		t.Fatalf("newMeshSource(compute1, /phenix/images/exp/files/vm-0.pcap) error = %v", err)
	}

	type result struct {
		data []byte
		err  error
	}

	read := make(chan result, 1)

	go func() {
		data, err := src.Read(24, 1024)
		read <- result{data, err}
	}()

	select {
	case got := <-read:
		if got.err != nil {
			t.Errorf("Read(24, 1024) error = %v", got.err)
		}

		if diff := cmp.Diff([]byte{0, 1, 2, 0xff}, got.data); diff != "" {
			t.Errorf("Read(24, 1024) mismatch (-want +got):\n%s", diff)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read(24, 1024) still waiting after 5s, want it not held up by another minimega command")
	}

	release()

	if err := <-held; err != nil {
		t.Errorf("holding minimega command error = %v", err)
	}

	// the command minimega runs: the script is one argument with no "$" for minimega to expand
	var sent string

	for _, cmd := range received() {
		if strings.HasPrefix(cmd.Base, "mesh send ") {
			sent = cmd.Base
		}
	}

	if !strings.HasPrefix(sent, "mesh send compute1 shell sh -c ") {
		t.Fatalf("Read(24, 1024) sent minimega %q, want a command starting %q", sent, "mesh send compute1 shell sh -c ")
	}

	minicli.Reset()
	t.Cleanup(minicli.Reset)

	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"mesh send <host> shell <args>..."},
		Call:     func(*minicli.Command, chan<- minicli.Responses) {},
	})

	cmd, err := minicli.Compile(sent)
	if err != nil {
		t.Fatalf("minicli.Compile(%q) error = %v", sent, err)
	}

	if host := cmd.StringArgs["host"]; host != "compute1" {
		t.Errorf("minicli.Compile(%q) host = %q, want %q", sent, host, "compute1")
	}

	want := []string{
		"sh", "-c",
		"dd if=/phenix/images/exp/files/vm-0.pcap iflag=skip_bytes,count_bytes skip=24 count=1024 bs=1048576 status=none" +
			" | base64 -w 0",
	}
	if diff := cmp.Diff(want, cmd.ListArgs["args"]); diff != "" {
		t.Errorf("minicli.Compile(%q) args mismatch (-want +got):\n%s", sent, diff)
	}
}

func TestMeshSourceRefusesUnsafePaths(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"relative.pcap",
		"/phenix/images/exp/files/../../../etc/shadow",
		"/phenix/images/exp/files/a b.pcap",
		`/phenix/images/exp/files/a"b.pcap`,
		"/phenix/images/exp/files/a#b.pcap",
		"/phenix/images/exp/files/$(reboot).pcap",
		"",
		"/",
		"/phenix//files/a.pcap",
		"/phenix/files/",
		"/phenix/files/./a.pcap",
		"/phenix/files/a.pcap\n",
	} {
		if _, err := newMeshSource("compute1", path); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("newMeshSource(compute1, %q) error = %v, want %v", path, err, ErrUnsafePath)
		}
	}

	for _, host := range []string{"compute1 shell reboot", "", "compute1;reboot", "compute1\n", "/", "a/b", "/compute1"} {
		if _, err := newMeshSource(host, "/a.pcap"); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("newMeshSource(%q, /a.pcap) error = %v, want %v", host, err, ErrUnsafePath)
		}
	}
}

// TestMeshReadScript runs the script a remote node runs, on this machine.
func TestMeshReadScript(t *testing.T) {
	t.Parallel()

	testenv.RequireCommands(t, "sh", "dd", "base64")

	// Remote nodes run Linux; BSD dd, as on macOS, has no skip_bytes or count_bytes.
	if exec.CommandContext(t.Context(), "dd", "if=/dev/null", "iflag=skip_bytes,count_bytes", "status=none").Run() != nil {
		testenv.Skipf(t, "dd does not support iflag=skip_bytes,count_bytes")
	}

	path := filepath.Join(t.TempDir(), "vm-0.pcap")

	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte(i * 7)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing the capture file: %v", err)
	}

	for _, tt := range []struct{ off, n int }{{0, 24}, {24, 1000}, {2500, 1000}, {3000, 10}} {
		out, err := exec.CommandContext(
			t.Context(), "sh", "-c", meshReadScript(path, int64(tt.off), tt.n),
		).Output()
		if err != nil {
			t.Errorf("meshReadScript(offset %d, count %d) error = %v", tt.off, tt.n, err)

			continue
		}

		got, err := decodeRange(string(out))
		if err != nil {
			t.Errorf("decodeRange(meshReadScript(offset %d, count %d)) error = %v", tt.off, tt.n, err)

			continue
		}

		from, to := min(tt.off, len(data)), min(tt.off+tt.n, len(data))
		if diff := cmp.Diff(data[from:to], got, cmpopts.EquateEmpty()); diff != "" {
			t.Errorf("meshReadScript(offset %d, count %d) mismatch with file bytes %d to %d (-want +got):\n%s",
				tt.off, tt.n, from, to, diff)
		}
	}
}

// genSourceName draws a name of the characters safePath allows, sometimes empty or a dot segment.
func genSourceName() *rapid.Generator[string] {
	chars := rapid.SampledFrom([]rune("aZ09._+=@,:-"))

	return rapid.OneOf(rapid.StringOfN(chars, 1, 6, -1), rapid.SampledFrom([]string{"", ".", ".."}))
}

// Every host and path newMeshSource accepts reaches the node's shell whole,
// after the script, as minimega's parser splits the mesh command.
func TestMeshSourceArgsProperties(t *testing.T) { //nolint:paralleltest // uses the fake minimega and minicli's registry
	received := mmtest.Use(t, func(mmtest.Command) []*minicli.Response {
		return []*minicli.Response{mmtest.Text("node", "")}
	})

	minicli.Reset()
	t.Cleanup(minicli.Reset)

	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"mesh send <host> shell <args>..."},
		Call:     func(*minicli.Command, chan<- minicli.Responses) {},
	})

	rapid.Check(t, func(t *rapid.T) {
		host := genSourceName().Draw(t, "host")
		capturePath := "/" + strings.Join(rapid.SliceOfN(genSourceName(), 1, 4).Draw(t, "segments"), "/")

		src, err := newMeshSource(host, capturePath)
		if err != nil {
			t.Skip("refused")
		}

		off, n := rapid.Int64Range(0, 1<<40).Draw(t, "off"), rapid.IntRange(1, 1<<20).Draw(t, "n")
		if _, err := src.Read(off, n); err != nil {
			t.Fatalf("Read(%d, %d) of %s on %s error = %v", off, n, capturePath, host, err)
		}

		bases := mmtest.Bases(received())
		sent := bases[len(bases)-1]

		cmd, err := minicli.Compile(sent)
		if err != nil {
			t.Fatalf("minicli.Compile(%q) error = %v", sent, err)
		}

		script := meshReadScript(capturePath, off, n)
		if os.ExpandEnv(script) != script {
			t.Fatalf("meshReadScript(%q, %d, %d) = %q, which minimega's $ expansion changes", capturePath, off, n, script)
		}

		want := []string{"sh", "-c", script}
		if got := cmd.StringArgs["host"]; got != host {
			t.Fatalf("minicli.Compile(%q) host = %q, want %q", sent, got, host)
		}

		if diff := cmp.Diff(want, cmd.ListArgs["args"]); diff != "" {
			t.Fatalf("minicli.Compile(%q) args mismatch (-want +got):\n%s", sent, diff)
		}
	})
}

func TestFileSourceReadErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pcap"), []byte("0123456789"), 0o600); err != nil {
		t.Fatalf("writing the capture file: %v", err)
	}

	closed, err := os.Open(filepath.Join(dir, "a.pcap"))
	if err != nil {
		t.Fatalf("opening the capture file: %v", err)
	}

	_ = closed.Close()

	tests := []struct {
		name     string
		src      *fileSource
		wantErr  error
		wantText string
	}{
		{
			name:     "missing file",
			src:      &fileSource{path: filepath.Join(dir, "missing.pcap")},
			wantErr:  fs.ErrNotExist,
			wantText: "opening capture file",
		},
		{
			name:     "closed file",
			src:      &fileSource{path: filepath.Join(dir, "a.pcap"), f: closed},
			wantErr:  os.ErrClosed,
			wantText: "getting capture file size",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := test.src.Read(0, 4)
			if got != nil || !errors.Is(err, test.wantErr) || !strings.Contains(fmt.Sprint(err), test.wantText) {
				t.Errorf("Read(0, 4) = %q, %v; want nil, %v containing %q", got, err, test.wantErr, test.wantText)
			}
		})
	}
}

// A directory opens and has a size, but reading it fails.
func TestFileSourceReadDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pcap"), nil, 0o600); err != nil {
		t.Fatalf("writing a file into the directory: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("os.Stat(%s) error = %v", dir, err)
	}

	src := &fileSource{path: dir}
	defer src.Close()

	got, err := src.Read(0, 4)

	// a filesystem that gives directories no size has nothing to read
	if info.Size() == 0 {
		if got != nil || err != nil {
			t.Errorf("Read(0, 4) of an empty-sized directory = %q, %v; want nil, nil", got, err)
		}

		return
	}

	if got != nil || err == nil || !strings.Contains(err.Error(), "reading capture file") {
		t.Errorf("Read(0, 4) of a directory = %q, %v; want nil and a read error", got, err)
	}
}

func TestFileSourceClose(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a.pcap")
	if err := os.WriteFile(path, []byte("0123"), 0o600); err != nil {
		t.Fatalf("writing the capture file: %v", err)
	}

	src := &fileSource{path: path}

	if err := src.Close(); err != nil {
		t.Errorf("Close() before any Read() error = %v, want nil", err)
	}

	if got, err := src.Read(0, 4); string(got) != "0123" || err != nil {
		t.Fatalf("Read(0, 4) = %q, %v; want %q, nil", got, err, "0123")
	}

	if err := src.Close(); err != nil {
		t.Errorf("Close() after Read() error = %v, want nil", err)
	}

	if _, err := src.Read(0, 4); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Read(0, 4) after Close() error = %v, want %v", err, os.ErrClosed)
	}
}

func TestMeshSourceReadErrors(t *testing.T) { //nolint:paralleltest // uses the fake minimega
	tests := []struct {
		name     string
		resp     *minicli.Response
		wantText string
	}{
		{name: "node error", resp: mmtest.Error("compute1", "no such file"), wantText: "reading /a.pcap on compute1: no such file"},
		{name: "not base64", resp: mmtest.Text("compute1", "!!not base64"), wantText: "decoding /a.pcap from compute1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mmtest.Use(t, func(mmtest.Command) []*minicli.Response { return []*minicli.Response{test.resp} })

			src, err := newMeshSource("compute1", "/a.pcap")
			if err != nil {
				t.Fatalf("newMeshSource(compute1, /a.pcap) error = %v", err)
			}

			got, err := src.Read(0, 4)
			if got != nil || err == nil || !strings.Contains(err.Error(), test.wantText) {
				t.Errorf("Read(0, 4) = %q, %v; want nil and an error containing %q", got, err, test.wantText)
			}

			if err := src.Close(); err != nil {
				t.Errorf("Close() error = %v, want nil", err)
			}
		})
	}
}

func TestDecodeRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		out     string
		want    []byte
		wantErr bool
	}{
		{name: "bytes", out: "AAEC/w==", want: []byte{0, 1, 2, 0xff}},
		{name: "trailing newline", out: "AAEC/w==\n", want: []byte{0, 1, 2, 0xff}},
		{name: "surrounding space", out: " \tAAEC\r\n", want: []byte{0, 1, 2}},
		{name: "nothing", out: "", want: nil},
		{name: "only space", out: "\n", want: nil},
		{name: "not base64", out: "!!", wantErr: true},
		{name: "missing padding", out: "AAEC/w", wantErr: true},
		{name: "space inside", out: "AAEC /w==", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodeRange(test.out)
			if (err != nil) != test.wantErr {
				t.Fatalf("decodeRange(%q) error = %v, want error %t", test.out, err, test.wantErr)
			}

			// what decodes before an error is not used
			if err != nil {
				return
			}

			if diff := cmp.Diff(test.want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("decodeRange(%q) mismatch (-want +got):\n%s", test.out, diff)
			}
		})
	}
}
