package cmd

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"phenix/util/common"
	"phenix/web"
	"phenix/web/rbac"
)

// defaultTokenLifetime is the lifetime of a token that phenix admin token create signs without --lifetime.
const defaultTokenLifetime = "24h"

// errBadTokenLifetime is returned for a --lifetime that is not a duration or a whole number of days.
var errBadTokenLifetime = errors.New("invalid token lifetime")

func newAdminTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Create API tokens",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newAdminTokenCreateCmd())

	return cmd
}

func newAdminTokenCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <username>",
		Short: "Create an API token for a user",
		Long: `Create an API token for a user

  The running phenix server signs the token with its JWT signing key
  (ui.jwt-signing-key) and adds it to the tokens of the user. The command
  writes the token to stdout and its expiry to stderr. Send the token in the
  X-Phenix-Auth-Token header. The token is valid until it expires or the
  user signs out with it. --lifetime is a duration, such as 12h, or a whole
  number of days.`,
		Example: `
  phenix admin token create alice
  TOKEN=$(phenix admin token create ci-bot --lifetime 30 --description "CI runner")`,
		Args:              argsWithUsage(cobra.ExactArgs(1)),
		ValidArgsFunction: completeArgs(completeUsers),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			if err := rbac.ValidateUsername(username); err != nil {
				return err //nolint:wrapcheck // the reason is the answer
			}

			lifetime := mustGet(cmd.Flags().GetString, "lifetime")
			if _, ok := web.TokenLifetime(lifetime); !ok {
				return fmt.Errorf("%w: %q is not a duration more than zero, such as 12h, or a whole number of days",
					errBadTokenLifetime, lifetime)
			}

			req := web.CreateTokenRequest{Lifetime: lifetime, Description: mustGet(cmd.Flags().GetString, "description")}

			var created web.CreateTokenResponse

			client := newServerClient(common.UnixSocket, adminHeldBy)
			if err := client.call(commandContext(cmd), http.MethodPost, userPath(username)+"/tokens", nil, req, &created); err != nil {
				return fmt.Errorf("unable to create a token for the user %s: %w", username, err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), created.Token)
			fmt.Fprintf(cmd.ErrOrStderr(), "The token %q of user %s expires at %s.\n", created.Description, username, created.Expiration)

			return nil
		},
	}

	cmd.Flags().String("lifetime", defaultTokenLifetime, "How long the token is valid: a duration or a whole number of days")
	cmd.Flags().String("description", "", "Description of the token (default \"manually generated\" and the time)")

	return cmd
}
