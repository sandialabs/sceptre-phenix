package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

func newCompletionCmd() *cobra.Command {
	// completionCmd represents the completion command
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate completion script",
		Long: `To load completions:

Bash:

  $ source <(phenix completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ phenix completion bash > /etc/bash_completion.d/phenix
  # macOS:
  $ phenix completion bash > /usr/local/etc/bash_completion.d/phenix

Zsh:

  # If shell completion is not already enabled in your environment,
  # you will need to enable it.  You can execute the following once:

  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ phenix completion zsh > "${fpath[1]}/_phenix"

  # You will need to start a new shell for this setup to take effect.

Fish:

  $ phenix completion fish | source

  # To load completions for each session, execute once:
  $ phenix completion fish > ~/.config/fish/completions/phenix.fish

PowerShell:

  PS> phenix completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, run:
  PS> phenix completion powershell > phenix.ps1
  # and source this file from your PowerShell profile.
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.ExactValidArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletion(os.Stdout)
			}
			return nil
		},
	}
	return cmd
}

func init() { //nolint:gochecknoinits // cobra command
	rootCmd.AddCommand(newCompletionCmd())
}
