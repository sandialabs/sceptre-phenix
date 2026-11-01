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

	"github.com/creack/pty"

	"phenix/util/plog"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/lifecycle"
)

type WebTerm struct {
	Exp   string `json:"exp"`
	Run   int    `json:"run"`
	Loop  int    `json:"loop"`
	Stage string `json:"stage"`
	Name  string `json:"name"`
	Loc   string `json:"loc"`
	Exit  string `json:"exit"`
	RO    bool   `json:"readOnly"`

	// exposed for use in web package
	Pid  int           `json:"-"`
	Pty  *os.File      `json:"-"`
	Done chan struct{} `json:"-"`

	key string
}

func newWebTerm(exp string, run, loop int, stage, name string) WebTerm {
	return WebTerm{ //nolint:exhaustruct // partial initialization
		Exp:   exp,
		Run:   run,
		Loop:  loop,
		Stage: stage,
		Name:  name,
		Done:  make(chan struct{}),

		key: fmt.Sprintf("%s|%d|%d|%s|%s", exp, run, loop, stage, name),
	}
}

var (
	webTermMu   sync.Mutex                 //nolint:gochecknoglobals // global lock
	webTermsPid = make(map[int]WebTerm)    //nolint:gochecknoglobals // global state
	webTermsExp = make(map[string]WebTerm) //nolint:gochecknoglobals // global state
)

var (
	ErrTerminalNotFound = errors.New("web terminal not found")
	// ErrServerStopping is why no terminal starts once the server shuts down.
	ErrServerStopping = errors.New(lifecycle.Unavailable)
)

func CreateWebTerminal(
	ctx context.Context,
	exp string,
	run, loop int,
	stage, name, dir, cmd string,
	args []string,
	envs ...string,
) (chan struct{}, error) {
	if lifecycle.IsStopping() {
		return nil, ErrServerStopping
	}

	term := newWebTerm(exp, run, loop, stage, name)

	c := exec.CommandContext(ctx, cmd, args...)
	c.Env = append(c.Env, envs...)
	c.Dir = dir

	tty, err := pty.Start(c)
	if err != nil {
		return nil, fmt.Errorf("%s terminal failed: %w", cmd, err)
	}

	term.Pty = tty
	term.Pid = c.Process.Pid

	plog.Info(plog.TypeSystem, "spawned new terminal", "cmd", cmd, "pid", term.Pid)

	webTermMu.Lock()

	// checked again under the lock KillTerminals lists terminals under
	if lifecycle.IsStopping() {
		webTermMu.Unlock()

		_ = c.Process.Kill()
		_ = c.Wait()
		_ = tty.Close()

		return nil, ErrServerStopping
	}

	webTermsPid[term.Pid] = term
	webTermsExp[term.key] = term
	webTermMu.Unlock()

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

// KillTerminal ends the terminal's process. A terminal already ended is left
// alone, so its owner's context and an exit request may both end it.
func KillTerminal(term WebTerm) error {
	webTermMu.Lock()

	if known, ok := webTermsPid[term.Pid]; !ok || known.Done != term.Done {
		webTermMu.Unlock()

		return nil
	}

	close(term.Done)
	delete(webTermsPid, term.Pid)
	delete(webTermsExp, term.key)
	webTermMu.Unlock()

	broker.Broadcast(
		bt.NewRequestPolicy("experiments", "get", term.Exp),
		bt.NewResource("apps/scorch", term.Exp, "terminal-exit"),
		nil,
	)

	defer term.Pty.Close()

	proc, err := os.FindProcess(term.Pid)
	if err != nil {
		return fmt.Errorf("cannot find process with PID %d", term.Pid)
	}

	_ = proc.Kill()
	_, _ = proc.Wait()

	plog.Debug(plog.TypeSystem, "process killed", "pid", term.Pid)

	return nil
}

// KillTerminals ends every terminal's process, and waits for them until ctx is
// done. It returns how many had not ended by then.
func KillTerminals(ctx context.Context) int {
	webTermMu.Lock()
	terms := make([]WebTerm, 0, len(webTermsPid))

	for _, term := range webTermsPid {
		terms = append(terms, term)
	}
	webTermMu.Unlock()

	killed := make(chan struct{}, len(terms))

	for _, term := range terms {
		go func() {
			_ = KillTerminal(term)
			killed <- struct{}{}
		}()
	}

	for left := len(terms); left > 0; left-- {
		select {
		case <-killed:
		case <-ctx.Done():
			return left
		}
	}

	return 0
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
