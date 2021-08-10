package cmd

import (
	"fmt"
	"os"

	"phenix/store"
	"phenix/util"
	"phenix/util/printer"

	"github.com/spf13/cobra"
)

func newEventCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "Event analysis",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newEventListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Display a table of events",
		RunE: func(cmd *cobra.Command, args []string) error {
			events, err := store.GetEvents()
			if err != nil {
				err := util.HumanizeError(err, "Unable to get list of events")
				return err.Humanized()
			}

			if len(events) == 0 {
				fmt.Println("\nThere are no recorded events\n")
			} else {
				events.SortByTimestamp()
				printer.PrintTableOfEvents(os.Stdout, events, MustGetBool(cmd.Flags(), "show-id"))
			}

			return nil
		},
	}

	cmd.Flags().Bool("show-id", false, "Include event IDs in table")

	return cmd
}

func newEventShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <uuid>",
		Short: "Show details of specific event",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			event := store.Event{ID: args[0]}
			if err := store.GetEvent(&event); err != nil {
				err := util.HumanizeError(err, "Unable to get event %s", args[0])
				return err.Humanized()
			}

			printer.PrintEventTable(os.Stdout, event)
			return nil
		},
	}

	return cmd
}

func init() {
	eventCmd := newEventCmd()

	eventCmd.AddCommand(newEventListCmd())
	eventCmd.AddCommand(newEventShowCmd())

	rootCmd.AddCommand(eventCmd)
}
