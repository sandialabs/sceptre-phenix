package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/viper"

	"phenix/util/termcolor"
)

const (
	colorFlag    = "color"
	colorOptions = "options: auto | on | off"
)

// colorMode is the --color setting as phenix reads it: the flag, PHENIX_COLOR,
// the config file, then auto.
func colorMode() (termcolor.Mode, error) {
	mode, err := termcolor.ParseMode(viper.GetString(colorFlag))
	if err != nil {
		return termcolor.Auto, fmt.Errorf("%w (%s)", err, colorOptions)
	}

	return mode, nil
}

// outputPalette is the palette of output to w. A --color value that phenix
// refuses colors as auto does, so the error about it still prints.
func outputPalette(w io.Writer) termcolor.Palette {
	mode, _ := colorMode()

	return termcolor.For(mode, w)
}

// startupPalette is the palette of output to w before cobra reads the flags:
// a --color in args wins over the setting.
func startupPalette(w io.Writer, args []string) termcolor.Palette {
	if value, ok := colorFlagInArgs(args); ok {
		if mode, err := termcolor.ParseMode(value); err == nil {
			return termcolor.For(mode, w)
		}
	}

	return outputPalette(w)
}

// colorFlagInArgs finds the value of the last --color flag in args, given as
// --color=<mode> or --color <mode>, before a "--" that ends the flags.
func colorFlagInArgs(args []string) (string, bool) {
	var (
		value string
		found bool
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			return value, found
		case strings.HasPrefix(arg, "--"+colorFlag+"="):
			value, found = strings.TrimPrefix(arg, "--"+colorFlag+"="), true
		case arg == "--"+colorFlag && i+1 < len(args):
			i++
			value, found = args[i], true
		}
	}

	return value, found
}

// printCommandError writes err as cobra does, after "Error:" in red. It reads
// --color from args, because cobra does not parse the flags of an unknown command.
func printCommandError(w io.Writer, args []string, err error) {
	_, _ = fmt.Fprintln(w, startupPalette(w, args).Red("Error:"), err.Error()) //nolint:gosec // G705 prints to the terminal
}

// configWarningf writes a warning about the config files to w, which phenix
// writes before it sets up logging, with "warning:" in yellow.
func configWarningf(w io.Writer, format string, args ...any) {
	label := startupPalette(w, os.Args[1:]).Yellow("warning:")

	_, _ = fmt.Fprintf(w, "phenix: %s %s\n", label, fmt.Sprintf(format, args...)) //nolint:gosec // G705 prints to the terminal
}
