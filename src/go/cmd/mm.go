package cmd

import (
	"fmt"

	"phenix/util"
	"phenix/util/common"

	"github.com/activeshadow/libminimega/miniclient"
	"github.com/spf13/cobra"
)

type noPager struct{}

func (noPager) Page(output string) {
	if output == "" {
		return
	}

	fmt.Println(output)
}

func newMMCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mm <minimega args>...",
		Short: "Send commands, or attach, to minimega",
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				attach    = MustGetBool(cmd.Flags(), "attach")
				namespace = MustGetString(cmd.Flags(), "namespace")
			)

			if !attach && len(args) == 0 {
				return cmd.Help()
			}

			mm, err := miniclient.Dial(common.MinimegaBase)
			if err != nil {
				return util.HumanizeError(err, "Unable to conect to minimega").Humanized()
			}

			mm.Pager = new(noPager)

			if attach {
				mm.Attach(namespace)
				return nil
			}

			var parts []string

			if namespace != "" {
				parts = append(parts, "namespace", namespace)
			}

			parts = append(parts, args...)
			command := util.QuoteJoin(parts, " ")

			if len(parts) == 1 {
				command = parts[0]
			}

			mm.RunAndPrint(command, false)
			return nil
		},
	}

	cmd.Flags().Bool("attach", false, "Attach to minimega console instead of sending commands")
	cmd.Flags().String("namespace", "", "Default minimega namespace to use")

	return cmd
}

func init() {
	mmCmd := newMMCmd()

	rootCmd.AddCommand(mmCmd)
}
