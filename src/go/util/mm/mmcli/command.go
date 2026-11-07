// Taken (almost) as-is from minimega/miniweb.

package mmcli

import (
	"fmt"
	"strings"
	"time"
)

// Command represents a command and options to send to minimega.
type Command struct {
	Command   string
	Columns   []string
	Filters   []string
	Namespace string
	Timeout   time.Duration
	// Background commands, such as screenshots, wait for the shared connection
	// behind every other command waiting, so they do not hold up user actions.
	Background bool
}

// NewCommand returns a pointer to a new, initialized command.
func NewCommand() *Command {
	return new(Command)
}

// NewNamespacedCommand returns a pointer to a new command, initialized with the
// given minimega namespace name.
func NewNamespacedCommand(ns string) *Command {
	return &Command{Namespace: ns} //nolint:exhaustruct // partial initialization
}

// String builds the actual command string to send to minimega using the command
// fields.
func (c *Command) String() string {
	cmd := c.Command

	// Apply filters first so we don't need to worry about the columns not
	// including the filtered fields.
	for _, f := range c.Filters {
		cmd = fmt.Sprintf(".filter %v %v", f, cmd)
	}

	// minicli gives the answering node as the response's host, not as a column, so
	// `host` is never asked for: the mappers in tabular.go fill it in.
	columns := make([]string, 0, len(c.Columns))

	for _, column := range c.Columns {
		if column != hostColumn {
			// Quote the column in case it has spaces.
			columns = append(columns, fmt.Sprintf("%q", column))
		}
	}

	if len(columns) > 0 {
		cmd = fmt.Sprintf(".columns %v %v", strings.Join(columns, ","), cmd)
	}

	// If there's a namespace, use it.
	if c.Namespace != "" {
		cmd = fmt.Sprintf("namespace %q %v", c.Namespace, cmd)
	}

	// Don't record command in history.
	cmd = fmt.Sprintf(".record false %v", cmd)

	return cmd
}
