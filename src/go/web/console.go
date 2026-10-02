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
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/creack/pty"
	"github.com/gorilla/mux"
	"golang.org/x/net/websocket"

	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/util"
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
	// consoleClientQueue is how many chunks of output may wait for a
	// websocket before it is dropped for not keeping up.
	consoleClientQueue  = 256
	consoleReadSize     = 32 * 1024
	consoleWriteTimeout = 10 * time.Second
	// consoleDrainTimeout bounds the wait for a console's last output once
	// its process has exited.
	consoleDrainTimeout = time.Second
	// consoleEndTimeout bounds how long ending a console waits for it.
	consoleEndTimeout  = 5 * time.Second
	consoleResizeSleep = 100 * time.Millisecond
)

var errConsoleEnded = errors.New("console ended")

// consoles are the running minimega consoles, by pid.
var consoles = newConsoleRegistry( //nolint:gochecknoglobals // shared by every request
	consoleIdleGrace,
	minimegaConsoleCommand,
)

// CreateConsole - POST /console.
func CreateConsole(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		user = middleware.UserFromContext(ctx)
	)

	if !o.minimegaConsole {
		plog.Error(plog.TypeSystem, "request made for minimega console, but console not enabled")
		http.Error(w, "'minimega-console' CLI arg not enabled", http.StatusMethodNotAllowed)

		return
	}

	if !role.Allowed("miniconsole", "post") {
		plog.Warn(plog.TypeSecurity, "creating miniconsole not allowed", "user", user)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// the console must outlive this request; r.Context() is canceled as soon
	// as the response is written, which would SIGKILL the console process
	console, err := consoles.start(context.WithoutCancel(ctx), user)
	if err != nil {
		plog.Error(plog.TypeSystem, "starting minimega console", "err", err)
		http.Error(w, "could not start terminal: "+err.Error(), http.StatusInternalServerError)

		return
	}

	plog.Info(plog.TypeSystem, "spawned new minimega console", "pid", console.pid)
	plog.Info(plog.TypeAction, "miniconsole created", "user", user)

	body, _ := json.Marshal(util.WithRoot("pid", console.pid))
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// GetConsole - GET /console/{pid}.
//
// Answers whether the console is still running, so the UI can attach to the
// console it had open (after a reload, or from another tab) or start another.
func GetConsole(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("miniconsole", "get") {
		plog.Warn(
			plog.TypeSecurity,
			"getting miniconsole not allowed",
			"user",
			middleware.UserFromContext(r.Context()),
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	console, ok := ownConsole(w, r)
	if !ok {
		return
	}

	body, _ := json.Marshal(util.WithRoot("pid", console.pid))
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// DeleteConsole - DELETE /console/{pid}.
//
// Ending a console needs the same permission as starting one: it is the
// user's own process, and the ownership check is what guards it.
func DeleteConsole(w http.ResponseWriter, r *http.Request) {
	var (
		ctx  = r.Context()
		role = middleware.RoleFromContext(ctx)
		user = middleware.UserFromContext(ctx)
	)

	if !role.Allowed("miniconsole", "post") {
		plog.Warn(plog.TypeSecurity, "ending miniconsole not allowed", "user", user)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	console, ok := ownConsole(w, r)
	if !ok {
		return
	}

	plog.Info(plog.TypeAction, "miniconsole ended", "user", user, "pid", console.pid)

	if !console.end() {
		plog.Error(plog.TypeSystem, "minimega console did not end", "pid", console.pid)
		http.Error(w, "console did not end", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ResizeConsole - POST /console/{pid}/size?cols={[0-9]+}&rows={[0-9]+}.
func ResizeConsole(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("miniconsole", "post") {
		plog.Warn(
			plog.TypeSecurity,
			"resizing miniconsole not allowed",
			"user",
			middleware.UserFromContext(r.Context()),
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	console, ok := ownConsole(w, r)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)

		return
	}

	rows, err := strconv.ParseUint(r.FormValue("rows"), 10, 16)
	if err != nil {
		http.Error(w, "invalid rows", http.StatusBadRequest)

		return
	}

	cols, err := strconv.ParseUint(r.FormValue("cols"), 10, 16)
	if err != nil {
		http.Error(w, "invalid cols", http.StatusBadRequest)

		return
	}

	plog.Debug(plog.TypeSystem, "resize console", "pid", console.pid, "cols", cols, "rows", rows)

	if err := console.resize(uint16(rows), uint16(cols)); err != nil {
		plog.Error(plog.TypeSystem, "unable to set winsize", "pid", console.pid, "err", err)
		http.Error(w, "set winsize failed", http.StatusInternalServerError)

		return
	}

	// make sure winsize gets processed, hopefully the user isn't typing...
	time.Sleep(consoleResizeSleep)

	_, _ = io.WriteString(console.tty, "\n")
}

// WsConsole - GET /console/{pid}/ws.
//
// Attaches a websocket to the console: it first gets the console's recent
// output, then its live output, and what it sends is the console's input.
// Closing it leaves the console running.
func WsConsole(w http.ResponseWriter, r *http.Request) {
	role := middleware.RoleFromContext(r.Context())

	if !role.Allowed("miniconsole", "get") {
		plog.Warn(
			plog.TypeSecurity,
			"getting miniconsole not allowed",
			"user",
			middleware.UserFromContext(r.Context()),
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	console, ok := ownConsole(w, r)
	if !ok {
		return
	}

	websocket.Handler(func(ws *websocket.Conn) {
		// binary frames: output cut between two reads need not be valid UTF-8
		ws.PayloadType = websocket.BinaryFrame

		client, err := console.attach()
		if err != nil {
			return
		}

		plog.Debug(plog.TypeSystem, "attached to minimega console", "pid", console.pid)

		written := make(chan struct{})
		go client.serve(ws, written)

		_, _ = io.Copy(console.tty, ws)

		console.detach(client)
		<-written

		plog.Debug(plog.TypeSystem, "detached from minimega console", "pid", console.pid)
	}).ServeHTTP(w, r)
}

// ownConsole returns the running console the request's pid names when the
// requesting user started it, and otherwise answers the request itself.
// Another user's console is reported as not found, so pids are not probed.
func ownConsole(w http.ResponseWriter, r *http.Request) (*consoleSession, bool) {
	pid, err := strconv.Atoi(mux.Vars(r)["pid"])
	if err != nil {
		http.Error(w, "invalid pid", http.StatusBadRequest)

		return nil, false
	}

	user := middleware.UserFromContext(r.Context())

	console := consoles.lookup(pid)
	if console != nil && console.owner != user {
		plog.Warn(
			plog.TypeSecurity,
			"minimega console belongs to another user",
			"user",
			user,
			"pid",
			pid,
		)

		console = nil
	}

	if console == nil {
		http.Error(w, "console not found", http.StatusNotFound)

		return nil, false
	}

	return console, true
}

// minimegaConsoleCommand runs `phenix mm --attach`, the minimega CLI.
func minimegaConsoleCommand(ctx context.Context) (*exec.Cmd, error) {
	phenix, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("getting full path to phenix: %w", err)
	}

	return exec.CommandContext(ctx, phenix, "mm", "--attach"), nil //nolint:gosec // fixed arguments
}

type consoleRegistry struct {
	mu       sync.Mutex
	sessions map[int]*consoleSession

	grace   time.Duration
	command func(context.Context) (*exec.Cmd, error)
}

func newConsoleRegistry(
	grace time.Duration,
	command func(context.Context) (*exec.Cmd, error),
) *consoleRegistry {
	return &consoleRegistry{
		mu:       sync.Mutex{},
		sessions: make(map[int]*consoleSession),
		grace:    grace,
		command:  command,
	}
}

// start runs a new console for owner. It ends after the grace period unless
// a websocket attaches.
func (r *consoleRegistry) start(ctx context.Context, owner string) (*consoleSession, error) {
	cmd, err := r.command(ctx)
	if err != nil {
		return nil, err
	}

	tty, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("starting console process: %w", err)
	}

	console := &consoleSession{
		pid:      cmd.Process.Pid,
		owner:    owner,
		tty:      tty,
		proc:     cmd.Process,
		grace:    r.grace,
		registry: r,
		done:     make(chan struct{}),
		mu:       sync.Mutex{},
		clients:  make(map[*consoleClient]struct{}),
		history:  newConsoleHistory(consoleHistorySize),
		idle:     nil,
		idleGen:  0,
		ended:    false,
	}

	r.mu.Lock()
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

func (r *consoleRegistry) lookup(pid int) *consoleSession {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.sessions[pid]
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
	pid      int
	owner    string
	tty      *os.File
	proc     *os.Process
	grace    time.Duration
	registry *consoleRegistry
	// done is closed once the console has ended.
	done chan struct{}

	mu      sync.Mutex
	clients map[*consoleClient]struct{}
	history *consoleHistory
	idle    *time.Timer
	// idleGen tells a stale idle timer, one stopped too late, to do nothing.
	idleGen int
	ended   bool
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

func (s *consoleSession) broadcast(output []byte) {
	chunk := bytes.Clone(output)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return
	}

	s.history.Write(chunk)

	for client := range s.clients {
		select {
		case client.out <- chunk:
		default:
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
	s.mu.Unlock()

	_ = s.tty.Close()

	close(s.done)

	plog.Info(plog.TypeSystem, "minimega console exited", "pid", s.pid)
}

// attach adds a websocket client, which first gets the console's history.
func (s *consoleSession) attach() (*consoleClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return nil, errConsoleEnded
	}

	s.stopIdleLocked()

	client := &consoleClient{out: make(chan []byte, consoleClientQueue)}

	// queued under the lock that live output takes, so the client gets
	// every byte of output once
	if history := s.history.Bytes(); len(history) > 0 {
		client.out <- history
	}

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
	close(client.out)

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

	_ = s.proc.Kill()
}

// end kills the console's process and reports whether the console ended.
func (s *consoleSession) end() bool {
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

// consoleClient is a websocket attached to a console.
type consoleClient struct {
	// out is the output waiting to be written; the console closes it when it
	// drops the client.
	out chan []byte
}

// serve writes the client's output to ws until the console drops the client
// or a write fails, then closes ws.
func (c *consoleClient) serve(ws *websocket.Conn, written chan<- struct{}) {
	defer close(written)
	defer func() { _ = ws.Close() }()

	for chunk := range c.out {
		_ = ws.SetWriteDeadline(time.Now().Add(consoleWriteTimeout))

		if _, err := ws.Write(chunk); err != nil {
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
