package cmd

import (
	"bytes"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"phenix/store"
	"phenix/util/envsubst"
)

const (
	workflowRenderShort = "Print the configs of a topology directory with their variables filled in"

	workflowRenderLong = workflowRenderShort + `

  render reads a topology directory as apply does: the configs in
  phenix-configs/, the workflow config, and the variables from -e, the env
  files and the shell. It fills in each ${NAME} and ${NAME:default}
  placeholder, ${BRANCH_NAME} included, and prints the text of each file as
  the server gets it and then stores or applies it. It does not contact the
  server, and it changes nothing.

  The output holds each config in upsert order, Topology configs first, and
  then the workflow config. A comment line with the path of the file comes
  before each file, and a line with --- comes between two files. render
  does not format the text of a file again.

  A problem with a variable is an error, as it is in apply. The output can
  hold secrets: render prints the value of each variable that a file uses.`

	workflowRenderExample = `
  # Print the configs of helloworld with their variables filled in
  phenix workflow render helloworld

  # Show what another branch name and a variable give
  phenix workflow render helloworld -b helloworld-2 -e NODES=3

  # Use an env file and another workflow config
  phenix workflow render ./topo --env-file site.env -c alt.yml`
)

func newWorkflowRenderCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "render <DIR|NAME>",
		Short:   workflowRenderShort,
		Long:    workflowRenderLong,
		Example: workflowRenderExample,
		Args:    argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := sourceOptionsFromFlags(cmd)
			if err != nil {
				return err
			}

			return runWorkflowRender(cmd.OutOrStdout(), opts, args[0])
		},
	}

	addWorkflowSourceFlags(cmd)

	return cmd
}

// runWorkflowRender writes to w the configs and the workflow config of the
// topology directory named by arg, as the server has them after an apply:
// the variables filled in, and then ${BRANCH_NAME} as the server fills it in.
// It writes nothing when a file has a problem.
func runWorkflowRender(w io.Writer, opts sourceOptions, arg string) error {
	src, err := resolveWorkflowSource(opts, arg)
	if err != nil {
		return err
	}

	var paths []string

	if src.configs != "" {
		if paths, err = configPaths(src.configs); err != nil {
			return fmt.Errorf("checking configs: %w", err)
		}
	}

	if len(paths) == 0 && src.wfConfig == "" {
		return fmt.Errorf(
			"nothing to render: %s has no %s or %s, and %s/ holds no .json, .yaml or .yml file that is not hidden",
			src.dir, workflowConfigName, legacyWorkflowConfig, configsDirName,
		)
	}

	files, wfConfig, err := loadWorkflowFiles(src.dir, paths, src.wfConfig, src.vars)
	if err != nil {
		return err
	}

	if wfConfig != nil {
		files = append(files, *wfConfig)
	}

	var out bytes.Buffer

	branch := envsubst.Fixed(src.name)

	for i, file := range files {
		if i > 0 {
			out.WriteString("---\n")
		}

		out.WriteString("# " + relTo(src.dir, file.Path) + "\n")
		out.Write(bytes.TrimSuffix(store.ExpandBranchName(file.Body, branch), []byte("\n")))
		out.WriteByte('\n')
	}

	if _, err := w.Write(out.Bytes()); err != nil {
		return fmt.Errorf("writing the configs: %w", err)
	}

	return nil
}
