package scorch

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"github.com/fatih/color"
	"golang.org/x/term"
)

func terminal(ctx context.Context, dir, cmd string, args []string, envs ...string) error {
	printer := color.New(color.FgGreen)

	_, _ = printer.Printf("Breakpoint: returning control to shell...\n\n")

	c := exec.CommandContext(ctx, cmd, args...)
	c.Env = append(c.Env, envs...)
	c.Dir = dir

	tty, err := pty.Start(c)
	if err != nil {
		return fmt.Errorf("starting pty for %s: %w", cmd, err)
	}

	defer func() { _ = tty.Close() }()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)

	go func() {
		for range ch {
			err = pty.InheritSize(os.Stdin, tty)
			if err != nil {
				//nolint:godox // TODO
				// TODO
				_ = err
			}
		}
	}()

	ch <- syscall.SIGWINCH // initial resize of tty

	defer func() { signal.Stop(ch); close(ch) }()

	old, err := term.MakeRaw(int(os.Stdin.Fd())) //nolint:gosec // integer overflow conversion uintptr -> int
	if err != nil {
		return fmt.Errorf("putting STDIN into raw mode: %w", err)
	}

	defer func() { _ = term.Restore(int(os.Stdin.Fd()), old) }() //nolint:gosec // integer overflow conversion uintptr -> int

	go func() { _, _ = io.Copy(tty, os.Stdin) }()

	_, _ = io.Copy(os.Stdout, tty)

	return nil
}
