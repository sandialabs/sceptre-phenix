package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"phenix/api/settings"
	"phenix/web"
)

// socketUser is the user that the server gives each request from its unix
// socket (see middleware.NoAuth): it owns the forwards that the CLI adds.
const socketUser = "global-admin"

// adminHeldBy says why the admin changes go through the running server.
const adminHeldBy = "only the running server changes users, roles and tokens, so that its websockets and capture grants follow"

var (
	// errNoPasswordInput is returned when no password can be read: stdin is not a terminal and --password-stdin is not
	// set.
	errNoPasswordInput = errors.New("stdin is not a terminal: give the password on stdin with --password-stdin")
	// errPasswordMismatch is returned when the password and its confirmation differ.
	errPasswordMismatch = errors.New("the passwords do not match")
	// errPasswordRules is returned for a password that the password settings refuse.
	errPasswordRules = errors.New("the password does not meet the requirements")
	// errBadPolicy is returned for a --policy value that is not a policy.
	errBadPolicy = errors.New("not a role policy")
)

// passwordReader reads passwords from stdin: one line for each, with
// --password-stdin, else from the terminal without echo, with a prompt on
// stderr and, for a new password, a confirmation.
type passwordReader struct {
	in     io.Reader
	prompt io.Writer
	stdin  bool

	lines *bufio.Reader
}

// newPasswordReader returns a reader of the passwords of cmd.
func newPasswordReader(cmd *cobra.Command, stdin bool) *passwordReader {
	return &passwordReader{in: cmd.InOrStdin(), prompt: cmd.ErrOrStderr(), stdin: stdin, lines: nil}
}

// read returns one password. A terminal shows the prompt, and asks again to
// confirm a new password.
func (p *passwordReader) read(prompt string, confirm bool) (string, error) {
	if p.stdin {
		if p.lines == nil {
			p.lines = bufio.NewReader(p.in)
		}

		line, err := p.lines.ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || line == "") {
			return "", fmt.Errorf("reading a password from stdin: %w", err)
		}

		return strings.TrimRight(line, "\r\n"), nil
	}

	f, ok := p.in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) { //nolint:gosec // G115 file descriptors fit in an int
		return "", errNoPasswordInput
	}

	password, err := p.readTerminal(f, prompt)
	if err != nil || !confirm {
		return password, err
	}

	again, err := p.readTerminal(f, "Confirm "+strings.ToLower(prompt[:1])+prompt[1:])
	if err != nil {
		return "", err
	}

	if again != password {
		return "", errPasswordMismatch
	}

	return password, nil
}

// readTerminal shows prompt and reads one line from the terminal f without echo.
func (p *passwordReader) readTerminal(f *os.File, prompt string) (string, error) {
	fmt.Fprint(p.prompt, prompt)

	b, err := term.ReadPassword(int(f.Fd())) //nolint:gosec // G115 file descriptors fit in an int

	fmt.Fprintln(p.prompt)

	if err != nil {
		return "", fmt.Errorf("reading a password from the terminal: %w", err)
	}

	return string(b), nil
}

// checkPassword returns an error that names the password rules unless the
// password settings accept password, as the API does.
func checkPassword(password string) error {
	if settings.IsPasswordValid(password) {
		return nil
	}

	rules, err := settings.PasswordRules()
	if err != nil {
		return fmt.Errorf("%w: %w", errPasswordRules, err)
	}

	return fmt.Errorf("%w:\n  %s", errPasswordRules, strings.Join(rules, "\n  "))
}

// parsePolicy parses a --policy value: "resources=", "verbs=" and, optionally,
// "names=", each with a comma list, separated by ";", such as
// "resources=vms,vms/*;verbs=get,list;names=my-exp/*". Each list item is
// trimmed and must not be empty, and each key appears at most once.
func parsePolicy(value string) (web.Policy, error) {
	policy := web.Policy{Resources: nil, ResourceNames: nil, Verbs: nil}
	seen := map[string]bool{}

	for part := range strings.SplitSeq(value, ";") {
		key, list, ok := strings.Cut(part, "=")
		key = strings.TrimSpace(key)

		if !ok || seen[key] {
			return policy, fmt.Errorf("%w: %q: give resources=, verbs= and names= once each, separated by ';'", errBadPolicy, value)
		}

		seen[key] = true

		items := strings.Split(list, ",")
		for i, item := range items {
			items[i] = strings.TrimSpace(item)
		}

		if slices.Contains(items, "") {
			return policy, fmt.Errorf("%w: %q: the %s list has an empty item", errBadPolicy, value, key)
		}

		switch key {
		case "resources":
			policy.Resources = items
		case "verbs":
			policy.Verbs = items
		case "names":
			policy.ResourceNames = items
		default:
			return policy, fmt.Errorf("%w: %q: %q is not resources, verbs or names", errBadPolicy, value, key)
		}
	}

	if policy.Resources == nil || policy.Verbs == nil {
		return policy, fmt.Errorf("%w: %q: give resources= and verbs=", errBadPolicy, value)
	}

	return policy, nil
}

// parsePolicies parses the --policy values of cmd; it needs at least one.
func parsePolicies(cmd *cobra.Command) ([]web.Policy, error) {
	values := mustGet(cmd.Flags().GetStringArray, "policy")
	if len(values) == 0 {
		return nil, errors.New("give at least one --policy")
	}

	policies := make([]web.Policy, 0, len(values))

	for _, value := range values {
		p, err := parsePolicy(value)
		if err != nil {
			return nil, err
		}

		policies = append(policies, p)
	}

	return policies, nil
}

// printPolicies writes a table of the policies to w.
func printPolicies(w io.Writer, policies []web.Policy) {
	table := tablewriter.NewWriter(w)
	table.SetAutoWrapText(false)
	table.SetHeader([]string{"Resources", "Verbs", "Resource Names"})

	for _, p := range policies {
		table.Append([]string{strings.Join(p.Resources, ", "), strings.Join(p.Verbs, ", "), strings.Join(p.ResourceNames, ", ")})
	}

	table.Render()
}

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administration of users, roles and API tokens",
		Long: `Administration of users, roles and API tokens

  The list and get commands read the store. The commands that change users,
  roles or tokens call the running phenix server over its unix socket
  (--unix-socket), as the user global-admin. The server then closes the
  websockets of a user whose role changes, revokes the capture grants of a
  deleted user, and signs tokens with its JWT signing key.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAdminUserCmd(), newAdminRoleCmd(), newAdminTokenCmd())

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	addCommandToRoot(newAdminCmd(), true)
}
