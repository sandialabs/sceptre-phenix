package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Output formats of -o/--output, beside FormatJSON and FormatYAML.
const (
	formatTable    = "table"
	formatMarkdown = "markdown"
	formatCSV      = "csv"
	outputFlagName = "output"
	flagTypeBool   = "bool" // the pflag type of a bool flag
)

var (
	// errOutputFormat is the error for an output format that the command does not make.
	errOutputFormat = errors.New("unknown output format")

	// errOutputConflict is the error for two output flags that ask for different formats.
	errOutputConflict = errors.New("output flags ask for different formats")
)

// outputView writes the data of a command in one output format, such as a table.
type outputView func(w io.Writer) error

// outputViews holds the views of a command by output format. A command gives
// a view for each format that is not json or yaml. A view for json or yaml
// replaces the default encoder of that format.
type outputViews map[string]outputView

// outputFlag is the -o/--output flag of a command, with its alias flags.
//
// A command adds it with [addOutputFlag], and writes its data with
// [outputFlag.print] from RunE. The flag refuses a format that the command
// does not make when cobra parses it, so the command does no work for a bad
// value. JSON and YAML come from the data; the other formats come from views.
type outputFlag struct {
	def     string
	formats []string
	given   map[string]string // the format that each output flag on the command line asks for
}

// outputOption adds an alias flag to an output flag.
type outputOption func(cmd *cobra.Command, o *outputFlag)

// addOutputFlag adds -o/--output to cmd, which takes one of formats and
// has the default def, and completes the formats. It panics when def is not
// one of formats.
func addOutputFlag(cmd *cobra.Command, def string, formats []string, opts ...outputOption) *outputFlag {
	if !slices.Contains(formats, def) {
		panic(fmt.Sprintf("phenix %s: default output format %q is not in %q", cmd.Name(), def, formats))
	}

	o := &outputFlag{def: def, formats: slices.Clone(formats), given: map[string]string{}}

	cmd.Flags().VarP(
		&outputValue{o: o, name: outputFlagName},
		outputFlagName, "o", "Output format ("+strings.Join(formats, ", ")+")",
	)

	_ = cmd.RegisterFlagCompletionFunc(outputFlagName, completeValues(formats...))

	for _, opt := range opts {
		opt(cmd, o)
	}

	return o
}

// withJSONFlag adds --json, the same as -o json.
func withJSONFlag() outputOption {
	return func(cmd *cobra.Command, o *outputFlag) {
		o.addAlias(cmd, "json", "", FormatJSON, "Output as JSON (the same as -o "+FormatJSON+")")
	}
}

// withPrettyFlag adds -p/--pretty, the same as -o format.
func withPrettyFlag(format string) outputOption {
	return func(cmd *cobra.Command, o *outputFlag) {
		usage := "Print indented JSON (the same as -o " + format + ")"
		if format != FormatJSON {
			usage = "Print the table as " + format + " (the same as -o " + format + ")"
		}

		o.addAlias(cmd, "pretty", "p", format, usage)
	}
}

// withFormatFlag adds -f/--format, a deprecated alias of -o/--output.
func withFormatFlag() outputOption {
	return func(cmd *cobra.Command, o *outputFlag) {
		cmd.Flags().VarP(&outputValue{o: o, name: "format"}, "format", "f", "Output format")
		deprecateFlag(cmd, "format", "-o/--output")
	}
}

// format returns the output format that the command line asks for, or the default.
func (o *outputFlag) format() string {
	for _, format := range o.given {
		return format
	}

	return o.def
}

// print writes data to the command's output in the format that the command
// line asks for. JSON and YAML are never in color.
func (o *outputFlag) print(cmd *cobra.Command, data any, views outputViews) error {
	return renderOutput(cmd.OutOrStdout(), o.format(), data, views)
}

// addAlias adds the bool flag name, which asks for format.
func (o *outputFlag) addAlias(cmd *cobra.Command, name, shorthand, format, usage string) {
	if !slices.Contains(o.formats, format) {
		panic(fmt.Sprintf("phenix %s: --%s output format %q is not in %q", cmd.Name(), name, format, o.formats))
	}

	f := cmd.Flags().VarPF(&outputAliasValue{o: o, name: name, format: format}, name, shorthand, usage)
	f.NoOptDefVal = boolStringTrue
}

// set records that the flag name asks for format.
func (o *outputFlag) set(name, format string) error {
	if !slices.Contains(o.formats, format) {
		return fmt.Errorf("%w %q: use one of %s", errOutputFormat, format, strings.Join(o.formats, ", "))
	}

	for other, otherFormat := range o.given {
		if other != name && otherFormat != format {
			return fmt.Errorf("%w: --%s asks for %s and --%s asks for %s", errOutputConflict, other, otherFormat, name, format)
		}
	}

	o.given[name] = format

	return nil
}

// renderOutput writes data to w in format: JSON or YAML from data, or else
// the view of the format.
func renderOutput(w io.Writer, format string, data any, views outputViews) error {
	if view, ok := views[format]; ok {
		return view(w)
	}

	switch format {
	case FormatJSON:
		return writeJSONOutput(w, data)
	case FormatYAML:
		return writeYAMLOutput(w, data)
	}

	return fmt.Errorf("%w %q: the command has no view for it", errOutputFormat, format)
}

// writeJSONOutput writes v as JSON indented by two spaces, with a newline at the
// end. A nil slice is an empty list, not null.
func writeJSONOutput(w io.Writer, v any) error {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		v = []any{}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	return nil
}

// writeYAMLOutput writes v as YAML, as yaml.Marshal makes it. A nil slice is an empty list.
func writeYAMLOutput(w io.Writer, v any) error {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		v = []any{}
	}

	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("writing YAML: %w", err)
	}

	if _, err := w.Write(b); err != nil {
		return fmt.Errorf("writing YAML: %w", err)
	}

	return nil
}

// outputValue is the value of -o/--output, or of a deprecated alias of it.
type outputValue struct {
	o    *outputFlag
	name string
}

// String returns the format that the command line asks for.
func (v *outputValue) String() string { return v.o.format() }

// Set records the format that the flag asks for.
func (v *outputValue) Set(s string) error { return v.o.set(v.name, s) }

// Type returns the type that the help shows for the flag.
func (v *outputValue) Type() string { return "string" }

// outputAliasValue is the value of a bool flag that asks for one output format, such as --json.
type outputAliasValue struct {
	o      *outputFlag
	name   string
	format string
}

// String returns "true" when the flag asks for its format.
func (v *outputAliasValue) String() string {
	_, ok := v.o.given[v.name]

	return strconv.FormatBool(ok)
}

// Set records the flag's format for true, and removes it for false.
func (v *outputAliasValue) Set(s string) error {
	on, err := strconv.ParseBool(s)
	if err != nil {
		return fmt.Errorf("parsing %q as a bool: %w", s, err)
	}

	if !on {
		delete(v.o.given, v.name)

		return nil
	}

	return v.o.set(v.name, v.format)
}

// Type returns the type that the help shows for the flag.
func (v *outputAliasValue) Type() string { return flagTypeBool }

// IsBoolFlag reports that the flag takes no value.
func (v *outputAliasValue) IsBoolFlag() bool { return true }
