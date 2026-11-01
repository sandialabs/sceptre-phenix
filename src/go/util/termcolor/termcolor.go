// Package termcolor decides whether phenix colors an output, from the --color
// mode, the NO_COLOR and TERM variables and whether the output is a terminal,
// and colors text with ANSI escape codes.
package termcolor

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
)

// Mode is a value of the --color setting.
type Mode string

const (
	// Auto colors an output only when it is a terminal, NO_COLOR is empty and TERM is not dumb.
	Auto Mode = "auto"
	// On colors every output.
	On Mode = "on"
	// Off colors no output.
	Off Mode = "off"
)

// ErrUnknownMode is wrapped by the error ParseMode returns.
var ErrUnknownMode = errors.New("unknown color mode")

// ParseMode returns the Mode whose value is s. An empty s is Auto.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case "":
		return Auto, nil
	case Auto, On, Off:
		return m, nil
	}

	return Auto, fmt.Errorf("%w: %s", ErrUnknownMode, s)
}

// Enabled reports whether output to w gets colors in mode m. getenv reads
// NO_COLOR and TERM, which only Auto obeys.
func Enabled(m Mode, w io.Writer, getenv func(string) string) bool {
	switch m {
	case On:
		return true
	case Off:
		return false
	case Auto:
	}

	if getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
		return false
	}

	return IsTerminal(w)
}

// IsTerminal reports whether w is a file that is a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}

	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

// Palette colors text for one output, or returns the text unchanged when it is off.
type Palette struct {
	on bool
}

// New returns a Palette that colors text when on is true.
func New(on bool) Palette {
	return Palette{on: on}
}

// For returns the Palette of output to w in mode m, with the variables of this process.
func For(m Mode, w io.Writer) Palette {
	return New(Enabled(m, w, os.Getenv))
}

// On reports whether p colors text.
func (p Palette) On() bool {
	return p.on
}

// Green colors s green.
func (p Palette) Green(s string) string { return p.paint(s, color.FgGreen) }

// Yellow colors s yellow.
func (p Palette) Yellow(s string) string { return p.paint(s, color.FgYellow) }

// Red colors s red.
func (p Palette) Red(s string) string { return p.paint(s, color.FgRed) }

// BoldRed colors s bold red.
func (p Palette) BoldRed(s string) string { return p.paint(s, color.Bold, color.FgRed) }

// Cyan colors s cyan.
func (p Palette) Cyan(s string) string { return p.paint(s, color.FgCyan) }

// Faint makes s faint.
func (p Palette) Faint(s string) string { return p.paint(s, color.Faint) }

func (p Palette) paint(s string, attrs ...color.Attribute) string {
	if !p.on || s == "" {
		return s
	}

	c := color.New(attrs...)
	c.EnableColor()

	return c.Sprint(s)
}
