package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/activeshadow/libminimega/miniclient"
	"github.com/creack/pty"

	"phenix/util/common"
	"phenix/util/mm"
)

const (
	// captureStreamDirEnv tells a copy of the test binary to be the `phenix
	// vm capture stream` process, reaching minimega through the directory it
	// names.
	captureStreamDirEnv = "PHENIX_TEST_CAPTURE_STREAM_DIR"

	// captureStreamIgnoreEnv names, by number, a signal that process starts
	// out ignoring.
	captureStreamIgnoreEnv = "PHENIX_TEST_CAPTURE_STREAM_IGNORE"

	// captureStreamArgsEnv holds that process's arguments, one per line.
	captureStreamArgsEnv = "PHENIX_TEST_CAPTURE_STREAM_ARGS"
)

// Streaming a capture from another node spools it in the temporary directory.
// However the command is stopped, the spool is gone once it exits; a signal
// it was started ignoring does not stop it.
func TestVMCaptureStreamRemovesItsSpool(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		ignoring os.Signal // sent while it streams, which it keeps doing
		stop     os.Signal // nil: its reader closes the pipe
	}{
		{name: "interrupted", stop: os.Interrupt},
		{name: "terminated", stop: syscall.SIGTERM},
		{name: "hung up", stop: syscall.SIGHUP},
		{name: "hung up under nohup, then interrupted", ignoring: syscall.SIGHUP, stop: os.Interrupt},
		{name: "interrupted in the background, then terminated", ignoring: os.Interrupt, stop: syscall.SIGTERM},
		{name: "reader closed the pipe"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sofar := append(pcapHeader(), pcapRecord(60)...)
			node := &fakeNode{captured: bytes.Clone(sofar)}
			stream := startCaptureStream(t, node, captureStreamOptions{ignoring: tt.ignoring})

			stream.expect(t, "the capture so far", sofar)

			if n := countSpools(t, stream.dir); n != 1 {
				stream.failf(t, "found %d spools while streaming, want 1", n)
			}

			if tt.ignoring != nil {
				stream.signal(t, tt.ignoring)

				packet := pcapRecord(70)
				node.capture(packet)
				stream.expect(t, "a packet captured after the signal", packet)
			}

			if tt.stop != nil {
				stream.signal(t, tt.stop)
			} else {
				// the next packet is written to a pipe nobody reads
				stream.out.Close()
				node.capture(pcapRecord(80))
			}

			if err := stream.Wait(); err != nil {
				t.Fatalf("stream exited with %v, want success\n%s", err, stream.stderr)
			}

			if n := countSpools(t, stream.dir); n != 0 {
				t.Fatalf("%d spools left behind", n)
			}
		})
	}
}

// A read of a node that does not answer holds up following the capture. An
// interrupt still removes the spool at once, and a second one ends the
// process at once.
func TestVMCaptureStreamInterruptedTwice(t *testing.T) {
	t.Parallel()

	node := &fakeNode{silent: true}
	stream := startCaptureStream(t, node, captureStreamOptions{})

	select {
	case <-node.asked:
	case <-time.After(10 * time.Second):
		stream.failf(t, "it never read the capture")
	}

	if n := countSpools(t, stream.dir); n != 1 {
		stream.failf(t, "found %d spools while following, want 1", n)
	}

	stream.signal(t, os.Interrupt)

	deadline := time.Now().Add(10 * time.Second)

	for countSpools(t, stream.dir) != 0 {
		if time.Now().After(deadline) {
			stream.failf(t, "spool still there after an interrupt")
		}

		time.Sleep(time.Millisecond)
	}

	stream.signal(t, os.Interrupt)

	var exit *exec.ExitError

	err := stream.Wait()
	if !errors.As(err, &exit) {
		t.Fatalf("stream exited with %v, want it ended by the second interrupt\n%s", err, stream.stderr)
	}

	if status, ok := exit.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGINT {
		t.Fatalf("stream exited with %v, want it ended by the second interrupt\n%s", err, stream.stderr)
	}
}

// With --output it writes the capture to that file instead of stdout, and
// with --from-now it leaves out the packets captured before it started.
func TestVMCaptureStreamFromNowToFile(t *testing.T) {
	t.Parallel()

	node := &fakeNode{captured: append(pcapHeader(), pcapRecord(60)...)}
	file := filepath.Join(t.TempDir(), "live.pcap")
	stream := startCaptureStream(t, node, captureStreamOptions{
		args: []string{"test-experiment", "test-vm", "IF0", "--from-now", "--output", file},
	})

	stream.expectFile(t, file, "the header", pcapHeader())

	packet := pcapRecord(70)
	node.capture(packet)
	stream.expectFile(t, file, "the header and a packet captured since", append(pcapHeader(), packet...))

	stream.signal(t, os.Interrupt)

	if err := stream.Wait(); err != nil {
		t.Fatalf("stream exited with %v, want success\n%s", err, stream.stderr)
	}

	if out, err := io.ReadAll(stream.out); err != nil || len(out) != 0 {
		t.Fatalf("wrote % x to stdout (%v), want nothing", out, err)
	}

	if n := countSpools(t, stream.dir); n != 0 {
		t.Fatalf("%d spools left behind", n)
	}
}

// Asked for a capture it cannot stream, or to write one to a terminal, it
// fails without following the capture.
func TestVMCaptureStreamRefuses(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		args     []string
		terminal bool   // stdout is a terminal
		want     string // in what it logs
	}{
		{
			name: "without an interface",
			args: []string{"test-experiment", "test-vm"},
			want: "must provide an experiment name, VM name, and iface name/index",
		},
		{
			name: "an unknown interface",
			args: []string{"test-experiment", "test-vm", "IF9"},
			want: "Unable to resolve interface IF9 on the test-vm VM",
		},
		{
			name: "an interface not being captured",
			args: []string{"test-experiment", "test-vm", "IF1"},
			want: "Unable to follow the packet capture on the test-vm VM",
		},
		{
			name: "a file it cannot create",
			args: []string{"test-experiment", "test-vm", "IF0", "--output", filepath.Join(os.DevNull, "live.pcap")},
			want: "creating " + filepath.Join(os.DevNull, "live.pcap"),
		},
		{
			name:     "a terminal",
			terminal: true,
			want:     "refusing to write a packet capture to a terminal",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := captureStreamOptions{args: tt.args}

			if tt.terminal {
				ptmx, tty, err := pty.Open()
				if err != nil {
					t.Fatalf("opening a terminal: %v", err)
				}

				t.Cleanup(func() { _, _ = ptmx.Close(), tty.Close() })

				opts.stdout = tty
			}

			node := &fakeNode{captured: pcapHeader()}
			stream := startCaptureStream(t, node, opts)

			var exit *exec.ExitError

			if err := stream.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("stream exited with %v, want status 1\n%s", err, stream.stderr)
			}

			if !strings.Contains(stream.stderr.String(), tt.want) {
				t.Fatalf("stream logged %q, want %q", stream.stderr, tt.want)
			}

			select {
			case <-node.asked:
				t.Fatal("it read the capture")
			default:
			}

			if n := countSpools(t, stream.dir); n != 0 {
				t.Fatalf("%d spools left behind", n)
			}
		})
	}
}

// Redirected to /dev/null, a character device that is not a terminal, it
// streams.
func TestVMCaptureStreamToDevNull(t *testing.T) {
	t.Parallel()

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = devNull.Close() })

	node := &fakeNode{captured: pcapHeader()}
	stream := startCaptureStream(t, node, captureStreamOptions{stdout: devNull})

	select {
	case <-node.asked:
	case <-time.After(10 * time.Second):
		stream.failf(t, "it never read the capture")
	}

	stream.signal(t, os.Interrupt)

	if err := stream.Wait(); err != nil {
		t.Fatalf("stream exited with %v, want success\n%s", err, stream.stderr)
	}

	if n := countSpools(t, stream.dir); n != 0 {
		t.Fatalf("%d spools left behind", n)
	}
}

// TestCaptureStreamProcess is not a test: it is the process the tests above
// start, running `phenix vm capture stream` with the arguments they give it
// for a capture on another node.
func TestCaptureStreamProcess(t *testing.T) {
	dir := os.Getenv(captureStreamDirEnv)
	if dir == "" {
		return
	}

	if sig, err := strconv.Atoi(os.Getenv(captureStreamIgnoreEnv)); err == nil {
		signal.Ignore(syscall.Signal(sig))
	}

	installCaptureCmdTestExperiment(t)

	mm.DefaultMM = remoteCaptureMM{&captureCmdTestMM{ //nolint:reassign // this process streams from a fake cluster
		vmInfo: mm.VMs{{Name: "test-vm", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}}},
		captures: []mm.Capture{
			{VM: "test-vm", Interface: 0, Filepath: "/phenix/images/test-experiment/files/test-vm-0.pcap"},
		},
	}}

	common.MinimegaBase = dir //nolint:reassign // this process talks to the test's fake minimega

	stream := newVMCaptureStreamCmd()
	stream.SetArgs(strings.Split(os.Getenv(captureStreamArgsEnv), "\n"))

	if err := stream.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	os.Exit(0)
}

// remoteCaptureMM is a cluster whose VM runs, and is captured, on a node
// other than the headnode.
type remoteCaptureMM struct {
	*captureCmdTestMM
}

func (remoteCaptureMM) GetVMHost(...mm.Option) (string, error) { return "compute1", nil }

func (remoteCaptureMM) IsHeadnode(string) bool { return false }

// captureStream is a running `phenix vm capture stream` process.
type captureStream struct {
	*exec.Cmd

	dir    string   // its temporary directory, where it spools and finds minimega
	out    *os.File // what it writes to stdout, unless that is a terminal
	stderr *bytes.Buffer
}

// captureStreamOptions set how startCaptureStream starts the process.
type captureStreamOptions struct {
	// a signal it starts out ignoring, as nohup has it ignore a hangup
	ignoring os.Signal
	// its arguments; by default, those that stream test-vm's IF0
	args []string
	// its stdout; by default, a pipe it reads from out
	stdout *os.File
}

// startCaptureStream starts the stream process, with node as the minimega
// it reads the capture through.
func startCaptureStream(t *testing.T, node *fakeNode, opts captureStreamOptions) *captureStream {
	t.Helper()

	// not t.TempDir(): the minimega socket in it must fit in a unix socket
	// address
	dir, err := os.MkdirTemp("", "cs") //nolint:usetesting // see above
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	node.listen(t, dir)

	var out *os.File

	w := opts.stdout
	if w == nil {
		out, w, err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = out.Close() })

		// the process has its own copy
		defer w.Close()
	}

	args := opts.args
	if args == nil {
		args = []string{"test-experiment", "test-vm", "IF0"}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	stream := &captureStream{
		Cmd:    exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCaptureStreamProcess$"),
		dir:    dir,
		out:    out,
		stderr: new(bytes.Buffer),
	}

	stream.Env = append(
		os.Environ(),
		captureStreamDirEnv+"="+dir,
		captureStreamArgsEnv+"="+strings.Join(args, "\n"),
		"TMPDIR="+dir,
		// a race-enabled binary otherwise waits a second before exiting
		"GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"),
	)

	if sig, ok := opts.ignoring.(syscall.Signal); ok {
		stream.Env = append(stream.Env, captureStreamIgnoreEnv+"="+strconv.Itoa(int(sig)))
	}

	stream.Stdout, stream.Stderr = w, stream.stderr

	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}

	return stream
}

// expect reads what the process streams next, which must be want.
func (s *captureStream) expect(t *testing.T, what string, want []byte) {
	t.Helper()

	got := make([]byte, len(want))

	_ = s.out.SetReadDeadline(time.Now().Add(10 * time.Second))

	if _, err := io.ReadFull(s.out, got); err != nil {
		s.failf(t, "reading %s: %v", what, err)
	}

	if !bytes.Equal(got, want) {
		s.failf(t, "read % x as %s, want % x", got, what, want)
	}
}

// expectFile waits for the process to have written want, and only want, to
// the file.
func (s *captureStream) expectFile(t *testing.T, path, what string, want []byte) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		got, err := os.ReadFile(path) //nolint:gosec // the test names the file
		if err == nil && len(got) >= len(want) {
			if !bytes.Equal(got, want) {
				s.failf(t, "file holds % x, want %s: % x", got, what, want)
			}

			return
		}

		if time.Now().After(deadline) {
			s.failf(t, "file holds % x (%v), want %s", got, err, what)
		}

		time.Sleep(time.Millisecond)
	}
}

func (s *captureStream) signal(t *testing.T, sig os.Signal) {
	t.Helper()

	if err := s.Process.Signal(sig); err != nil {
		s.failf(t, "sending %v: %v", sig, err)
	}
}

// failf ends the process, then the test, with what the process logged.
func (s *captureStream) failf(t *testing.T, format string, args ...any) {
	t.Helper()

	_ = s.Process.Kill()
	_ = s.Wait()

	t.Fatalf(format+"\n%s", append(args, s.stderr)...)
}

func countSpools(t *testing.T, dir string) int {
	t.Helper()

	spools, err := filepath.Glob(filepath.Join(dir, "phenix-capture-*.pcap"))
	if err != nil {
		t.Fatal(err)
	}

	return len(spools)
}

// fakeNode is minimega, as the stream process sees it, with the VM's capture
// on another node. It answers each mesh read of the capture file with that
// byte range of what has been captured so far; a silent node never answers.
type fakeNode struct {
	silent bool
	asked  chan struct{} // has a read once one has arrived

	mu       sync.Mutex
	captured []byte
}

// capture adds packets to the capture.
func (n *fakeNode) capture(packets []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.captured = append(n.captured, packets...)
}

func (n *fakeNode) listen(t *testing.T, dir string) {
	t.Helper()

	listener, err := net.Listen("unix", filepath.Join(dir, "minimega"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = listener.Close() })

	n.asked = make(chan struct{}, 1)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go n.serve(conn)
		}
	}()
}

func (n *fakeNode) serve(conn net.Conn) {
	defer conn.Close()

	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)

	for {
		var req miniclient.Request
		if err := dec.Decode(&req); err != nil {
			return
		}

		// phenix asks for minimega's files directory before it names a VM's
		// disk; that is not a read of the capture
		if fields := strings.Fields(req.Command); fields[len(fields)-1] == "args" {
			if err := enc.Encode(miniclient.Response{Resp: minicli.Responses{{Host: "head"}}}); err != nil {
				return
			}

			continue
		}

		select {
		case n.asked <- struct{}{}:
		default:
		}

		if n.silent {
			continue
		}

		// the read ends with its offset and byte count
		args := strings.Fields(req.Command)
		off, _ := strconv.Atoi(args[len(args)-2])
		count, _ := strconv.Atoi(args[len(args)-1])

		n.mu.Lock()
		part := n.captured[min(off, len(n.captured)):min(off+count, len(n.captured))]
		resp := miniclient.Response{
			Resp: minicli.Responses{{Host: "compute1", Response: base64.StdEncoding.EncodeToString(part)}},
		}
		n.mu.Unlock()

		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

// pcapHeader is the header of a pcap file of Ethernet frames.
func pcapHeader() []byte {
	b := make([]byte, 24)
	binary.LittleEndian.PutUint32(b, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(b[4:], 2)
	binary.LittleEndian.PutUint16(b[6:], 4)
	binary.LittleEndian.PutUint32(b[16:], 1600)
	binary.LittleEndian.PutUint32(b[20:], 1)

	return b
}

// pcapRecord is a captured packet of payload bytes.
func pcapRecord(payload int) []byte {
	b := make([]byte, 16+payload)
	binary.LittleEndian.PutUint32(b[8:], uint32(payload))
	binary.LittleEndian.PutUint32(b[12:], uint32(payload))

	return b
}
