package cmd

import (
	"encoding/csv"
	"errors"
	"io"
	"slices"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/api/experiment"
	"phenix/util"
	"phenix/web/rbac"
)

const roleHeaderPadding = 2

func newUtilCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "util",
		Short: "Utility commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newUtilAppJSONCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:               "app-json <experiment name>",
		Short:             "Print application JSON input for given experiment to STDOUT",
		ValidArgsFunction: expNameCompletion(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("there was no experiment provided")
			}

			name := args[0]

			exp, err := experiment.AppInput(cmd.Context(), name)
			if errors.Is(err, experiment.ErrAppRuntime) {
				err := util.HumanizeError(err, "Unable to collect runtime experiment information")

				return err.Humanized()
			}

			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to get the "+name+" experiment")

				return err.Humanized()
			}

			return out.print(cmd, exp, nil)
		},
	}

	out = addOutputFlag(cmd, FormatJSON, []string{FormatJSON, FormatYAML}, withPrettyFlag(FormatJSON))

	return cmd
}

// roleTable is what phenix util role-table prints: each permission that
// phenix checks, and the roles that allow it.
type roleTable struct {
	Roles       []string         `json:"roles"       yaml:"roles"`
	Permissions []rolePermission `json:"permissions" yaml:"permissions"`
}

// rolePermission is one permission of a role table.
type rolePermission struct {
	Resource string   `json:"resource" yaml:"resource"`
	Verb     string   `json:"verb"     yaml:"verb"`
	Roles    []string `json:"roles"    yaml:"roles"`
}

// newRoleTable returns the role table of roles.
func newRoleTable(roles []*rbac.Role) roleTable {
	table := roleTable{Roles: make([]string, 0, len(roles)), Permissions: make([]rolePermission, 0, len(rbac.Permissions))}

	for _, r := range roles {
		table.Roles = append(table.Roles, r.Spec.Name)
	}

	for _, p := range rbac.Permissions {
		perm := rolePermission{Resource: p.Resource, Verb: p.Verb, Roles: []string{}}

		for _, r := range roles {
			if r.Allowed(p.Resource, p.Verb) {
				perm.Roles = append(perm.Roles, r.Spec.Name)
			}
		}

		table.Permissions = append(table.Permissions, perm)
	}

	return table
}

// rows returns the header and the rows of the table: a "Y" in each column of
// a role that allows the permission.
func (t roleTable) rows() ([]string, [][]string) {
	header := make([]string, 0, roleHeaderPadding+len(t.Roles))
	header = append(header, "", "")
	header = append(header, t.Roles...)

	data := make([][]string, 0, len(t.Permissions))

	for _, p := range t.Permissions {
		row := []string{p.Resource, p.Verb}

		for _, r := range t.Roles {
			if slices.Contains(p.Roles, r) {
				row = append(row, "Y")
			} else {
				row = append(row, "")
			}
		}

		data = append(data, row)
	}

	return header, data
}

// views returns the CSV and Markdown views of the table.
func (t roleTable) views() outputViews {
	header, data := t.rows()

	return outputViews{
		formatCSV: func(w io.Writer) error {
			cw := csv.NewWriter(w)

			_ = cw.Write(header)
			_ = cw.WriteAll(data)

			return cw.Error() //nolint:wrapcheck // the writer's own error
		},
		formatMarkdown: func(w io.Writer) error {
			table := tablewriter.NewWriter(w)
			table.SetHeader(header)
			table.AppendBulk(data)
			table.SetBorders(
				tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false},
			)
			table.SetCenterSeparator("|")
			table.Render()

			return nil
		},
	}
}

func newUtilRoleTableCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "role-table",
		Short: "Print a table for permissions and roles",
		Args:  argsWithUsage(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			roles, err := rbac.GetRoles()
			if err != nil {
				err := util.HumanizeError(err, "Unable to get the roles")

				return err.Humanized()
			}

			table := newRoleTable(roles)

			return out.print(cmd, table, table.views())
		},
	}

	out = addOutputFlag(
		cmd, formatCSV, []string{formatCSV, formatMarkdown, FormatJSON, FormatYAML}, withPrettyFlag(formatMarkdown),
	)

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	utilCmd := newUtilCmd()

	utilCmd.AddCommand(newUtilAppJSONCmd())
	utilCmd.AddCommand(newUtilSchedulerJSONCmd())
	utilCmd.AddCommand(newUtilScorchJSONCmd())
	utilCmd.AddCommand(newUtilRoleTableCmd())

	addCommandToRoot(utilCmd, true)
}
