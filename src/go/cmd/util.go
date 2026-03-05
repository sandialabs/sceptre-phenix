package cmd

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
	cmd := &cobra.Command{
		Use:   "app-json <experiment name>",
		Short: "Print application JSON input for given experiment to STDOUT",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("there was no experiment provided")
			}

			name := args[0]

			exp, err := experiment.Get(name)
			if err != nil {
				err := util.HumanizeError(err, "%s", "Unable to get the "+name+" experiment")

				return err.Humanized()
			}

			var m []byte

			if MustGetBool(cmd.Flags(), "pretty") {
				m, err = json.MarshalIndent(exp, "", "  ")
			} else {
				m, err = json.Marshal(exp)
			}

			if err != nil {
				err := util.HumanizeError(err, "Unable to convert experiment to JSON")

				return err.Humanized()
			}

			fmt.Fprintln(os.Stdout, string(m))

			return nil
		},
	}

	cmd.Flags().BoolP("pretty", "p", false, "Pretty print the JSON output")

	return cmd
}

func newUtilRoleTableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role-table",
		Short: "Print a table for permissions and roles",
		RunE: func(cmd *cobra.Command, args []string) error {
			roles, _ := rbac.GetRoles()

			header := make([]string, 0, roleHeaderPadding+len(roles))
			header = append(header, "", "")
			for _, r := range roles {
				header = append(header, r.Spec.Name)
			}

			data := make([][]string, 0, len(rbac.Permissions))

			for _, p := range rbac.Permissions {
				row := []string{p.Resource, p.Verb}
				for _, r := range roles {
					if r.Allowed(p.Resource, p.Verb) {
						row = append(row, "Y")
					} else {
						row = append(row, "")
					}
				}

				data = append(data, row)
			}

			if MustGetBool(cmd.Flags(), "pretty") {
				table := tablewriter.NewWriter(os.Stdout)
				table.SetHeader(header)
				table.AppendBulk(data)
				table.SetBorders(
					tablewriter.Border{Left: true, Top: false, Right: true, Bottom: false},
				)
				table.SetCenterSeparator("|")
				table.Render()
			} else {
				w := csv.NewWriter(os.Stdout)

				_ = w.Write(header)
				for _, r := range data {
					_ = w.Write(r)
				}

				w.Flush()
			}

			return nil
		},
	}

	cmd.Flags().BoolP("pretty", "p", false, "Pretty print the table output in markdown")

	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	utilCmd := newUtilCmd()

	utilCmd.AddCommand(newUtilAppJSONCmd())
	utilCmd.AddCommand(newUtilRoleTableCmd())

	rootCmd.AddCommand(utilCmd)
}
