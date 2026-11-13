package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/util/common"
	"phenix/web"
	"phenix/web/rbac"
)

// roleRow is a role with the fields of GET /roles and its config name.
type roleRow struct {
	web.Role

	ConfigName string `json:"configName" yaml:"configName"`
}

// roleRows returns the rows of the roles, sorted by config name.
func roleRows(roles []*rbac.Role) []roleRow {
	slices.SortFunc(roles, func(a, b *rbac.Role) int { return strings.Compare(a.ConfigName(), b.ConfigName()) })

	rows := make([]roleRow, 0, len(roles))
	for _, r := range roles {
		rows = append(rows, roleRow{ConfigName: r.ConfigName(), Role: web.RoleView(*r)})
	}

	return rows
}

// printRoles writes a table of the roles to w, sorted by config name.
func printRoles(w io.Writer, rows []roleRow) {
	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{"Config Name", "Name", "Policies"})

	for _, r := range rows {
		table.Append([]string{r.ConfigName, r.Name, strconv.Itoa(len(r.Policies))})
	}

	table.Render()
}

// rolePath is the API path of the role name.
func rolePath(name string) string {
	return "/roles/" + url.PathEscape(name)
}

func newAdminRoleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role",
		Short: "List, get, create, update, or delete roles",
		Long: `List, get, create, update, or delete roles

  A role is a list of policies. A policy gives verbs on resources, and
  optionally only on some resource names. Give a policy as
  resources=<list>;verbs=<list>[;names=<list>], each list separated by
  commas. Only custom roles change: a built-in role cannot. A role cannot
  give more than the user global-admin of the unix socket holds, and no
  custom role may give roles create, patch or delete.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newAdminRoleListCmd(), newAdminRoleGetCmd(), newAdminRoleCreateCmd(), newAdminRoleUpdateCmd(), newAdminRoleDeleteCmd(),
	)

	return cmd
}

func newAdminRoleListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the roles",
		Long: `List the roles

  -o json and -o yaml print each role with the fields of GET /roles and its
  config name (configName), sorted by config name.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			roles, err := rbac.GetRoles()
			if err != nil {
				return fmt.Errorf("unable to list the roles: %w", err)
			}

			rows := roleRows(roles)

			return out.printAPI(cmd, rows, outputViews{
				formatTable: func(w io.Writer) error {
					printRoles(w, rows)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func newAdminRoleGetCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "get <role name>",
		Short: "Show the policies of a role",
		Long: `Show the policies of a role

  The role name is its name or its config name. -o json and -o yaml print
  the role with the fields of GET /roles/{name} and its config name
  (configName).`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeRoles),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, err := rbac.RoleFromConfig(args[0])
			if err != nil {
				return fmt.Errorf("unable to get the role %s: %w", args[0], err)
			}

			row := roleRows([]*rbac.Role{role})[0]

			return out.printAPI(cmd, row, outputViews{
				formatTable: func(w io.Writer) error {
					printRoles(w, []roleRow{row})
					printPolicies(w, row.Policies)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func newAdminRoleCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <role name>",
		Short: "Create a custom role",
		Long: `Create a custom role

  Creates a role with the policies that --policy gives. The config name is
  --config-name, or else the role name in lower case with a "-" for each run
  of other characters. Give the role to a user with phenix admin user
  update <username> --role <role name>.`,
		Example: `
  phenix admin role create "Lab Viewer" --policy 'resources=experiments,vms;verbs=list,get;names=lab-*'`,
		Args: argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			policies, err := parsePolicies(cmd)
			if err != nil {
				return err
			}

			req := web.CreateRoleRequest{
				Name: args[0], MetadataName: mustGet(cmd.Flags().GetString, "config-name"), Policies: &policies,
			}

			var created web.Role

			client := newServerClient(common.UnixSocket, adminHeldBy)
			if err := client.call(commandContext(cmd), http.MethodPost, "/roles", nil, req, &created); err != nil {
				return fmt.Errorf("unable to create the role %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Created role %s\n", created.Name)
			printPolicies(cmd.OutOrStdout(), created.Policies)

			return nil
		},
	}

	cmd.Flags().StringArray("policy", nil, "Policy of the role, as resources=<list>;verbs=<list>[;names=<list>] (repeatable)")
	cmd.Flags().String("config-name", "", "Config name of the role (default from the role name)")

	return cmd
}

func newAdminRoleUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <role name>",
		Short: "Replace the policies of a custom role",
		Long: `Replace the policies of a custom role

  Replaces all policies of the role with those that --policy gives. Each
  user with the role gets the new policies, with the resource names that
  the user had, and the websockets of those users close.`,
		Example: `
  phenix admin role update lab-viewer --policy 'resources=experiments;verbs=list,get'`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeRoles),
		RunE: func(cmd *cobra.Command, args []string) error {
			policies, err := parsePolicies(cmd)
			if err != nil {
				return err
			}

			var updated web.RoleUpdate

			client := newServerClient(common.UnixSocket, adminHeldBy)

			req := web.UpdateRoleRequest{Name: "", Policies: &policies}

			err = client.call(commandContext(cmd), http.MethodPatch, rolePath(args[0]), nil, req, &updated)
			if err != nil {
				return fmt.Errorf("unable to update the role %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Updated role %s and the copies of the role of %d users", updated.Role.Name, updated.Count)

			if len(updated.Users) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), ": %s", strings.Join(updated.Users, ", "))
			}

			fmt.Fprintln(cmd.OutOrStdout())
			printPolicies(cmd.OutOrStdout(), updated.Role.Policies)

			return nil
		},
	}

	cmd.Flags().StringArray("policy", nil, "Policy of the role, as resources=<list>;verbs=<list>[;names=<list>] (repeatable)")

	return cmd
}

func newAdminRoleDeleteCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "delete <role name>",
		Short: "Delete a custom role that no user has",
		Long: `Delete a custom role that no user has

  --dry-run reads the store: it checks that the role exists, is not built
  in, and that no user has it, and does not call the server. The server
  also refuses a role that its settings name, such as in ui.users.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeRoles),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return errors.New("give the name of the role")
			}

			if dry.enabled() {
				if err := web.CheckRoleDelete(args[0]); err != nil {
					return fmt.Errorf("unable to delete the role %s: %w", args[0], err)
				}

				dry.skip("delete", "role "+args[0])

				return nil
			}

			client := newServerClient(common.UnixSocket, adminHeldBy)
			if err := client.call(commandContext(cmd), http.MethodDelete, rolePath(args[0]), nil, nil, nil); err != nil {
				return fmt.Errorf("unable to delete the role %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted role %s\n", args[0])

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}
