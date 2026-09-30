package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/activeshadow/libminimega/miniclient"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/util"
	"phenix/util/common"
	"phenix/util/plog"
)

// minimegaSuggestTimeout bounds how long shell completion waits for minimega,
// so a hung minimega cannot hang the shell.
const minimegaSuggestTimeout = 2 * time.Second

var errMinimegaSuggestTimeout = errors.New("timed out waiting for minimega suggestions")

type noPager struct{}

func (noPager) Page(output string) {
	if output == "" {
		return
	}

	fmt.Fprintln(os.Stdout, output)
}

func newMMCmd() *cobra.Command {
	desc := `Send commands, or attach, to minimega

  Sends a command to minimega and prints the response, or attaches to the
  interactive minimega console. Exits non-zero if minimega reports an error.

  Flags must come before the minimega command. Everything after the first
  minimega argument, including arguments that start with "-", is passed to
  minimega unchanged, and arguments are quoted for minimega as needed. A
  single argument is sent as-is, so a whole command written in minimega's own
  syntax can be passed as one quoted string.`

	example := `
  phenix mm vm info
  phenix mm -n <experiment name> vm info
  phenix mm -n <experiment name> cc exec ls -a
  phenix mm -n <experiment name> "vm info"
  phenix minimega -a -n <experiment name>`

	cmd := &cobra.Command{
		Use:               "mm <minimega args>...",
		Aliases:           []string{"minimega"},
		Short:             "Send commands, or attach, to minimega",
		Long:              desc,
		Example:           example,
		ValidArgsFunction: mmArgsCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				attach    = MustGetBool(cmd.Flags(), "attach")
				namespace = MustGetString(cmd.Flags(), "namespace")
			)

			if !attach && len(args) == 0 {
				return cmd.Help()
			}

			mm, err := miniclient.Dial(common.MinimegaBase)
			if err != nil {
				return util.HumanizeError(
					err,
					"Unable to connect to minimega at %s",
					filepath.Join(common.MinimegaBase, "minimega"),
				).Humanized()
			}

			defer func() { _ = mm.Close() }()

			mm.Pager = new(noPager)

			if attach {
				mm.Attach(namespace)

				// Attach returns without an error when the user disconnects, so
				// any error here means the connection to minimega was lost.
				if err := mm.Error(); err != nil {
					plog.Error(plog.TypeMinimega, "lost connection to minimega console", "error", err)

					return fmt.Errorf("lost connection to minimega: %w", err)
				}

				return nil
			}

			return runMinimegaCommand(mm, minimegaCommand(namespace, args), cmd.OutOrStdout())
		},
	}

	// Stop parsing phenix flags at the first minimega argument, so flags meant
	// for minimega (for example, the -a in `cc exec ls -a`) are passed through
	// instead of being parsed as the phenix -a and -n shorthands.
	cmd.Flags().SetInterspersed(false)

	cmd.Flags().BoolP("attach", "a", false, "Attach to minimega console instead of sending commands")
	cmd.Flags().StringP("namespace", "n", "", "Default minimega namespace to use")

	_ = cmd.RegisterFlagCompletionFunc("namespace", mmNamespaceCompletion)

	return cmd
}

// runMinimegaCommand sends command to minimega and writes the rendered
// response to out. Errors minimega reports, and a lost connection, are logged
// and returned.
func runMinimegaCommand(mm *miniclient.Conn, command string, out io.Writer) error {
	var errs []error

	for resp := range mm.Run(command) {
		if resp.Rendered != "" {
			fmt.Fprintln(out, resp.Rendered)
		}

		for _, r := range resp.Resp {
			if r.Error == "" {
				continue
			}

			plog.Error(
				plog.TypeMinimega,
				"minimega command failed",
				"command", command,
				"host", r.Host,
				"error", r.Error,
			)

			errs = append(errs, fmt.Errorf("%s: %s", r.Host, r.Error))
		}
	}

	if err := mm.Error(); err != nil {
		plog.Error(plog.TypeMinimega, "lost connection to minimega", "command", command, "error", err)

		errs = append(errs, fmt.Errorf("lost connection to minimega: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("running minimega command %q: %w", command, errors.Join(errs...))
	}

	return nil
}

// minimegaCommand builds the command line sent to minimega. A single argument
// is sent as-is, so a whole command can be passed as one string in minimega's
// own syntax; multiple arguments are quoted as needed and joined.
func minimegaCommand(namespace string, args []string) string {
	var command string

	if len(args) == 1 {
		command = args[0]
	} else {
		command = quoteMinimegaArgs(args)
	}

	if namespace != "" {
		command = "namespace " + quoteMinimegaArg(namespace) + " " + command
	}

	return command
}

func quoteMinimegaArgs(args []string) string {
	quoted := make([]string, len(args))

	for i, arg := range args {
		quoted[i] = quoteMinimegaArg(arg)
	}

	return strings.Join(quoted, " ")
}

// quoteMinimegaArg quotes arg so that minimega's command lexer reads it back as
// one unchanged argument. Outside quotes the lexer splits on whitespace,
// starts a quoted string at `"` or `'`, treats `\` as an escape, and drops
// everything from `#` on as a comment. Inside double quotes only `"` and `\`
// are special. An empty argument must be quoted or the lexer drops it.
func quoteMinimegaArg(arg string) string {
	if arg != "" && !strings.ContainsFunc(arg, needsMinimegaQuote) {
		return arg
	}

	var b strings.Builder

	b.WriteByte('"')

	for _, r := range arg {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}

	b.WriteByte('"')

	return b.String()
}

func needsMinimegaQuote(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`"'\#`, r)
}

// mmArgsCompletion completes minimega commands using suggestions from the
// running minimega, the same suggestions its interactive console offers.
// minimega makes suggestions for its active namespace, not the --namespace
// flag.
func mmArgsCompletion(
	cmd *cobra.Command,
	args []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if MustGetBool(cmd.Flags(), "attach") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// The root command's PersistentPreRunE runs for the completion request,
	// not for the command line being completed, so common.MinimegaBase misses
	// a --base-dir.minimega typed on that line. Read it from the parsed flags.
	base := getEffectiveString("base-dir.minimega", cmd.Flags().Changed("base-dir.minimega"))

	line := toComplete
	if len(args) > 0 {
		line = quoteMinimegaArgs(args) + " " + toComplete
	}

	suggestions, err := suggestMinimega(base, line)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	return minimegaCompletions(line, suggestions)
}

// suggestMinimega asks the minimega listening under base for suggestions to
// complete line, giving up after minimegaSuggestTimeout.
func suggestMinimega(base, line string) ([]string, error) {
	// minimega rejects an empty suggestion request, but suggests its top-level
	// commands for a blank line.
	if line == "" {
		line = " "
	}

	type result struct {
		suggestions []string
		err         error
	}

	done := make(chan result, 1)

	go func() {
		mm, err := miniclient.Dial(base)
		if err != nil {
			done <- result{suggestions: nil, err: err}

			return
		}

		defer func() { _ = mm.Close() }()

		suggestions := mm.Suggest(line)
		done <- result{suggestions: suggestions, err: mm.Error()}
	}()

	select {
	case r := <-done:
		return r.suggestions, r.err
	case <-time.After(minimegaSuggestTimeout):
		return nil, errMinimegaSuggestTimeout
	}
}

// minimegaCompletions converts minimega's suggestions into completions for the
// last word of line. Each suggestion is a whole command line: line with its
// last word replaced by the suggested word and, unless the word is a
// directory, a trailing space.
func minimegaCompletions(line string, suggestions []string) ([]string, cobra.ShellCompDirective) {
	var (
		prefix      = strings.TrimRightFunc(line, func(r rune) bool { return !unicode.IsSpace(r) })
		directive   = cobra.ShellCompDirectiveNoFileComp
		completions []string
	)

	for _, suggestion := range suggestions {
		word, ok := strings.CutPrefix(suggestion, prefix)
		if !ok {
			continue
		}

		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}

		if strings.HasSuffix(word, "/") {
			directive |= cobra.ShellCompDirectiveNoSpace
		}

		completions = append(completions, word)
	}

	return completions, directive
}

// mmNamespaceCompletion completes --namespace with the names of running
// experiments, which are the experiments that have a minimega namespace.
// Naming any other namespace makes minimega create it.
func mmNamespaceCompletion(
	_ *cobra.Command,
	_ []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	_ = store.Init(store.Endpoint(viper.GetString("store.endpoint")))

	exps, err := experiment.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	var names []string

	for _, exp := range exps {
		if exp.Running() && strings.HasPrefix(exp.Metadata.Name, toComplete) {
			names = append(names, exp.Metadata.Name)
		}
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newMMCmd(), true)
}
