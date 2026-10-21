package scorch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"

	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
)

type WebTerm struct {
	ExecutionID string `json:"executionID"`
	Exp         string `json:"exp"`
	Run         int    `json:"run"`
	Loop        int    `json:"loop"`
	Stage       string `json:"stage"`
	Name        string `json:"name"`
	Loc         string `json:"loc"`
	Exit        string `json:"exit"`
	RO          bool   `json:"readOnly"`

	// exposed for use in web package
	Pid  int           `json:"-"`
	Pty  *os.File      `json:"-"`
	Done chan struct{} `json:"-"`

	clientID string
	key      string
	exited   *sync.Once
}

func newWebTerm(exp string, run, loop int, stage, name string) WebTerm {
	return WebTerm{ //nolint:exhaustruct // partial initialization
		Exp:    exp,
		Run:    run,
		Loop:   loop,
		Stage:  stage,
		Name:   name,
		Done:   make(chan struct{}),
		exited: new(sync.Once),

		key: fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, name),
	}
}

var (
	webTermMu   sync.Mutex                 //nolint:gochecknoglobals // global lock
	webTermsPid = make(map[int]WebTerm)    //nolint:gochecknoglobals // global state
	webTermsExp = make(map[string]WebTerm) //nolint:gochecknoglobals // global state
)

var ErrTerminalNotFound = errors.New("web terminal not found")

func CreateWebTerminal(
	ctx context.Context,
	executionID string,
	exp string,
	run, loop int,
	stage, name, dir, cmd string,
	args []string,
	envs ...string,
) (chan struct{}, error) {
	term := newWebTerm(exp, run, loop, stage, name)
	term.ExecutionID = executionID

	c := exec.CommandContext(ctx, cmd, args...)
	c.Env = append(os.Environ(), envs...)
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.Dir = dir

	tty, err := pty.Start(c)
	if err != nil {
		return nil, fmt.Errorf("%s terminal failed: %w", cmd, err)
	}

	term.Pty = tty
	term.Pid = c.Process.Pid

	plog.Info(plog.TypeSystem, "spawned new terminal", "cmd", cmd, "pid", term.Pid)

	webTermMu.Lock()
	webTermsPid[term.Pid] = term
	webTermsExp[term.key] = term
	webTermMu.Unlock()

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			buf := make([]byte, TerminalBufferSize)
			n, err := tty.Read(buf)
			if n > 0 {
				publishTerminalOutput(term, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { _ = c.Wait(); _ = syscall.Kill(-term.Pid, syscall.SIGKILL); <-readerDone; finishTerminal(term) }()

	// Monitor for the provided context being canceled and kill the terminal
	// accordingly.
	go func() {
		select {
		case <-ctx.Done():
			_ = KillTerminal(term)
		case <-term.Done:
		}
	}()

	body, _ := json.Marshal(term)

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", exp),
		bt.NewResource("apps/scorch", exp, "terminal-create"),
		body,
	)

	return term.Done, nil
}

func finishTerminal(term WebTerm) {
	term.exited.Do(func() {
		_ = term.Pty.Close()
		webTermMu.Lock()
		delete(webTermsPid, term.Pid)
		if current, ok := webTermsExp[term.key]; ok && current.Pid == term.Pid {
			delete(webTermsExp, term.key)
		}
		webTermMu.Unlock()
		body, _ := json.Marshal(term)
		broker.Broadcast(
			bt.NewRequestPolicy("experiments", "get", term.Exp),
			bt.NewResource("apps/scorch", term.Exp, "terminal-exit"),
			body,
		)
		mu.Lock()
		delete(history, term.Pid)
		delete(rwTerm, term.Pid)
		delete(rwTermOwners, term.Pid)
		mu.Unlock()
		close(term.Done)
	})
}

func KillTerminal(term WebTerm) error {
	// A PTY child leads its own session; kill its process group, then let the
	// single waiter reap it before releasing the breakpoint.
	_ = syscall.Kill(-term.Pid, syscall.SIGKILL)
	<-term.Done
	return nil
}

func GetTerminalByPID(pid int) (WebTerm, error) {
	webTermMu.Lock()
	defer webTermMu.Unlock()

	term, ok := webTermsPid[pid]
	if !ok {
		return WebTerm{}, ErrTerminalNotFound
	}

	return term, nil
}

func GetTerminalByExperiment(key string) (WebTerm, error) {
	webTermMu.Lock()
	defer webTermMu.Unlock()

	term, ok := webTermsExp[key]
	if !ok {
		return WebTerm{}, ErrTerminalNotFound
	}

	return term, nil
}

func GetExperimentTerminals(exp string, run int) ([]WebTerm, error) {
	webTermMu.Lock()
	defer webTermMu.Unlock()

	var terms []WebTerm

	if exp == "" {
		for _, term := range webTermsExp {
			terms = append(terms, term)
		}
	} else {
		exps := strings.SplitSeq(exp, ",")

		for exp := range exps {
			for _, term := range webTermsExp {
				if term.Exp == exp && (run < 0 || term.Run == run) {
					terms = append(terms, term)
				}
			}
		}
	}

	return terms, nil
}
