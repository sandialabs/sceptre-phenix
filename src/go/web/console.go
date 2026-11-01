package web

import (
	"context"
	"encoding/json"
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
	"phenix/web/audit"
	"phenix/web/lifecycle"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
	"phenix/web/weberror"
)

var (
	ptys  = map[int]*os.File{} //nolint:gochecknoglobals // global state
	ptyMu sync.Mutex           //nolint:gochecknoglobals // global lock
)

const consoleResizeSleep = 100 * time.Millisecond

// CreateConsole - POST /console.
func CreateConsole(w http.ResponseWriter, r *http.Request) error {
	if !o.minimegaConsole {
		plog.Error(plog.TypeSystem, "request made for minimega console, but console not enabled")

		return weberror.New(http.StatusMethodNotAllowed, "'minimega-console' CLI arg not enabled")
	}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("miniconsole", "post") {
		audit.Denied(r, audit.ConsoleCreate, "creating miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	// create a new console
	phenix, err := os.Executable()
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to get full path to phenix")

		return weberror.New(http.StatusInternalServerError, "")
	}

	// the console must outlive this request; r.Context() is canceled as soon
	// as the response is written, which would SIGKILL the attach process
	cmd := exec.CommandContext(
		context.WithoutCancel(r.Context()),
		phenix,
		"mm",
		"--attach",
	) //nolint:gosec // Command injection via taint analysis

	tty, err := pty.Start(cmd)
	if err != nil {
		audit.Failure(r, audit.ConsoleCreate, "starting miniconsole failed", audit.Fields{}, err)

		return weberror.New(http.StatusInternalServerError, fmt.Sprintf("could not start terminal: %v", err))
	}

	pid := cmd.Process.Pid

	plog.Info(plog.TypeSystem, "spawned new minimega console", "pid", pid)

	ptyMu.Lock()
	ptys[pid] = tty
	ptyMu.Unlock()

	// reap the console process when it exits and drop its dead pty
	go func() {
		_ = cmd.Wait()

		ptyMu.Lock()
		if t, ok := ptys[pid]; ok && t == tty {
			_ = t.Close()
			delete(ptys, pid)
		}
		ptyMu.Unlock()

		plog.Debug(plog.TypeSystem, "minimega console exited", "pid", pid)
	}()

	body, _ := json.Marshal(util.WithRoot("pid", pid))

	audit.Success(r, audit.ConsoleCreate, "miniconsole created", audit.Fields{PID: pid})

	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis

	return nil
}

// ResizeConsole - POST /console/{pid}/size?cols={[0-9]+}&rows={[0-9]+}.
func ResizeConsole(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("miniconsole", "post") {
		audit.Denied(r, audit.ConsoleAttach, "resizing miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	vars := mux.Vars(r)

	pid, err := strconv.Atoi(vars["pid"])
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid pid")
	}

	if err := r.ParseForm(); err != nil {
		return weberror.New(http.StatusBadRequest, "bad form")
	}

	ptyMu.Lock()
	tty, ok := ptys[pid]
	ptyMu.Unlock()

	if !ok {
		return weberror.New(http.StatusNotFound, "pty not found")
	}

	rows, err := strconv.ParseUint(r.FormValue("rows"), 10, 16)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid rows")
	}

	cols, err := strconv.ParseUint(r.FormValue("cols"), 10, 16)
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid cols")
	}

	plog.Debug(plog.TypeSystem, "resize console", "pid", pid, "cols", cols, "rows", rows)

	ws := struct {
		R, C, X, Y uint16
	}{
		R: uint16(rows), C: uint16(cols), X: 0, Y: 0,
	}

	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		tty.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)

	if errno != 0 {
		plog.Error(plog.TypeSystem, "unable to set winsize", "err", errno)

		return weberror.New(http.StatusInternalServerError, "set winsize failed")
	}

	// make sure winsize gets processed, hopefully the user isn't typing...
	time.Sleep(consoleResizeSleep)

	_, _ = io.WriteString(tty, "\n")

	return nil
}

// WsConsole - GET /console/{pid}/ws.
func WsConsole(w http.ResponseWriter, r *http.Request) error {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("miniconsole", "get") {
		audit.Denied(r, audit.ConsoleAttach, "attaching to miniconsole not allowed", audit.Fields{})

		return weberror.New(http.StatusForbidden, "forbidden")
	}

	vars := mux.Vars(r)

	pid, err := strconv.Atoi(vars["pid"])
	if err != nil {
		return weberror.New(http.StatusBadRequest, "invalid pid")
	}

	if err := r.ParseForm(); err != nil {
		return weberror.New(http.StatusBadRequest, "bad form")
	}

	ptyMu.Lock()
	tty, ok := ptys[pid]
	ptyMu.Unlock()

	if !ok {
		return weberror.New(http.StatusNotFound, "pty not found")
	}

	if lifecycle.RefuseWhileStopping(w) {
		return nil
	}

	websocket.Handler(func(ws *websocket.Conn) {
		defer lifecycle.TrackConn("minimega console", ws)()
		defer func() { _ = tty.Close() }()

		proc, err := os.FindProcess(pid)
		if err != nil {
			audit.Failure(r, audit.ConsoleAttach, "attaching to miniconsole failed", audit.Fields{PID: pid}, err)

			return
		}

		audit.Success(r, audit.ConsoleAttach, "attached to miniconsole", audit.Fields{PID: pid})

		go func() { _, _ = io.Copy(ws, tty) }()

		_, _ = io.Copy(tty, ws)

		plog.Debug(plog.TypeSystem, "killing minimega console", "pid", pid)

		_ = proc.Kill()
		_, _ = proc.Wait()

		ptyMu.Lock()
		delete(ptys, pid)
		ptyMu.Unlock()

		plog.Debug(plog.TypeSystem, "killed minimega console", "pid", pid)
	}).ServeHTTP(w, r)

	return nil
}
