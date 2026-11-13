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
	workflowConfigName         = "phenix.yml"
	legacyWorkflowConfig       = ".phenix.yml"
	defaultEnvFileName         = ".env"
	injectsDirName             = "phenix-injects"
	configsDirName             = "phenix-configs"
	optionWorkflowDryRun       = "workflow-dry-run"
	optionInjectsBase          = "base-dir.injects"
	optionFollowInjectSymlinks = "follow-inject-symlinks"
	workflowDateLayout         = "20060102150405MST"

	// optionBranchName is the -b flag of apply, and optionBranchNameAlias its
	// alias --name, which is the same flag.
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
    .env              variables for the configs, as NAME=value lines

  DIR|NAME is required. An existing path is used as is. Otherwise a NAME
  with no path separator is looked up under --base-dir.topologies. The
  branch name defaults to the base name of the directory; -b sets
  another. -b, --branch-name and --name are the same flag. Give only one of
  them, or give them the same value.

  The branch name is not the experiment name. The experiment name is
  auto.create in the workflow config. With "auto.create: ${BRANCH_NAME}",
  -b also names the experiment, and each branch name is a separate
  deployment. A fixed auto.create ignores -b. phenix reads auto.create
  only when no experiment is mapped to the branch.

  apply fills in the ${NAME} and ${NAME:default} placeholders of each
  config and of the workflow config before it sends them. A value comes
  from -e, else from an env file, else from the shell. --env-file names
  the env files; without it, apply reads .env in the topology directory.
  The shell gives no PHENIX_ variable unless -e NAME names it. A
  placeholder with no value and no default is an error, and so is a -e
  variable that no file uses. Write ${NAME:} for an optional variable and
  $${NAME} for the literal text ${NAME}. Only -b sets BRANCH_NAME, and the
  server fills in ${BRANCH_NAME}. Files in phenix-injects/ are not
  changed.

  The names phenix.yml, phenix-configs and phenix-injects are
  reserved, in any letter case: they cannot be the branch name,
  and no entry of phenix-injects/ may have one of them at its top level
  or directly inside one of its top-level directories.

  apply talks to the server over --unix-socket and changes nothing until
  step 3. Each log record names its step in the step field:

    1. preflight: check the server, fill in the variables, and check that
       every file is one YAML or JSON document and that every config has
       a kind
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
  # Deploy by path; the branch name is the directory name, helloworld
  phenix workflow apply /phenix/topologies/helloworld

  # Look helloworld up under --base-dir.topologies; works from any directory
  phenix workflow apply helloworld

  # Deploy the current directory
  phenix workflow apply .

  # Validate and log the plan; change nothing
  phenix workflow apply helloworld -n

  # Allow restarting the running experiment
  phenix workflow apply helloworld -f

  # Deploy under another branch name; injects go to <base-dir.injects>/helloworld-2
  phenix workflow apply helloworld -b helloworld-2

  # The same; --branch-name and --name are the same as -b
  phenix workflow apply helloworld --name helloworld-2

  # Set variables for the ${NAME} placeholders; -e overrides an env file
  phenix workflow apply helloworld -e NODES=3 -e SITE=lab

  # Copy NODES from the shell; it then overrides helloworld/.env
  phenix workflow apply helloworld -e NODES

  # Read the env files, and not helloworld/.env; site.env overrides base.env
  phenix workflow apply helloworld --env-file base.env --env-file site.env

  # Follow symlinks in phenix-injects/ and copy targets as regular files and directories
  phenix workflow apply helloworld --follow-inject-symlinks

  # Use another workflow config
  phenix workflow apply helloworld -c alt.yml

  # Dry run of an alternate config under another branch name
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
  unix socket (--unix-socket), and print their configs with the variables
  filled in.`

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

	addWorkflowSourceFlags(cmd)
	cmd.Flags().BoolP("dry-run", "n", false, "check every config and the workflow config, log the server's plan, and change nothing")
	cmd.Flags().BoolP("force", "f", false, "allow apply to stop, reconfigure and restart a running experiment")
	cmd.Flags().Bool(
		optionFollowInjectSymlinks, false,
		"follow symlinks in phenix-injects/ and copy targets as regular files and directories",
	)

	return cmd
}

// addWorkflowSourceFlags adds the flags that say which topology directory a
// workflow command reads, and with which variables: -b and its alias, -c, -e
// and --env-file.
func addWorkflowSourceFlags(cmd *cobra.Command) {
	cmd.Flags().StringP(
		optionBranchName, "b", "",
		"workflow branch name (default: base name of the topology directory); it does not rename an experiment",
	)
	cmd.Flags().String(optionBranchNameAlias, "", "same as -b/--"+optionBranchName)
	cmd.Flags().StringP(
		"config", "c", workflowConfigName,
		"workflow config, relative to the topology directory unless absolute",
	)
	addVarFlags(cmd)
}

// sourceOptionsFromFlags builds the source options from the flags that
// [addWorkflowSourceFlags] adds and the global settings the root command set
// up. The error is that of [branchNameFromFlags].
func sourceOptionsFromFlags(cmd *cobra.Command) (sourceOptions, error) {
	name, err := branchNameFromFlags(cmd.Flags())
	if err != nil {
		return sourceOptions{}, err
	}

	vars := varOptionsFromFlags(cmd)

	return sourceOptions{
		Name:           name,
		Config:         mustGet(cmd.Flags().GetString, "config"),
		ConfigExplicit: cmd.Flags().Changed("config"),
		TopologiesBase: common.TopologiesBase,
		Env:            vars.Flags,
		EnvFiles:       vars.EnvFiles,
		Environ:        vars.Environ,
	}, nil
}

// applyOptionsFromFlags builds the apply options from cmd's flags and the
// global settings the root command set up. The error is that of
// [branchNameFromFlags].
func applyOptionsFromFlags(cmd *cobra.Command) (applyOptions, error) {
	src, err := sourceOptionsFromFlags(cmd)
	if err != nil {
		return applyOptions{}, err
	}

	return applyOptions{
		Name:                 src.Name,
		Config:               src.Config,
		ConfigExplicit:       src.ConfigExplicit,
		DryRun:               mustGet(cmd.Flags().GetBool, "dry-run"),
		Force:                mustGet(cmd.Flags().GetBool, "force"),
		Socket:               common.UnixSocket,
		InjectsBase:          common.InjectsBase,
		TopologiesBase:       src.TopologiesBase,
		InjectsBaseExplicit:  cmd.Flags().Changed(optionInjectsBase),
		FollowInjectSymlinks: mustGet(cmd.Flags().GetBool, optionFollowInjectSymlinks),
		Env:                  src.Env,
		EnvFiles:             src.EnvFiles,
		Environ:              src.Environ,
		Now:                  time.Now,
	}, nil
}

// branchNameFromFlags returns the workflow branch name: --branch-name (-b),
// or its alias --name when only that one was given. Both given with
// the same value is accepted; with different values it is an error, since
// there is no way to tell which was meant.
func branchNameFromFlags(flags *pflag.FlagSet) (string, error) {
	branch := mustGet(flags.GetString, optionBranchName)
	alias := mustGet(flags.GetString, optionBranchNameAlias)

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
	workflowCmd.AddCommand(newWorkflowRenderCmd())

	// Global flags are hidden in help, as they are for every command but ui.
	// The long help and the examples name the ones apply reads.
	addCommandToRoot(workflowCmd, true)
}
