// Package minimega sends one minimega command, as phenix mm and the
// POST /minimega/commands route do, and collects each host's answer.
// minimega expands environment variables in every argument, even quoted ones.
package minimega

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/sandia-minimega/minimega/v2/pkg/miniclient"

	"phenix/util/plog"
)

var (
	// ErrCommandFailed is wrapped by the error of Run when minimega answered
	// with an error from one or more hosts.
	ErrCommandFailed = errors.New("minimega reported an error")

	// ErrConnectionLost is wrapped by the error of Run when the connection to
	// minimega broke before the whole answer arrived.
	ErrConnectionLost = errors.New("lost connection to minimega")
)

// hostError is an error that a minimega host answered. It wraps
// ErrCommandFailed.
type hostError struct {
	host, msg string
}

func (e *hostError) Error() string { return e.host + ": " + e.msg }

func (e *hostError) Unwrap() error { return ErrCommandFailed }

// HostResponse is the answer of one minimega host to a command: its text, or
// its table, and its error.
type HostResponse struct {
	Host     string     `json:"host"`
	Response string     `json:"response,omitempty"`
	Header   []string   `json:"header,omitempty"`
	Tabular  [][]string `json:"tabular,omitempty"`
	Error    string     `json:"error,omitempty"`
}

// Result is what minimega answered to a command.
type Result struct {
	// Output is the answer as minimega renders it, as phenix mm prints it.
	Output string `json:"output"`
	// Responses are the answers of each host, in the order they came.
	Responses []HostResponse `json:"responses"`
	// Errors are the errors of the hosts, as "<host>: <error>", and a lost
	// connection.
	Errors []string `json:"errors"`
}

// CommandLine builds the command line sent to minimega. A single argument is
// sent as-is, so a whole command can be passed as one string in minimega's own
// syntax; multiple arguments are quoted as needed and joined. A namespace runs
// the command in that minimega namespace.
func CommandLine(namespace string, args []string) string {
	var command string

	if len(args) == 1 {
		command = args[0]
	} else {
		command = QuoteArgs(args)
	}

	if namespace != "" {
		command = "namespace " + QuoteArg(namespace) + " " + command
	}

	return command
}

// QuoteArgs quotes each of args with QuoteArg and joins them with spaces.
func QuoteArgs(args []string) string {
	quoted := make([]string, len(args))

	for i, arg := range args {
		quoted[i] = QuoteArg(arg)
	}

	return strings.Join(quoted, " ")
}

// QuoteArg quotes arg so that minimega's command lexer reads it back as
// one unchanged argument. Outside quotes the lexer splits on whitespace,
// starts a quoted string at `"` or `'`, treats `\` as an escape, and drops
// everything from `#` on as a comment. Inside double quotes only `"` and `\`
// are special. An empty argument must be quoted or the lexer drops it.
func QuoteArg(arg string) string {
	if arg != "" && !strings.ContainsFunc(arg, needsQuote) {
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

func needsQuote(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`"'\#`, r)
}

// Run sends command to minimega on mm and returns its answer. It calls
// rendered, when it is not nil, with the rendered text of each response as it
// arrives. Errors that minimega reports, and a lost connection, are logged.
// The error wraps ErrCommandFailed for errors that minimega reported and
// ErrConnectionLost for a lost connection; Result holds what came.
func Run(mm *miniclient.Conn, command string, rendered func(string)) (Result, error) {
	var (
		result = Result{Output: "", Responses: []HostResponse{}, Errors: []string{}}
		output []string
		errs   []error
	)

	for resp := range mm.Run(command) {
		if resp.Rendered != "" {
			output = append(output, resp.Rendered)

			if rendered != nil {
				rendered(resp.Rendered)
			}
		}

		for _, r := range resp.Resp {
			result.Responses = append(result.Responses, HostResponse{
				Host: r.Host, Response: r.Response, Header: r.Header, Tabular: r.Tabular, Error: r.Error,
			})

			if r.Error == "" {
				continue
			}

			plog.Error(plog.TypeMinimega, "minimega command failed", "command", command, "host", r.Host, "error", r.Error)

			herr := &hostError{host: r.Host, msg: r.Error}

			errs = append(errs, herr)
			result.Errors = append(result.Errors, herr.Error())
		}
	}

	if err := mm.Error(); err != nil {
		plog.Error(plog.TypeMinimega, "lost connection to minimega", "command", command, "error", err)

		lost := fmt.Errorf("%w: %w", ErrConnectionLost, err)

		errs = append(errs, lost)
		result.Errors = append(result.Errors, lost.Error())
	}

	result.Output = strings.Join(output, "\n")

	if len(errs) > 0 {
		return result, fmt.Errorf("running minimega command %q: %w", command, errors.Join(errs...))
	}

	return result, nil
}

// RunAt sends command to the minimega whose base directory is base, on a
// connection of its own, and returns its answer as Run does. Once ctx is
// done, it closes the connection and returns an error that wraps ctx's error:
// minimega still finishes the command. A failed dial returns an error that
// wraps ErrConnectionLost.
func RunAt(ctx context.Context, base, command string) (Result, error) {
	mm, err := miniclient.Dial(base)
	if err != nil {
		return Result{Output: "", Responses: []HostResponse{}, Errors: []string{}},
			fmt.Errorf("%w: connecting to minimega at %s: %w", ErrConnectionLost, base, err)
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = mm.Close()
		case <-done:
		}
	}()

	defer func() { _ = mm.Close() }()

	result, err := Run(mm, command, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, fmt.Errorf("running minimega command %q: %w", command, ctxErr)
	}

	return result, err
}
