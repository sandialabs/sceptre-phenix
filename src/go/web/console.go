package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"

	"phenix/api/experiment"
	"phenix/util/plog"
	"phenix/web/audit"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/weberror"
)

// Why a console ended, as its audit event's phenix.console.end_reason says.
const (
	consoleEndedByUser   = "user"
	consoleEndedIdle     = "idle"
	consoleExited        = "exited"
	consoleEndedShutdown = "shutdown"
)

// A minimega console outlives the websockets that show it, so the user keeps
// it (and its output) while moving between UI pages, reloading, or opening
// another tab. It ends when its process exits (the user typed CTRL+D or
// disconnect), when its owner ends it (logging out), or once no websocket has
// been attached for consoleIdleGrace (the last tab showing it closed).
const (
	// consoleIdleGrace is long enough for a page reload to attach again.
	consoleIdleGrace = 15 * time.Second
	// consoleHistorySize is how much recent output is replayed to a
	// websocket that attaches.
	consoleHistorySize = 256 * 1024
	// consoleClientBacklog is how many bytes of output may wait for a
	// websocket before it is dropped for not keeping up. It holds a full
	// history replay.
	consoleClientBacklog = 1024 * 1024
	consoleReadSize      = 32 * 1024
	consoleWriteTimeout  = 10 * time.Second
	// consoleFrameSize is the most output one websocket frame carries.
	consoleFrameSize = 64 * 1024
	// consoleDrainTimeout bounds the wait for a console's last output once
	// its process has exited.
	consoleDrainTimeout = time.Second
	// consoleEndTimeout bounds how long ending a console waits for it.
	consoleEndTimeout = 5 * time.Second
	// consoleCols and consoleRows are the size a console's pty starts at, the
	// browser terminal's own default, until the browser sends its size.
	consoleCols = 80
	consoleRows = 24
)

var (
	errConsoleEnded    = errors.New("console ended")
	errConsoleNotEnded = errors.New("console did not end")
	errServerStopping  = errors.New(lifecycle.CloseReason)
)

// consoles are the running minimega consoles, by pid.
var consoles = newConsoleRegistry( //nolint:gochecknoglobals // shared by every request
	consoleIdleGrace,
	minimegaConsoleCommand,
)

// CreateConsole - POST /console?namespace={experiment}.
//
// With a namespace, the console's default minimega namespace is that of the
// experiment it names, which must exist and be one the role may get.
func CreateConsole(w http.ResponseWriter, r *http.Request) error {
	var (
		ctx       = r.Context()
		role      = middleware.RoleFromContext(ctx)
		user      = middleware.UserFromContext(ctx)
		namespace = r.URL.Query().Get("namespace")
	)

	if !o.minimegaConsole {
		plog.Error(plog.TypeSystem, "minimega console requested, but the console is not enabled")

		return weberror.New(http.StatusMethodNotAllowed, "'minimega-console' CLI argument not enabled")
	}

	if !role.Allowed("miniconsole", "post") {
		audit.Denied(r, audit.ConsoleCreate, "creating miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	if err := checkConsoleNamespace(r, namespace); err != nil {
		return err
	}

	// the console must outlive this request; r.Context() is canceled as soon
	// as the response is written, which would SIGKILL the console process
	console, err := consoles.start(context.WithoutCancel(ctx), user, namespace)
	if errors.Is(err, errServerStopping) {
		lifecycle.Refuse(w)

		return nil
	}

	if err != nil {
		plog.Error(plog.TypeSystem, "starting minimega console", "err", err)
		audit.Failure(r, audit.ConsoleCreate, "starting miniconsole failed", audit.Fields{}, err)

		return weberror.New(http.StatusInternalServerError, "could not start terminal: "+err.Error())
	}

	audit.Success(r, audit.ConsoleCreate, "miniconsole created", audit.Fields{PID: console.pid, Experiment: namespace})

	writeConsole(w, console)

	return nil
}

// checkConsoleNamespace returns a 400 unless namespace is empty or names an
// experiment that exists and that the request's role may get. minimega
// creates a namespace that does not exist, so an unknown name is refused, and
// one the role may not see is refused the same way, so names are not probed.
func checkConsoleNamespace(r *http.Request, namespace string) error {
	if namespace == "" {
		return nil
	}

	invalid := weberror.New(http.StatusBadRequest, "no experiment named "+strconv.Quote(namespace))

	if !middleware.RoleFromContext(r.Context()).Allowed("experiments", "get", namespace) {
		audit.Denied(r, audit.ConsoleCreate, "miniconsole namespace not allowed", audit.Fields{Experiment: namespace})

		return invalid
	}

	if _, err := experiment.Get(namespace); err != nil {
		if storeStatus(err) == http.StatusNotFound {
			return invalid
		}

		return weberror.NewWebError(err, "unable to get experiment %s from store", namespace).SetStatus(storeStatus(err))
	}

	return nil
}

// writeConsole answers with the console's pid and namespace.
func writeConsole(w http.ResponseWriter, console *consoleSession) {
	body, _ := json.Marshal(map[string]any{"pid": console.pid, "namespace": console.namespace})

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// GetConsole - GET /console/{pid}.
//
// Answers whether the console is still running, so the UI can attach to the
// console it had open (after a reload, or from another tab) or start another.
func GetConsole(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("miniconsole", "get") {
		audit.Denied(r, audit.ConsoleAttach, "getting miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	console, err := ownConsole(r, audit.ConsoleAttach)
	if err != nil {
		return err
	}

	writeConsole(w, console)

	return nil
}

// DeleteConsole - DELETE /console/{pid}.
//
// Ending a console needs the same permission as starting one: it is the
// user's own process, and the ownership check is what guards it.
func DeleteConsole(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("miniconsole", "post") {
		audit.Denied(r, audit.ConsoleTerminate, "ending miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	console, err := ownConsole(r, audit.ConsoleTerminate)
	if err != nil {
		return err
	}

	fields := audit.Fields{PID: console.pid}

	if !console.end(consoleEndedByUser) {
		plog.Error(plog.TypeSystem, "minimega console did not end", "pid", console.pid)
		audit.Failure(r, audit.ConsoleTerminate, "ending miniconsole failed", fields, errConsoleNotEnded)

		return weberror.New(http.StatusInternalServerError, "console did not end")
	}

	audit.Success(r, audit.ConsoleTerminate, "miniconsole ended", fields)

	w.WriteHeader(http.StatusNoContent)

	return nil
}

// ResizeConsole - POST /console/{pid}/size?cols={[0-9]+}&rows={[0-9]+}.
func ResizeConsole(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("miniconsole", "post") {
		audit.Denied(r, audit.ConsoleAttach, "resizing miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	console, err := ownConsole(r, audit.ConsoleAttach)
	if err != nil {
		return err
	}

	if err := r.ParseForm(); err != nil {
		return weberror.New(http.StatusBadRequest, "invalid form")
	}

	rows, err := strconv.ParseUint(r.FormValue("rows"), 10, 16)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid rows")
	}

	cols, err := strconv.ParseUint(r.FormValue("cols"), 10, 16)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid cols")
	}

	plog.Debug(plog.TypeSystem, "resize console", "pid", console.pid, "cols", cols, "rows", rows)

	// the kernel signals SIGWINCH, and the console's line editor redraws its
	// line at the new size
	if err := console.resize(uint16(rows), uint16(cols)); err != nil {
		plog.Error(plog.TypeSystem, "unable to set winsize", "pid", console.pid, "err", err)

		return weberror.New(http.StatusInternalServerError, "set winsize failed")
	}

	return nil
}

// WsConsole - GET /console/{pid}/ws.
//
// Attaches a websocket to the console: it first gets the console's recent
// output, then its live output, and what it sends is the console's input.
// Closing it leaves the console running.
func WsConsole(w http.ResponseWriter, r *http.Request) error {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("miniconsole", "get") {
		audit.Denied(r, audit.ConsoleAttach, "attaching to miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	console, err := ownConsole(r, audit.ConsoleAttach)
	if err != nil {
		return err
	}

	if lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	websocket.Handler(func(ws *websocket.Conn) {
		defer lifecycle.TrackConn("minimega console", ws)()

		// binary frames: output cut between two reads need not be valid UTF-8
		ws.PayloadType = websocket.BinaryFrame

		client, err := console.attach()
		if err != nil {
			audit.Failure(r, audit.ConsoleAttach, "attaching to miniconsole failed", audit.Fields{PID: console.pid}, err)

			return
		}

		audit.Success(r, audit.ConsoleAttach, "attached to miniconsole", audit.Fields{PID: console.pid})

		written := make(chan struct{})
		go client.serve(ws, written)

		_, _ = io.Copy(console.tty, ws)

		console.detach(client)
		<-written

		plog.Debug(plog.TypeSystem, "detached from minimega console", "pid", console.pid)
	}).ServeHTTP(w, r)

	return nil
}

// ownConsole returns the running console the request's pid names if the user started it, and otherwise the
// error to answer: another user's console is not found, so pids are not probed, and action a is audited.
func ownConsole(r *http.Request, a audit.Action) (*consoleSession, error) {
	pid, err := strconv.Atoi(mux.Vars(r)["pid"])
	if err != nil {
		return nil, weberror.New(http.StatusBadRequest, "invalid pid")
	}

	user := middleware.UserFromContext(r.Context())

	console := consoles.lookup(pid)
	if console != nil && console.owner != user {
		audit.Denied(r, a, "minimega console belongs to another user", audit.Fields{PID: pid, Reason: "not owner"})

		console = nil
	}

	if console == nil {
		return nil, weberror.New(http.StatusNotFound, "console not found")
	}

	return console, nil
}

// consoleTerm is the TERM a console's process gets: the browser terminal is
// xterm.js. minimega's line editor (liner) falls back to a plain reader, with
// no history or completion, when TERM is unset, "dumb" or "cons25".
const consoleTerm = "TERM=xterm-256color"

// minimegaConsoleCommand runs `phenix mm --attach`, the minimega CLI, with the
// server's environment and TERM set to consoleTerm. A namespace, unless
// empty, is the CLI's default minimega namespace.
func minimegaConsoleCommand(ctx context.Context, namespace string) (*exec.Cmd, error) {
	phenix, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("getting full path to phenix: %w", err)
	}

	args := []string{"mm", "--attach"}
	if namespace != "" {
		args = append(args, "--namespace="+namespace)
	}

	cmd := exec.CommandContext(ctx, phenix, args...) //nolint:gosec,nolintlint // G702 runs phenix itself (flagged on some runs)
	cmd.Env = consoleEnv(cmd.Environ())

	return cmd, nil
}

// consoleEnv returns environ with each TERM entry replaced by one consoleTerm.
func consoleEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+1)

	for _, kv := range environ {
		if !strings.HasPrefix(kv, "TERM=") {
			env = append(env, kv)
		}
	}

	return append(env, consoleTerm)
}

type consoleRegistry struct {
	mu       sync.Mutex
	sessions map[int]*consoleSession
	// set by endAll: no console starts after it
	closed bool

	grace   time.Duration
	command func(ctx context.Context, namespace string) (*exec.Cmd, error)
}

func newConsoleRegistry(
	grace time.Duration,
	command func(ctx context.Context, namespace string) (*exec.Cmd, error),
) *consoleRegistry {
	return &consoleRegistry{
		mu:       sync.Mutex{},
		sessions: make(map[int]*consoleSession),
		closed:   false,
		grace:    grace,
		command:  command,
	}
}

// start runs a new console for owner in namespace ("" for none). It ends
// after the grace period unless a websocket attaches.
func (r *consoleRegistry) start(ctx context.Context, owner, namespace string) (*consoleSession, error) {
	select {
	case <-lifecycle.Stopping():
		return nil, errServerStopping
	default:
	}

	cmd, err := r.command(ctx, namespace)
	if err != nil {
		return nil, err
	}

	tty, err := startConsoleProcess(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting console process: %w", err)
	}

	console := &consoleSession{
		pid:       cmd.Process.Pid,
		owner:     owner,
		namespace: namespace,
		tty:       tty,
		proc:      cmd.Process,
		grace:     r.grace,
		registry:  r,
		done:      make(chan struct{}),
		mu:        sync.Mutex{},
		clients:   make(map[*consoleClient]struct{}),
		history:   newConsoleHistory(consoleHistorySize),
		idle:      nil,
		idleGen:   0,
		ended:     false,

		endReason: "",
	}

	r.mu.Lock()

	if r.closed {
		r.mu.Unlock()

		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = tty.Close()

		return nil, errServerStopping
	}

	r.sessions[console.pid] = console
	r.mu.Unlock()

	console.mu.Lock()
	console.armIdleLocked()
	console.mu.Unlock()

	pumped := make(chan struct{})

	go console.pump(pumped)
	go console.wait(cmd, pumped)

	return console, nil
}

// startConsoleProcess starts cmd on a new pty of consoleCols by consoleRows,
// and returns the pty. The size is set before the process starts: minimega's
// line editor reads the width once, and with none it reads plain lines, with
// no history, search or completion.
func startConsoleProcess(cmd *exec.Cmd) (*os.File, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("opening pty: %w", err)
	}

	defer func() { _ = tty.Close() }()

	// sized on the process's side, the side the line editor reads
	if err := pty.Setsize(tty, &pty.Winsize{Rows: consoleRows, Cols: consoleCols, X: 0, Y: 0}); err != nil {
		_ = ptmx.Close()

		return nil, fmt.Errorf("sizing pty: %w", err)
	}

	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = new(syscall.SysProcAttr)
	}

	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true

	if err := cmd.Start(); err != nil {
		_ = ptmx.Close()

		return nil, err //nolint:wrapcheck // the caller wraps it
	}

	return ptmx, nil
}

func (r *consoleRegistry) lookup(pid int) *consoleSession {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.sessions[pid]
}

// endAll ends every console for reason, and waits for them until ctx is done.
// It returns how many had not ended by then.
func (r *consoleRegistry) endAll(ctx context.Context, reason string) int {
	r.mu.Lock()
	r.closed = true
	running := make([]*consoleSession, 0, len(r.sessions))

	for _, console := range r.sessions {
		running = append(running, console)
	}
	r.mu.Unlock()

	for _, console := range running {
		console.mu.Lock()
		console.endingLocked(reason)
		console.mu.Unlock()

		_ = console.proc.Kill()
	}

	var left int

	for _, console := range running {
		select {
		case <-console.done:
		case <-ctx.Done():
			if !isDone(console.done) {
				left++
			}
		}
	}

	return left
}

func isDone(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (r *consoleRegistry) remove(console *consoleSession) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.sessions[console.pid] == console {
		delete(r.sessions, console.pid)
	}
}

// consoleSession is one running console and the websockets attached to it.
type consoleSession struct {
	pid   int
	owner string
	// namespace is the experiment whose minimega namespace the console uses
	// by default, or "" for none.
	namespace string
	tty       *os.File
	proc      *os.Process
	grace     time.Duration
	registry  *consoleRegistry
	// done is closed once the console has ended.
	done chan struct{}

	mu      sync.Mutex
	clients map[*consoleClient]struct{}
	history *consoleHistory
	idle    *time.Timer
	// idleGen tells a stale idle timer, one stopped too late, to do nothing.
	idleGen int
	ended   bool
	// endReason is why the console was ended, once something ended it: the
	// first of consoleEndedByUser and consoleEndedIdle to.
	endReason string
}

// pump copies the console's output to its history and its websockets.
func (s *consoleSession) pump(pumped chan<- struct{}) {
	defer close(pumped)

	buf := make([]byte, consoleReadSize)

	for {
		n, err := s.tty.Read(buf)
		if n > 0 {
			s.broadcast(buf[:n])
		}

		if err != nil {
			return
		}
	}
}

// broadcast keeps output in the history and queues it for each websocket. A
// websocket whose queue would grow past consoleClientBacklog bytes is dropped.
func (s *consoleSession) broadcast(output []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return
	}

	s.history.Write(output)

	for client := range s.clients {
		if !client.queue(output) {
			// it can attach again, and gets the history when it does
			plog.Warn(plog.TypeSystem, "dropping slow minimega console websocket", "pid", s.pid)
			s.removeLocked(client)
		}
	}
}

// wait ends the console once its process has exited.
func (s *consoleSession) wait(cmd *exec.Cmd, pumped <-chan struct{}) {
	_ = cmd.Wait()

	// the process is gone, but its last output may still be in the pty
	select {
	case <-pumped:
	case <-time.After(consoleDrainTimeout):
	}

	// forget it first, so a client told the console ended finds it gone
	s.registry.remove(s)

	s.mu.Lock()
	s.ended = true
	s.stopIdleLocked()

	for client := range s.clients {
		s.removeLocked(client)
	}

	reason := s.endReason
	s.mu.Unlock()

	_ = s.tty.Close()

	if reason == "" {
		reason = consoleExited
	}

	// before done closes, so it is logged by the time anything sees it ended
	audit.Background(s.owner, audit.ConsoleEnd, "minimega console ended", audit.OutcomeSuccess, audit.Fields{
		PID: s.pid, Extra: []any{"phenix.console.end_reason", reason},
	})

	close(s.done)
}

// attach adds a websocket client, which first gets the console's history.
func (s *consoleSession) attach() (*consoleClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return nil, errConsoleEnded
	}

	s.stopIdleLocked()

	client := newConsoleClient()

	// queued under the lock that live output takes, so the client gets
	// every byte of output once; the backlog holds all of the history
	client.queue(s.history.Bytes())

	s.clients[client] = struct{}{}

	return client, nil
}

func (s *consoleSession) detach(client *consoleClient) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.removeLocked(client)
}

// removeLocked drops a client; its websocket closes once it has written the
// output already queued for it. The last one to go starts the idle timer.
func (s *consoleSession) removeLocked(client *consoleClient) {
	if _, ok := s.clients[client]; !ok {
		return
	}

	delete(s.clients, client)
	client.close()

	if len(s.clients) == 0 && !s.ended {
		s.armIdleLocked()
	}
}

func (s *consoleSession) armIdleLocked() {
	s.stopIdleLocked()

	gen := s.idleGen
	s.idle = time.AfterFunc(s.grace, func() { s.expire(gen) })
}

func (s *consoleSession) stopIdleLocked() {
	s.idleGen++

	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
}

// expire ends the console if still no websocket has attached.
func (s *consoleSession) expire(gen int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if gen != s.idleGen || s.ended || len(s.clients) > 0 {
		return
	}

	plog.Info(plog.TypeSystem, "ending unattached minimega console", "pid", s.pid, "after", s.grace)

	s.endingLocked(consoleEndedIdle)

	_ = s.proc.Kill()
}

// endingLocked records why the console is being ended, unless something
// already ended it.
func (s *consoleSession) endingLocked(reason string) {
	if s.endReason == "" {
		s.endReason = reason
	}
}

// end kills the console's process, for the reason, and reports whether the
// console ended.
func (s *consoleSession) end(reason string) bool {
	s.mu.Lock()
	s.endingLocked(reason)
	s.mu.Unlock()

	_ = s.proc.Kill()

	select {
	case <-s.done:
		return true
	case <-time.After(consoleEndTimeout):
		return false
	}
}

func (s *consoleSession) resize(rows, cols uint16) error {
	// through SyscallConn rather than Fd, which would make the pty blocking
	// and so stop Close from interrupting a read
	conn, err := s.tty.SyscallConn()
	if err != nil {
		return fmt.Errorf("getting console pty: %w", err)
	}

	size := struct {
		R, C, X, Y uint16
	}{
		R: rows, C: cols, X: 0, Y: 0,
	}

	var errno syscall.Errno

	err = conn.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(
			syscall.SYS_IOCTL,
			fd,
			syscall.TIOCSWINSZ,
			uintptr(unsafe.Pointer(&size)),
		)
	})
	if err != nil {
		return fmt.Errorf("resizing console pty: %w", err)
	}

	if errno != 0 {
		return fmt.Errorf("resizing console pty: %w", errno)
	}

	return nil
}

// consoleClient is a websocket attached to a console. Its queue is bounded
// in bytes, not in writes: the pty gives output in many small reads.
type consoleClient struct {
	mu sync.Mutex
	// pending is the output waiting to be written.
	pending []byte
	// closed is set when the console drops the client.
	closed bool
	// ready tells serve that pending or closed changed.
	ready chan struct{}
}

func newConsoleClient() *consoleClient {
	return &consoleClient{mu: sync.Mutex{}, pending: nil, closed: false, ready: make(chan struct{}, 1)}
}

// queue adds output to the pending output, and reports false, adding none,
// when the pending output would grow past consoleClientBacklog bytes.
func (c *consoleClient) queue(output []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.pending)+len(output) > consoleClientBacklog {
		return false
	}

	c.pending = append(c.pending, output...)
	c.signal()

	return true
}

// close tells serve to stop once it has written the pending output.
func (c *consoleClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	c.signal()
}

// signal wakes serve; one wake-up already waiting is enough.
func (c *consoleClient) signal() {
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

// take returns the pending output, which it empties, and whether the client
// is closed.
func (c *consoleClient) take() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	output := c.pending
	c.pending = nil

	return output, c.closed
}

// serve writes the client's output to ws, all that is pending in frames of
// at most consoleFrameSize bytes, until the console drops the client or a
// write fails, then closes ws. Each frame has its own write deadline, so a
// slow link has time for a full backlog.
func (c *consoleClient) serve(ws *websocket.Conn, written chan<- struct{}) {
	defer close(written)
	defer func() { _ = ws.Close() }()

	for range c.ready {
		output, closed := c.take()

		for len(output) > 0 {
			frame := output[:min(len(output), consoleFrameSize)]
			output = output[len(frame):]

			_ = ws.SetWriteDeadline(time.Now().Add(consoleWriteTimeout))

			if _, err := ws.Write(frame); err != nil {
				return
			}
		}

		if closed {
			return
		}
	}
}

// consoleHistory is a console's most recent output, up to a fixed size.
type consoleHistory struct {
	buf []byte
	// next is where the next byte goes once buf has filled.
	next    int
	written int
}

func newConsoleHistory(size int) *consoleHistory {
	return &consoleHistory{buf: make([]byte, size), next: 0, written: 0}
}

func (h *consoleHistory) Write(output []byte) {
	size := len(h.buf)
	h.written += len(output)

	if len(output) >= size {
		copy(h.buf, output[len(output)-size:])
		h.next = 0

		return
	}

	n := copy(h.buf[h.next:], output)
	copy(h.buf, output[n:])

	h.next = (h.next + len(output)) % size
}

// Bytes returns the kept output, oldest first. Once older output has been
// dropped it starts at a line, not partway through a line or an escape
// sequence.
func (h *consoleHistory) Bytes() []byte {
	if h.written <= len(h.buf) {
		return bytes.Clone(h.buf[:h.written])
	}

	kept := make([]byte, 0, len(h.buf))
	kept = append(kept, h.buf[h.next:]...)
	kept = append(kept, h.buf[:h.next]...)

	if i := bytes.IndexByte(kept, '\n'); i >= 0 {
		kept = kept[i+1:]
	}

	return kept
}
