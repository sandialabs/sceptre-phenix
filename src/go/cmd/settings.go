package cmd

import (
	"fmt"
	"os"
	"phenix/api/settings"
	"phenix/util"
	"phenix/util/printer"

	"github.com/spf13/cobra"
)

func newSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings",
		Short: "View or edit phenix settings",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	return cmd
}

func newSettingsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List phenix settings",
		RunE: func(cmd *cobra.Command, args []string) error {

			s, err := settings.List()
			if err != nil {
				err := util.HumanizeError(err, "Unable to print a table")
				return err.Humanized()
			}

			printer.PrintTableOfSettings(os.Stdout, s)
			return nil
		},
	}
	return cmd
}

func newSettingsEditCmd() *cobra.Command {
	example := `
  phenix setting edit <category> <name> <newValue>
  phenix setting edit Password MinLength 20`

	cmd := &cobra.Command{
		Use:     "edit",
		Short:   "Edit a phenix setting",
		Example: example,
		RunE: func(cmd *cobra.Command, args []string) error {

			if len(args) < 1 {
				return fmt.Errorf("Must provide a setting category")
			} else if len(args) < 2 {
				return fmt.Errorf("Must provide a setting name")
			} else if len(args) < 3 {
				return fmt.Errorf("Must provide a setting value")
			} else if len(args) > 3 {
				return fmt.Errorf("Must only provide a setting category, name and value. If the value is a string, please use quotes.")
			}

			category := args[0]
			name := args[1]
			value := args[2]

			err := settings.UpdateWithVerification(category, name, value)
			if err != nil {
				err := util.HumanizeError(err, "Error updating setting")
				return err.Humanized()
			}

			return nil
		},
	}
	return cmd
}

func init() {
	settingsCmd := newSettingsCmd()
	settingsCmd.AddCommand(newSettingsListCmd())
	settingsCmd.AddCommand(newSettingsEditCmd())
	rootCmd.AddCommand(settingsCmd)
}
