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

func terminal(ctx context.Context, shell string, envs ...string) error {
	printer := color.New(color.FgGreen)

	printer.Printf("Breakpoint: returning control to shell...\n\n")

	cmd := exec.CommandContext(ctx, shell)
	cmd.Env = append(cmd.Env, envs...)

	tty, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("starting pty for %s: %w", shell, err)
	}

	defer tty.Close()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)

	go func() {
		for range ch {
			if err := pty.InheritSize(os.Stdin, tty); err != nil {
				// TODO
				// log.Printf("error resizing pty: %s", err)
			}
		}
	}()

	ch <- syscall.SIGWINCH // initial resize of tty
	defer func() { signal.Stop(ch); close(ch) }()

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("putting STDIN into raw mode: %w", err)
	}

	defer term.Restore(int(os.Stdin.Fd()), old)

	go io.Copy(tty, os.Stdin)
	io.Copy(os.Stdout, tty)

	return nil
}
