package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"phenix/util/common"
	"phenix/util/sigterm"
)

const (
	workflowConfigName   = "phenix.yml"
	legacyWorkflowConfig = ".phenix.yml"
	injectsDirName       = "phenix-injects"
	configsDirName       = "phenix-configs"
	optionWorkflowDryRun = "workflow-dry-run"
	optionInjectsBase    = "base-dir.injects"
	workflowDateLayout   = "20060102150405MST"

	// optionBranchName is the -b flag of apply, and optionBranchNameAlias its
	// hidden alias, kept so an invocation written with --name still works.
	optionBranchName      = "branch-name"
	optionBranchNameAlias = "name"
)

// Steps of workflow apply, as logged in the step field and named in the help.
const (
	stepPreflight = "preflight"
	stepPlan      = "plan"
	stepInjects   = "injects"
	stepConfigs   = "configs"
	stepApply     = "apply"
)

const (
	workflowApplyLong = `Deploy a topology directory through the running phenix server

  A topology directory holds any of:

    phenix-injects/   copied to <base-dir.injects>/<name>
    phenix-configs/   configs upserted to the server, Topology configs first;
                      only .json, .yaml and .yml files count, and hidden
                      files are skipped
    phenix.yml        the workflow config (any other name, such as
                      phenix.yaml, needs -c)

  DIR|NAME is required. An existing path is used as is. Otherwise a NAME
  with no path separator is looked up under --base-dir.topologies. The
  workflow name defaults to the base name of the directory; -b sets
  another.

  The names phenix.yml, phenix-configs and phenix-injects are
  reserved, in any letter case: they cannot be the workflow's name,
  and no entry of phenix-injects/ may have one of them at its top level
  or directly inside one of its top-level directories.

  apply talks to the server over --unix-socket and changes nothing until
  step 3. Each log record names its step in the step field:

    1. preflight: check the server, and check that every file is one
       YAML or JSON document and that every config has a kind
    2. plan: the server dry-runs every config, then the workflow config
       with those configs as pending, and checks what they refer to;
       every rejection is logged, any rejection, or two files that
       define the same config, stops the apply, and the plan is logged
    3. injects: copy phenix-injects/ to the injects directory the server
       reports, or to --base-dir.injects when it is given on the command
       line
    4. configs: upsert every config; every rejection is logged, and any
       rejection stops the apply
    5. apply: apply the workflow config, tagged with where it came from

  A missing input skips its step. -n stops after step 2. A plan that
  would restart a running experiment needs -f. Progress is logged at the
  info level, so --log.level warn hides it. Skipped steps, and an apply
  that changes no experiment (action none), are logged as warnings.`

	workflowApplyExample = `
  # Deploy by path; the workflow name is the directory name, helloworld
  phenix workflow apply /phenix/topologies/helloworld

  # Look helloworld up under --base-dir.topologies; works from any directory
  phenix workflow apply helloworld

  # Deploy the current directory
  phenix workflow apply .

  # Validate and log the plan; change nothing
  phenix workflow apply helloworld -n

  # Allow restarting the running experiment
  phenix workflow apply helloworld -f

  # Deploy under another name; injects go to <base-dir.injects>/helloworld-2
  phenix workflow apply helloworld -b helloworld-2

  # Use another workflow config
  phenix workflow apply helloworld -c alt.yml

  # Dry run of an alternate config under another name
  phenix workflow apply helloworld -b hw-test -c alt.yml -n

  # A directory with only phenix-configs/: upsert the configs; injects and apply are skipped
  phenix workflow apply ./topo

  # Use a non-default socket
  phenix --unix-socket /run/phenix.sock workflow apply helloworld

  # Use another topology root
  phenix --base-dir.topologies /srv/topologies workflow apply helloworld

  # The same, from the environment
  PHENIX_BASE_DIR_TOPOLOGIES=/srv/topologies phenix workflow apply helloworld

  # Override the injects directory the server reports
  phenix --base-dir.injects /srv/injects workflow apply helloworld

  # Log request and response detail
  phenix --log.level debug workflow apply helloworld -n

  # Compose deployment; the socket is inside the container
  docker exec phenix phenix workflow apply /phenix/topologies/helloworld -f`
)

func newWorkflowCmd() *cobra.Command {
	desc := `Workflow management

  Deploy topology directories through the running phenix server over its
  unix socket (--unix-socket).`

	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Workflow management",
		Long:  desc,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	return cmd
}

func newWorkflowApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "apply <DIR|NAME>",
		Short:   "Deploy a topology directory through the phenix server",
		Long:    workflowApplyLong,
		Example: workflowApplyExample,
		Args:    argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := applyOptionsFromFlags(cmd)
			if err != nil {
				return err
			}

			ctx := sigterm.CancelContext(context.Background())

			return runWorkflowApply(ctx, opts, args[0])
		},
	}

	cmd.Flags().StringP(optionBranchName, "b", "", "workflow branch name (default: base name of the topology directory)")
	cmd.Flags().String(optionBranchNameAlias, "", "alias of --"+optionBranchName)
	_ = cmd.Flags().MarkHidden(optionBranchNameAlias)
	cmd.Flags().StringP(
		"config", "c", workflowConfigName,
		"workflow config, relative to the topology directory unless absolute",
	)
	cmd.Flags().BoolP("dry-run", "n", false, "check every config and the workflow config, log the server's plan, and change nothing")
	cmd.Flags().BoolP("force", "f", false, "allow apply to stop, reconfigure and restart a running experiment")

	return cmd
}

// applyOptionsFromFlags builds the apply options from cmd's flags and the
// global settings the root command set up. The error is that of
// [branchNameFromFlags].
func applyOptionsFromFlags(cmd *cobra.Command) (applyOptions, error) {
	name, err := branchNameFromFlags(cmd.Flags())
	if err != nil {
		return applyOptions{}, err
	}

	return applyOptions{
		Name:                name,
		Config:              MustGetString(cmd.Flags(), "config"),
		ConfigExplicit:      cmd.Flags().Changed("config"),
		DryRun:              MustGetBool(cmd.Flags(), "dry-run"),
		Force:               MustGetBool(cmd.Flags(), "force"),
		Socket:              common.UnixSocket,
		InjectsBase:         common.InjectsBase,
		TopologiesBase:      common.TopologiesBase,
		InjectsBaseExplicit: cmd.Flags().Changed(optionInjectsBase),
		Now:                 time.Now,
	}, nil
}

// branchNameFromFlags returns the workflow branch name: --branch-name (-b),
// or its hidden alias --name when only that one was given. Both given with
// the same value is accepted; with different values it is an error, since
// there is no way to tell which was meant.
func branchNameFromFlags(flags *pflag.FlagSet) (string, error) {
	branch := MustGetString(flags, optionBranchName)
	alias := MustGetString(flags, optionBranchNameAlias)

	switch {
	case !flags.Changed(optionBranchNameAlias):
		return branch, nil
	case !flags.Changed(optionBranchName):
		return alias, nil
	case branch != alias:
		return "", fmt.Errorf(
			"--%s %q and --%s %q differ; --%s is an alias of --%s, so pass only one of them",
			optionBranchName, branch, optionBranchNameAlias, alias, optionBranchNameAlias, optionBranchName,
		)
	default:
		return branch, nil
	}
}

func init() { //nolint:gochecknoinits // cobra command
	workflowCmd := newWorkflowCmd()

	workflowCmd.AddCommand(newWorkflowApplyCmd())

	// The examples use global flags (--unix-socket, --base-dir.*, --log.level),
	// so help shows them, as it does for ui.
	addCommandToRoot(workflowCmd, false)
}
