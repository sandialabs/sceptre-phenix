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
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/util"
)

var (
	ptys  = map[int]*os.File{} //nolint:gochecknoglobals // global state
	ptyMu sync.Mutex           //nolint:gochecknoglobals // global lock
)

const consoleResizeSleep = 100 * time.Millisecond

// CreateConsole - POST /console.
func CreateConsole(w http.ResponseWriter, r *http.Request) {
	if !o.minimegaConsole {
		plog.Error(plog.TypeSystem, "request made for minimega console, but console not enabled")
		http.Error(w, "'minimega-console' CLI arg not enabled", http.StatusMethodNotAllowed)

		return
	}

	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)
	if !role.Allowed("miniconsole", "post") {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"creating miniconsole not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	// create a new console
	phenix, err := os.Executable()
	if err != nil {
		plog.Error(plog.TypeSystem, "unable to get full path to phenix")
		http.Error(w, "", http.StatusInternalServerError)

		return
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
		http.Error(
			w,
			fmt.Sprintf("could not start terminal: %v", err),
			http.StatusInternalServerError,
		)

		return
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

	user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
	plog.Info(
		plog.TypeAction,
		"miniconsole created",
		"user",
		user,
	)
	_, _ = w.Write(body) //nolint:gosec // XSS via taint analysis
}

// ResizeConsole - POST /console/{pid}/size?cols={[0-9]+}&rows={[0-9]+}.
func ResizeConsole(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("miniconsole", "post") {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"resizing miniconsole not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	vars := mux.Vars(r)

	pid, err := strconv.Atoi(vars["pid"])
	if err != nil {
		http.Error(w, "invalid pid", http.StatusBadRequest)

		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)

		return
	}

	ptyMu.Lock()
	tty, ok := ptys[pid]
	ptyMu.Unlock()

	if !ok {
		http.Error(w, "pty not found", http.StatusNotFound)

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
		http.Error(w, "set winsize failed", http.StatusInternalServerError)
	}

	// make sure winsize gets processed, hopefully the user isn't typing...
	time.Sleep(consoleResizeSleep)

	_, _ = io.WriteString(tty, "\n")
}

// WsConsole - GET /console/{pid}/ws.
func WsConsole(w http.ResponseWriter, r *http.Request) {
	role, _ := r.Context().Value(middleware.ContextKeyRole).(rbac.Role)

	if !role.Allowed("miniconsole", "get") {
		user, _ := r.Context().Value(middleware.ContextKeyUser).(string)
		plog.Warn(
			plog.TypeSecurity,
			"getting miniconsole not allowed",
			user,
		)
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	vars := mux.Vars(r)

	pid, err := strconv.Atoi(vars["pid"])
	if err != nil {
		http.Error(w, "invalid pid", http.StatusBadRequest)

		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)

		return
	}

	ptyMu.Lock()
	tty, ok := ptys[pid]
	ptyMu.Unlock()

	if !ok {
		http.Error(w, "pty not found", http.StatusNotFound)

		return
	}

	websocket.Handler(func(ws *websocket.Conn) {
		defer func() { _ = tty.Close() }()

		proc, err := os.FindProcess(pid)
		if err != nil {
			plog.Warn(plog.TypeSystem, "unable to find process", "pid", pid)

			return
		}

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
}
