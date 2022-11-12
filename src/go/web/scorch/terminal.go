package scorch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"phenix/web/broker"

	log "github.com/activeshadow/libminimega/minilog"
	"github.com/creack/pty"
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
	return WebTerm{
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
	webTermMu   sync.Mutex
	webTermsPid = make(map[int]WebTerm)
	webTermsExp = make(map[string]WebTerm)
)

var ErrTerminalNotFound = fmt.Errorf("web terminal not found")

func CreateWebTerminal(ctx context.Context, exp string, run, loop int, stage, name, cmd string, args []string, envs ...string) (chan struct{}, error) {
	log.Debug("CreateTerminal function called")

	term := newWebTerm(exp, run, loop, stage, name)

	c := exec.CommandContext(ctx, cmd, args...)
	c.Env = append(c.Env, envs...)

	tty, err := pty.Start(c)
	if err != nil {
		return nil, fmt.Errorf("%s terminal failed: %w", cmd, err)
	}

	term.Pty = tty
	term.Pid = c.Process.Pid

	log.Info("spawned new %s terminal, pid = %v", cmd, term.Pid)

	webTermMu.Lock()
	webTermsPid[term.Pid] = term
	webTermsExp[term.key] = term
	webTermMu.Unlock()

	// Monitor for the provided context being canceled and kill the terminal
	// accordingly.
	go func() {
		select {
		case <-ctx.Done():
			KillTerminal(term)
		case <-term.Done:
		}
	}()

	body, _ := json.Marshal(term)

	broker.Broadcast(
		nil, // TODO
		broker.NewResource("apps/scorch", exp, "terminal-create"),
		body,
	)

	return term.Done, nil
}

func KillTerminal(term WebTerm) error {
	close(term.Done)

	// The calling function needs to be holding the webTermMu lock!
	delete(webTermsPid, term.Pid)
	delete(webTermsExp, term.key)

	broker.Broadcast(
		nil, // TODO
		broker.NewResource("apps/scorch", term.Exp, "terminal-exit"),
		nil,
	)

	defer term.Pty.Close()

	proc, err := os.FindProcess(term.Pid)
	if err != nil {
		return fmt.Errorf("cannot find process with PID %d", term.Pid)
	}

	proc.Kill()
	proc.Wait()

	log.Debug("process %d killed", term.Pid)

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
		exps := strings.Split(exp, ",")

		for _, exp := range exps {
			for _, term := range webTermsExp {
				if term.Exp == exp && (run < 0 || term.Run == run) {
					terms = append(terms, term)
				}
			}
		}
	}

	return terms, nil
}
