package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"phenix/api/runtimesettings"
)

// Annotations that mark a deprecated flag, command or argument form. The test
// of docs/content/deprecations.md finds each deprecated item through them.
const (
	annotationDeprecated     = "phenix_deprecated"      // the replacement of a flag or command
	annotationDeprecatedArgs = "phenix_deprecated_args" // "<old form>\t<new form>" of the arguments of a command
)

// warnDeprecated writes the one deprecation warning of phenix to w: old is
// deprecated, and replacement is what to use instead. "warning:" is yellow
// when color is on.
func warnDeprecated(w io.Writer, old, replacement string) {
	label := outputPalette(w).Yellow("warning:")

	_, _ = fmt.Fprintf(
		w, "phenix: %s %s is deprecated and will be removed in a future release; use %s instead.\n",
		label, old, replacement,
	)
}

// deprecateFlag marks the flag name of cmd deprecated: the help and the CLI
// reference do not show it, and each use of it writes the deprecation
// warning to stderr. replacement is the flag to use instead, such as
// "-o/--output". Call it after the flag is added. It panics when cmd has no
// such flag.
func deprecateFlag(cmd *cobra.Command, name, replacement string) {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		panic(fmt.Sprintf("phenix %s: no flag --%s to deprecate", cmd.Name(), name))
	}

	f.Hidden = true

	if f.Annotations == nil {
		f.Annotations = map[string][]string{}
	}

	f.Annotations[annotationDeprecated] = []string{replacement}

	f.Value = &deprecatedValue{
		Value: f.Value,
		warn: func() {
			warnDeprecated(cmd.ErrOrStderr(), cmd.CommandPath()+" "+flagLabel(f), cmd.CommandPath()+" "+replacement)
		},
	}
}

// deprecateCommand marks cmd deprecated: the help and the CLI reference do
// not show it, and it writes the deprecation warning to stderr before it
// runs. replacement is the command line to use instead. Call it after RunE
// is set.
func deprecateCommand(cmd *cobra.Command, replacement string) {
	cmd.Hidden = true

	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}

	cmd.Annotations[annotationDeprecated] = replacement

	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		warnDeprecated(c.ErrOrStderr(), deprecatedCommandForm(c), replacement)

		return run(c, args)
	}
}

// deprecatedArgs is a deprecated form of the arguments of a command, which
// the command still takes. Make it with [deprecateArgs], and call
// [deprecatedArgs.warn] when the command gets that form.
type deprecatedArgs struct {
	cmd         *cobra.Command
	old         string
	replacement string
}

// deprecateArgs records that cmd still takes the deprecated argument form old,
// such as "<lifecycle> <experiment name>", and that replacement is the form to
// use instead.
func deprecateArgs(cmd *cobra.Command, old, replacement string) deprecatedArgs {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}

	line := old + "\t" + replacement
	if prev := cmd.Annotations[annotationDeprecatedArgs]; prev != "" {
		line = prev + "\n" + line
	}

	cmd.Annotations[annotationDeprecatedArgs] = line

	return deprecatedArgs{cmd: cmd, old: old, replacement: replacement}
}

// warn writes the deprecation warning of the argument form to stderr. use is
// the command line to use instead, made from the arguments given; with "",
// the warning names the replacement form.
func (d deprecatedArgs) warn(use string) {
	if use == "" {
		use = d.cmd.CommandPath() + " " + d.replacement
	}

	warnDeprecated(d.cmd.ErrOrStderr(), d.cmd.CommandPath()+" "+d.old, use)
}

// deprecatedCommandForm returns the command line of cmd as its Use shows it,
// such as "phenix experiment trigger-running <experiment name>".
func deprecatedCommandForm(cmd *cobra.Command) string {
	form := cmd.CommandPath()

	if _, args, ok := strings.Cut(cmd.Use, " "); ok {
		form += " " + args
	}

	return form
}

// flagLabel returns the names of f as the deprecation warning shows them, such
// as "-f/--format".
func flagLabel(f *pflag.Flag) string {
	if f.Shorthand != "" {
		return "-" + f.Shorthand + "/--" + f.Name
	}

	return "--" + f.Name
}

// deprecatedValue is the value of a deprecated flag, which warns at each use.
type deprecatedValue struct {
	pflag.Value

	warn func()
}

// Set sets the flag, and then writes the deprecation warning.
func (v *deprecatedValue) Set(s string) error {
	if err := v.Value.Set(s); err != nil {
		return err //nolint:wrapcheck // the value's own error
	}

	v.warn()

	return nil
}

// IsBoolFlag reports whether the flag takes no value, as the wrapped value does.
func (v *deprecatedValue) IsBoolFlag() bool {
	b, ok := v.Value.(interface{ IsBoolFlag() bool })

	return ok && b.IsBoolFlag()
}

// deprecatedSetting is a settings key that phenix still reads, from the
// config file and from its PHENIX_ environment variable, and the key to use
// instead.
type deprecatedSetting struct {
	key         string
	replacement string
}

// deprecatedSettings returns the deprecated settings keys that phenix still
// reads. docs/content/deprecations.md lists each of them.
func deprecatedSettings() []deprecatedSetting {
	return []deprecatedSetting{
		{key: legacyKeyUnixSocketGID, replacement: keyUnixSocketGID},
	}
}

// warnDeprecatedSetting writes the deprecation warning of the deprecated
// settings key to w, with its environment variable and its replacement. It
// writes nothing for a key that is not deprecated.
func warnDeprecatedSetting(w io.Writer, key string) {
	for _, s := range deprecatedSettings() {
		if s.key == key {
			warnDeprecated(
				w,
				"the setting "+s.key+" ("+runtimesettings.EnvVar(s.key)+")",
				s.replacement+" ("+runtimesettings.EnvVar(s.replacement)+")",
			)
		}
	}
}
