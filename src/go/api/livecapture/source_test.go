package livecapture

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
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

	// the command minimega runs: the script is one argument and the path,
	// offset and count are arguments after it, never part of the script
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

	want := []string{"sh", "-c", meshReadScript, "phenix", "/phenix/images/exp/files/vm-0.pcap", "24", "1024"}
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
	} {
		if _, err := newMeshSource("compute1", path); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("newMeshSource(compute1, %q) error = %v, want %v", path, err, ErrUnsafePath)
		}
	}

	if _, err := newMeshSource("compute1 shell reboot", "/a.pcap"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("newMeshSource(%q, /a.pcap) error = %v, want %v", "compute1 shell reboot", err, ErrUnsafePath)
	}
}

// TestMeshReadScript runs the script a remote node runs, on this machine.
func TestMeshReadScript(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"sh", "dd", "base64"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}

	// Remote nodes run Linux; BSD dd, as on macOS, has no skip_bytes or count_bytes.
	if exec.CommandContext(t.Context(), "dd", "if=/dev/null", "iflag=skip_bytes,count_bytes", "status=none").Run() != nil {
		t.Skip("dd does not support iflag=skip_bytes,count_bytes")
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
			t.Context(), "sh", "-c", meshReadScript, "phenix", path, strconv.Itoa(tt.off), strconv.Itoa(tt.n),
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
