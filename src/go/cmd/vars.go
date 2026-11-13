package cmd

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"phenix/util/envsubst"
	"phenix/util/plog"
)

const (
	envFlagName     = "env"
	envFileFlagName = "env-file"

	// composeDefaultWarning is logged for a default such as that of ${N:-3}.
	composeDefaultWarning = "a default starts with - or ?; " +
		"phenix has no Docker Compose syntax, so the default is the text as written"
)

// errVariables is wrapped by the error of [substituteVars] when a variable of
// the files has a problem.
var errVariables = errors.New("variable problems")

// addVarFlags adds -e/--env and --env-file to cmd. They set the variables
// that fill in the ${NAME} placeholders of the config files that cmd reads.
func addVarFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayP(
		envFlagName, "e", nil,
		"set a variable for the ${NAME} placeholders: NAME=value, or NAME to copy the value from the shell (repeatable)",
	)
	cmd.Flags().StringArray(
		envFileFlagName, nil,
		"read variables from an env file of NAME=value lines; a relative path starts at the working directory (repeatable)",
	)

	_ = cmd.MarkFlagFilename(envFileFlagName)
}

// varOptionsFromFlags returns the variable sources that the flags of cmd
// give, with the environment of the process as the shell.
func varOptionsFromFlags(cmd *cobra.Command) envsubst.Options {
	return envsubst.Options{
		Flags:          mustGet(cmd.Flags().GetStringArray, envFlagName),
		EnvFiles:       mustGet(cmd.Flags().GetStringArray, envFileFlagName),
		DefaultEnvFile: "",
		Environ:        os.Environ(),
		Reserved:       "",
	}
}

// newVarSet reads the variable sources of o and logs each env file that it
// read. attrs are log fields, as key and value pairs, for every record.
func newVarSet(o envsubst.Options, attrs ...any) (*envsubst.Set, error) {
	set, err := envsubst.NewSet(o)
	if err != nil {
		if errors.Is(err, envsubst.ErrReserved) {
			return nil, fmt.Errorf("%w cannot be set as a variable; set the branch name with -b", err)
		}

		return nil, fmt.Errorf("reading the variables: %w", err)
	}

	for _, file := range set.EnvFiles() {
		plog.Info(plog.TypeSystem, "env file read", slices.Concat(attrs, []any{"file", file.Path, "variables", file.Count})...)
	}

	return set, nil
}

// substituteVars fills in the variables of files in one pass. It logs the
// names and sources of the variables that the files use, their values at the
// debug level only, and each warning. When a variable has a problem, it logs
// every problem and changes no file: the error wraps [errVariables].
func substituteVars(set *envsubst.Set, files []envsubst.File, attrs ...any) ([]envsubst.File, error) {
	res, err := set.Substitute(files)
	if err != nil {
		var subErr *envsubst.Error
		if !errors.As(err, &subErr) {
			return nil, fmt.Errorf("filling in the variables: %w", err)
		}

		for _, problem := range subErr.Problems {
			logVarProblem(problem, attrs)
		}

		return nil, fmt.Errorf(
			"%d %w; nothing was changed: set a variable with -e NAME=value or an env file, "+
				"give it a default with ${NAME:default}, or write $${NAME} to keep the text",
			len(subErr.Problems), errVariables,
		)
	}

	for _, warning := range res.Warnings {
		at := []any{"file", warning.Path, "line", warning.Line, "variable", warning.Name, "default", warning.Default}

		plog.Warn(plog.TypeSystem, composeDefaultWarning, slices.Concat(attrs, at)...)
	}

	if len(res.Used) == 0 {
		return res.Files, nil
	}

	names := make([]string, 0, len(res.Used))

	for _, used := range res.Used {
		names = append(names, used.Name+" ("+used.Source.String()+")")

		plog.Debug(
			plog.TypeSystem, "variable value",
			slices.Concat(attrs, []any{"variable", used.Name, "source", used.Source.String(), "value", used.Value})...,
		)
	}

	plog.Info(plog.TypeSystem, "variables filled in", slices.Concat(attrs, []any{"variables", strings.Join(names, ", ")})...)

	return res.Files, nil
}

// logVarProblem logs one problem of a variable as an error record.
func logVarProblem(problem envsubst.Problem, attrs []any) {
	at := []any{"variable", problem.Name, "file", problem.Path, "line", problem.Line}

	switch problem.Kind {
	case envsubst.Undefined:
		if problem.Filtered {
			at = append(at, "hint", "the shell has it, but "+envsubst.ShellFilterPrefix+" variables are not used; pass -e "+problem.Name)
		}

		plog.Error(plog.TypeSystem, "variable is not set and has no default", slices.Concat(attrs, at)...)
	case envsubst.ValueLineBreak:
		at = append(at, "source", problem.Source.String())

		plog.Error(plog.TypeSystem, "variable value holds a line break", slices.Concat(attrs, at)...)
	case envsubst.DefaultLineBreak:
		plog.Error(plog.TypeSystem, "variable default holds a line break", slices.Concat(attrs, at)...)
	case envsubst.UnusedFlag:
		plog.Error(plog.TypeSystem, "no file uses the variable that -e sets", slices.Concat(attrs, []any{"variable", problem.Name})...)
	}
}
