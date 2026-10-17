package livecapture

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmcli"
	"phenix/util/mm/mmtest"
)

func TestFileSourceRead(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "a.pcap")

	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}

	src := &fileSource{path: path, f: nil, buf: nil}
	defer src.Close()

	got, err := src.Read(2, 4)
	if err != nil || string(got) != "2345" {
		t.Fatalf("Read(2, 4) = %q, %v; want 2345", got, err)
	}

	got, err = src.Read(8, 4)
	if err != nil || string(got) != "89" {
		t.Fatalf("Read(8, 4) = %q, %v; want 89", got, err)
	}

	got, err = src.Read(10, 4)
	if err != nil || len(got) != 0 {
		t.Fatalf("Read(10, 4) = %q, %v; want nothing", got, err)
	}

	if err := os.Truncate(path, 4); err != nil {
		t.Fatal(err)
	}

	if _, err := src.Read(10, 4); !errors.Is(err, errTruncated) {
		t.Fatalf("Read of a shrunk file = %v, want errTruncated", err)
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
		t.Fatal(err)
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
		if got.err != nil || !bytes.Equal(got.data, []byte{0, 1, 2, 0xff}) {
			t.Fatalf("Read = %v, %v; want the decoded bytes", got.data, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the mesh read waited for another minimega command")
	}

	release()

	if err := <-held; err != nil {
		t.Fatalf("holding command failed: %v", err)
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
		t.Fatalf("unexpected command %q", sent)
	}

	minicli.Reset()
	t.Cleanup(minicli.Reset)

	minicli.MustRegister(&minicli.Handler{
		Patterns: []string{"mesh send <host> shell <args>..."},
		Call:     func(*minicli.Command, chan<- minicli.Responses) {},
	})

	cmd, err := minicli.Compile(sent)
	if err != nil {
		t.Fatalf("minimega cannot parse %q: %v", sent, err)
	}

	args := cmd.ListArgs["args"]
	want := []string{"sh", "-c", meshReadScript, "phenix", "/phenix/images/exp/files/vm-0.pcap", "24", "1024"}

	if cmd.StringArgs["host"] != "compute1" || !slices.Equal(args, want) {
		t.Fatalf("minimega would run %q on %q, want %q", args, cmd.StringArgs["host"], want)
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
			t.Errorf("newMeshSource(%q) = %v, want ErrUnsafePath", path, err)
		}
	}

	if _, err := newMeshSource("compute1 shell reboot", "/a.pcap"); !errors.Is(err, ErrUnsafePath) {
		t.Errorf("unsafe host accepted: %v", err)
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
		t.Fatal(err)
	}

	for _, tt := range []struct{ off, n int }{{0, 24}, {24, 1000}, {2500, 1000}, {3000, 10}} {
		out, err := exec.CommandContext(
			t.Context(), "sh", "-c", meshReadScript, "phenix", path, strconv.Itoa(tt.off), strconv.Itoa(tt.n),
		).Output()
		if err != nil {
			t.Fatalf("script failed: %v", err)
		}

		got, err := decodeRange(string(out))
		if err != nil {
			t.Fatal(err)
		}

		want := data[min(tt.off, len(data)):min(tt.off+tt.n, len(data))]
		if !bytes.Equal(got, want) {
			t.Fatalf("off %d n %d: got %d bytes, want %d", tt.off, tt.n, len(got), len(want))
		}
	}
}
