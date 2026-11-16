package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"phenix/util/common"
	"phenix/web"
	"phenix/web/rbac"
)

// printUsers writes a table of the users to w, sorted by username.
func printUsers(w io.Writer, users []web.User) {
	slices.SortFunc(users, func(a, b web.User) int { return strings.Compare(a.Username, b.Username) })

	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{"Username", "First Name", "Last Name", "Role", "Resource Names"})

	for _, u := range users {
		table.Append([]string{u.Username, u.FirstName, u.LastName, u.Role.Name, strings.Join(u.ResourceNames, ", ")})
	}

	table.Render()
}

// userPath is the API path of the user username.
func userPath(username string) string {
	return "/users/" + url.PathEscape(username)
}

func newAdminUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "List, get, create, update, or delete users",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newAdminUserListCmd(), newAdminUserGetCmd(), newAdminUserCreateCmd(), newAdminUserUpdateCmd(), newAdminUserDeleteCmd(),
	)

	return cmd
}

func newAdminUserListCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the users",
		Long: `List the users

  -o json and -o yaml print each user with the fields of GET /users, sorted
  by username.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			users, err := rbac.GetUsers()
			if err != nil {
				return fmt.Errorf("unable to list the users: %w", err)
			}

			views := make([]web.User, 0, len(users))
			for _, u := range users {
				views = append(views, web.UserView(*u))
			}

			slices.SortFunc(views, func(a, b web.User) int { return strings.Compare(a.Username, b.Username) })

			return out.printAPI(cmd, views, outputViews{
				formatTable: func(w io.Writer) error {
					printUsers(w, views)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func newAdminUserGetCmd() *cobra.Command {
	var out *outputFlag

	cmd := &cobra.Command{
		Use:   "get <username>",
		Short: "Show a user and the policies of its role",
		Long: `Show a user and the policies of its role

  -o json and -o yaml print the user with the fields of GET /users/{username}.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeUsers),
		RunE: func(cmd *cobra.Command, args []string) error {
			u, err := rbac.GetUser(args[0])
			if err != nil {
				return fmt.Errorf("unable to get the user %s: %w", args[0], err)
			}

			view := web.UserView(*u)

			return out.printAPI(cmd, view, outputViews{
				formatTable: func(w io.Writer) error {
					printUsers(w, []web.User{view})
					printPolicies(w, view.Role.Policies)

					return nil
				},
			})
		},
	}

	out = addOutputFlag(cmd, formatTable, tableJSONYAML())

	return cmd
}

func newAdminUserCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <username>",
		Short: "Create a user",
		Long: `Create a user

  Creates a user with the role --role. --resource-names limits the role to
  those resource names, such as my-exp/*. The command asks for the password
  twice on the terminal, or reads it from the first line of stdin with
  --password-stdin. The password must meet the password settings. The user
  may get and update their own user.`,
		Example: `
  phenix admin user create alice --role experiment-user --resource-names 'lab-*'
  printf '%s\n' "$PASSWORD" | phenix admin user create bob --role global-viewer --password-stdin`,
		Args: argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			if err := rbac.ValidateUsername(username); err != nil {
				return err //nolint:wrapcheck // the reason is the answer
			}

			role := mustGet(cmd.Flags().GetString, "role")
			if role == "" {
				return errors.New("give the role of the user with --role")
			}

			password, err := newPasswordReader(cmd, mustGet(cmd.Flags().GetBool, "password-stdin")).read("Password: ", true)
			if err != nil {
				return err
			}

			if err := checkPassword(password); err != nil {
				return err
			}

			req := web.CreateUserRequest{
				Username:      username,
				Password:      password,
				FirstName:     mustGet(cmd.Flags().GetString, "first-name"),
				LastName:      mustGet(cmd.Flags().GetString, "last-name"),
				RoleName:      role,
				ResourceNames: mustGet(cmd.Flags().GetStringSlice, "resource-names"),
			}

			var created web.User

			client := newServerClient(common.UnixSocket, adminHeldBy)
			if err := client.call(commandContext(cmd), http.MethodPost, "/users", nil, req, &created); err != nil {
				return fmt.Errorf("unable to create the user %s: %w", username, err)
			}

			printUsers(cmd.OutOrStdout(), []web.User{created})

			return nil
		},
	}

	cmd.Flags().String("role", "", "Role of the user, by its name or its config name")
	cmd.Flags().String("first-name", "", "First name of the user")
	cmd.Flags().String("last-name", "", "Last name of the user")
	cmd.Flags().StringSlice("resource-names", nil, "Resource names that the role applies to (comma list)")
	cmd.Flags().Bool("password-stdin", false, "Read the password from the first line of stdin")

	_ = cmd.RegisterFlagCompletionFunc("role", completeRoles)

	return cmd
}

func newAdminUserUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <username>",
		Short: "Change the names, role, resource names, or password of a user",
		Long: `Change the names, role, resource names, or password of a user

  Changes only what the flags give. --resource-names without --role keeps
  the role of the user. --password changes the password: the command asks
  for the current password and the new password on the terminal, or reads
  them from the first two lines of stdin with --password-stdin. The API
  changes a password only with the current password.`,
		Example: `
  phenix admin user update alice --role experiment-admin
  phenix admin user update alice --first-name Alice --last-name Smith
  phenix admin user update alice --password`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeUsers),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]

			var (
				flags = cmd.Flags()
				req   = web.UpdateUserRequest{
					CreateUserRequest: web.CreateUserRequest{
						Username: "", Password: "", RoleName: mustGet(flags.GetString, "role"),
						FirstName: mustGet(flags.GetString, "first-name"), LastName: mustGet(flags.GetString, "last-name"),
						ResourceNames: nil,
					},
					NewPassword: "",
				}
				stdin    = mustGet(flags.GetBool, "password-stdin")
				password = mustGet(flags.GetBool, "password") || stdin
			)

			// the API sets the role and its names together, so the one not given is sent as it is
			if req.RoleName != "" || flags.Changed("resource-names") {
				u, err := rbac.GetUser(username)
				if err != nil {
					return fmt.Errorf("unable to get the user %s: %w", username, err)
				}

				if req.RoleName == "" {
					req.RoleName = u.RoleName()
				}

				req.ResourceNames = web.UserView(*u).ResourceNames
				if flags.Changed("resource-names") {
					req.ResourceNames = mustGet(flags.GetStringSlice, "resource-names")
				}
			}

			if req.RoleName == "" && req.FirstName == "" && req.LastName == "" && !password {
				return errors.New("give at least one of --first-name, --last-name, --role, --resource-names and --password")
			}

			if password {
				passwords := newPasswordReader(cmd, stdin)

				current, err := passwords.read("Current password: ", false)
				if err != nil {
					return err
				}

				next, err := passwords.read("New password: ", true)
				if err != nil {
					return err
				}

				if err := checkPassword(next); err != nil {
					return err
				}

				req.Password, req.NewPassword = current, next
			}

			var updated web.User

			client := newServerClient(common.UnixSocket, adminHeldBy)
			if err := client.call(commandContext(cmd), http.MethodPatch, userPath(username), nil, req, &updated); err != nil {
				return fmt.Errorf("unable to update the user %s: %w", username, err)
			}

			printUsers(cmd.OutOrStdout(), []web.User{updated})

			return nil
		},
	}

	cmd.Flags().String("role", "", "New role of the user, by its name or its config name")
	_ = cmd.RegisterFlagCompletionFunc("role", completeRoles)
	cmd.Flags().String("first-name", "", "New first name of the user")
	cmd.Flags().String("last-name", "", "New last name of the user")
	cmd.Flags().StringSlice("resource-names", nil, "Resource names that the role applies to (comma list)")
	cmd.Flags().Bool("password", false, "Change the password")
	cmd.Flags().Bool("password-stdin", false, "Change the password, and read the current and the new password from stdin")

	return cmd
}

func newAdminUserDeleteCmd() *cobra.Command {
	var dry dryRun

	cmd := &cobra.Command{
		Use:   "delete <username>",
		Short: "Delete a user",
		Long: `Delete a user

  Deletes the user, closes the websockets of the user, and revokes the
  capture grants given to the user. --dry-run reads the store: it checks
  that the user exists and is not global-admin, and does not call the
  server.`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeUsers),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dry.enabled() {
				if args[0] == socketUser {
					return fmt.Errorf("unable to delete the user %s: %w", args[0], errOwnUser)
				}

				if _, err := rbac.GetUser(args[0]); err != nil {
					return fmt.Errorf("unable to delete the user %s: %w", args[0], err)
				}

				dry.skip("delete", "user "+args[0])

				return nil
			}

			client := newServerClient(common.UnixSocket, adminHeldBy)
			if err := client.call(commandContext(cmd), http.MethodDelete, userPath(args[0]), nil, nil, nil); err != nil {
				return fmt.Errorf("unable to delete the user %s: %w", args[0], err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted user %s\n", args[0])

			return nil
		},
	}

	dry = addDryRunFlag(cmd)

	return cmd
}

// errOwnUser is the error of a delete of the user that the server gives each
// request from its unix socket, which the server refuses.
var errOwnUser = errors.New("the server refuses to delete the user of its unix socket")
