package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"phenix/api/workflow"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/util/sigterm"
)

const (
	workflowDeleteLong = `Delete what workflow apply deployed for a branch name

  NAME is the branch name of the deployment, not a directory. delete
  removes:

    the experiment mapped to NAME, through the phenix server
    <base-dir.injects>/NAME, the injects directory
    .NAME.tmp-* and .NAME.old-*, the staging copies of workflow apply,
      such as the leftovers of an interrupted apply

  Do not run delete and workflow apply for the same NAME at the same time.
  delete removes the injects directory and the staging copies that apply
  writes.

  delete does not remove the stored configs, such as the Topology and
  Scenario configs. Use phenix config delete for them.

  delete does not stop a running experiment unless you give -f. When the
  experiment runs and -f is not given, or when more than one experiment is
  mapped to NAME, delete removes nothing and exits with an error.

  delete keeps the injects directory when the topology of another
  experiment has an injection whose absolute source path is in it. It
  logs a warning that names those experiments. Relative source paths are
  not checked.

  The injects directory is in --base-dir.injects when it is given on the
  command line, else in the base-dir.injects of the server, else in the
  local setting. When nothing exists for NAME, delete logs that there is
  nothing to delete and exits 0, so you can run it again.

  -n writes one "would ..." line for each item that delete would stop,
  delete or keep, and changes nothing. Progress is logged at the info
  level, and a kept injects directory at the warn level.`

	workflowDeleteExample = `
  # Delete the experiment and the injects of the branch name helloworld
  phenix workflow delete helloworld

  # Show what delete would remove; change nothing
  phenix workflow delete helloworld -n

  # Stop the running experiment, then delete it
  phenix workflow delete helloworld -f

  # Use a non-default socket
  phenix --unix-socket /run/phenix.sock workflow delete helloworld

  # Delete the injects in another injects directory
  phenix --base-dir.injects /srv/injects workflow delete helloworld

  # Compose deployment; the socket is inside the container
  docker exec phenix phenix workflow delete helloworld -f`
)

// deleteOptions is what runWorkflowDelete needs. The delete command fills it
// from its flags and the global settings; tests fill it directly. Out gets
// the lines of a dry run.
type deleteOptions struct {
	DryRun              bool
	Force               bool
	Socket              string
	InjectsBase         string
	InjectsBaseExplicit bool
	TopologiesBase      string
	Out                 io.Writer
}

func newWorkflowDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <NAME>",
		Short:   "Delete what workflow apply deployed for a branch name",
		Long:    workflowDeleteLong,
		Example: workflowDeleteExample,
		Args:    argsWithUsage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := deleteOptions{
				DryRun:              mustGet(cmd.Flags().GetBool, "dry-run"),
				Force:               mustGet(cmd.Flags().GetBool, "force"),
				Socket:              common.UnixSocket,
				InjectsBase:         common.InjectsBase,
				InjectsBaseExplicit: cmd.Flags().Changed(optionInjectsBase),
				TopologiesBase:      common.TopologiesBase,
				Out:                 cmd.OutOrStdout(),
			}

			ctx := sigterm.CancelContext(context.Background())

			return runWorkflowDelete(ctx, opts, args[0])
		},
	}

	cmd.Flags().BoolP("dry-run", "n", false, "show what the command would remove, and change nothing")
	cmd.Flags().BoolP("force", "f", false, "stop a running experiment before the delete")

	return cmd
}

// runWorkflowDelete deletes what workflow apply deployed for the branch name
// through the phenix server at opts.Socket: the mapped experiment, then the
// injects directory and its staging leftovers. It checks the name, the server
// and the injects before it sends the delete, and removes nothing locally when
// the server refuses the delete.
func runWorkflowDelete(ctx context.Context, opts deleteOptions, name string) error {
	if err := workflow.ValidateName(name); err != nil {
		return fmt.Errorf("give the branch name of a deployment: %w", err)
	}

	if isMarkerName(name) {
		return fmt.Errorf("the name %q is reserved; no deployment has it", name)
	}

	if opts.Socket == "" {
		return errors.New("no phenix unix socket configured; set --unix-socket")
	}

	client := newWorkflowClient(opts.Socket)

	base, err := deleteInjectsBase(ctx, client, opts)
	if err != nil {
		return err
	}

	var protect []string

	// The removal must not remove the working directory.
	if cwd, err := os.Getwd(); err == nil {
		protect = append(protect, cwd)
	}

	found, err := findInjectsToDelete(base, name, opts.TopologiesBase, protect)
	if err != nil {
		return err
	}

	result, err := client.delete(ctx, name, filepath.Join(base, name), opts.DryRun, opts.Force)
	if err != nil {
		return deleteRequestError(ctx, name, opts, err)
	}

	if opts.DryRun {
		writeDeleteDryRun(opts.Out, name, result, found)

		return nil
	}

	return finishWorkflowDelete(ctx, name, base, opts.TopologiesBase, protect, result)
}

// deleteInjectsBase checks that the server supports workflow delete, and
// returns the absolute injects base: --base-dir.injects when it was given on
// the command line, else the server's setting, else the local one.
func deleteInjectsBase(ctx context.Context, client *workflowClient, opts deleteOptions) (string, error) {
	serverOpts, err := client.options(ctx)
	if err != nil {
		return "", socketRequestError("reading the phenix server options", opts.Socket, err)
	}

	if supported, _ := serverOpts[optionWorkflowDelete].(bool); !supported {
		return "", fmt.Errorf("the phenix server at %s does not support workflow delete; upgrade the phenix server", opts.Socket)
	}

	// A relative directory would be resolved against the working directory.
	serverDir, _ := serverOpts[optionInjectsBase].(string)
	if !opts.InjectsBaseExplicit && serverDir != "" && !filepath.IsAbs(serverDir) {
		return "", fmt.Errorf("the phenix server reports the relative injects directory %q; pass --base-dir.injects", serverDir)
	}

	base := injectsBaseDir(opts.InjectsBase, opts.InjectsBaseExplicit, serverOpts)
	if base == "" {
		return "", errors.New("no injects directory configured; pass --base-dir.injects")
	}

	abs, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", base, err)
	}

	return abs, nil
}

// findInjectsToDelete returns the injects directory of name in base and its
// staging leftovers, as [workflow.FindStagedInjects] does. It also refuses a
// base or an injects directory that a wrong --base-dir.injects makes part of
// a topology directory, and an injects directory that is or holds the
// topologies directory topologies. It removes nothing.
func findInjectsToDelete(base, name, topologies string, protect []string) (workflow.Staged, error) {
	found, err := workflow.FindStagedInjects(base, name, protect...)
	if err != nil {
		return workflow.Staged{}, injectsDeleteError(name, base, err)
	}

	cwd, _ := os.Getwd()

	if found.Dir != "" && topologiesUnder(topologies, found.Dir, cwd) {
		return workflow.Staged{}, fmt.Errorf(
			"refusing to delete %s: it is or holds the topologies directory %s; check --base-dir.injects",
			filepath.Join(base, name), topologies,
		)
	}

	if err := checkTopologyMarkers(base, found.Dir); err != nil {
		return workflow.Staged{}, err
	}

	return found, nil
}

// checkTopologyMarkers refuses base when it holds an entry that marks a
// topology directory or lies inside such a directory. It refuses dir, unless
// it is "", missing or a symbolic link, when it or a directory in it holds
// such an entry.
func checkTopologyMarkers(base, dir string) error {
	if marker := topologyMarker(base); marker != "" {
		return fmt.Errorf("refusing to delete in %s: it holds %s, which marks a topology directory; check --base-dir.injects", base, marker)
	}

	if ancestor, marker := topologyAncestor(workflow.ResolvedPath(base)); ancestor != "" {
		return fmt.Errorf(
			"refusing to delete in %s: it lies inside %s, which holds %s, a name that marks a topology directory; "+
				"check --base-dir.injects",
			base, ancestor, marker,
		)
	}

	// A missing dir holds nothing, and the delete removes a link, not its target.
	if info, err := os.Lstat(dir); dir != "" && err == nil && info.Mode()&fs.ModeSymlink == 0 {
		return checkInjectsDirMarkers(dir)
	}

	return nil
}

// checkInjectsDirMarkers refuses dir when it or a directory in it holds an
// entry that marks a topology directory.
func checkInjectsDirMarkers(dir string) error {
	if marker := topologyMarker(dir); marker != "" {
		return fmt.Errorf("refusing to delete %s: it holds %s, which marks a topology directory; check --base-dir.injects", dir, marker)
	}

	// A directory that cannot be listed has no directory to look into.
	entries, _ := os.ReadDir(dir)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		if marker := topologyMarker(filepath.Join(dir, entry.Name())); marker != "" {
			return fmt.Errorf(
				"refusing to delete %s: its directory %s holds %s, which marks a topology directory; check --base-dir.injects",
				dir, entry.Name(), marker,
			)
		}
	}

	return nil
}

// writeDeleteDryRun writes one "would <action> <item>" line to w for each
// item that the delete would stop, delete or keep, and logs the end of the
// dry run.
func writeDeleteDryRun(w io.Writer, name string, result workflow.DeleteResult, found workflow.Staged) {
	would := func(action, item string) { fmt.Fprintf(w, "would %s %s\n", action, item) }

	if result.Stopped {
		would("stop", "experiment "+result.Experiment)
	}

	if result.Experiment != "" {
		would("delete", "experiment "+result.Experiment)
	}

	if found.Dir != "" {
		if len(result.InjectsUsedBy) > 0 {
			would("keep", fmt.Sprintf(
				"directory %s: the experiments %s inject files from it", found.Dir, strings.Join(result.InjectsUsedBy, ", "),
			))
		} else {
			would("delete", "directory "+found.Dir)
		}
	}

	for _, leftover := range found.Leftovers {
		would("delete", "staging leftover "+leftover)
	}

	if result.Experiment == "" && len(found.Paths()) == 0 {
		plog.Info(plog.TypeSystem, "nothing to delete for branch name "+name, "name", name)

		return
	}

	plog.Info(plog.TypeSystem, "dry run complete; nothing was changed", "name", name)
}

// finishWorkflowDelete logs what the server deleted, then removes the injects
// directory, unless other experiments use it, and the staging leftovers.
func finishWorkflowDelete(
	ctx context.Context, name, base, topologies string, protect []string, result workflow.DeleteResult,
) error {
	exp := result.Experiment

	if result.Stopped {
		plog.Info(plog.TypeSystem, "experiment stopped", "name", name, "exp", exp)
	}

	if exp != "" {
		plog.Info(plog.TypeSystem, "experiment deleted", "name", name, "exp", exp)
	} else {
		plog.Info(plog.TypeSystem, "no experiment is mapped to the branch name", "name", name)
	}

	removed, kept, err := removeInjects(ctx, name, base, topologies, protect, result.InjectsUsedBy)
	if err != nil {
		if exp != "" {
			err = fmt.Errorf("%w; already deleted: experiment %s; fix the cause and run the command again", err, exp)
		}

		return err
	}

	if exp == "" && !kept && len(removed.Paths()) == 0 {
		plog.Info(plog.TypeSystem, "nothing to delete for branch name "+name, "name", name)

		return nil
	}

	plog.Info(plog.TypeSystem, "workflow delete complete", "name", name)

	return nil
}

// removeInjects removes the injects directory of name in base and its staging
// leftovers, and logs each removal. It keeps the injects directory, and warns,
// when usedBy names other experiments. It returns what it removed and whether
// it kept the directory.
func removeInjects(ctx context.Context, name, base, topologies string, protect, usedBy []string) (workflow.Staged, bool, error) {
	if ctx.Err() != nil {
		return workflow.Staged{}, false, fmt.Errorf(
			"interrupted before the injects of branch name %s were deleted: %w", name, context.Cause(ctx),
		)
	}

	// The injects can change while the server deletes the experiment.
	found, err := findInjectsToDelete(base, name, topologies, protect)
	if err != nil {
		return workflow.Staged{}, false, err
	}

	keep := len(usedBy) > 0 && found.Dir != ""

	if keep {
		hint := "delete those experiments or remove their injections from that directory, then run phenix workflow delete " + name + " again"

		plog.Warn(
			plog.TypeSystem, "injects directory kept: other experiments inject files from it",
			"name", name, "dir", found.Dir, "experiments", strings.Join(usedBy, ", "), "hint", hint,
		)
	}

	removed, err := workflow.RemoveStagedInjects(base, name, workflow.RemoveOptions{KeepDir: keep, Protect: protect})

	if removed.Dir != "" {
		plog.Info(plog.TypeSystem, "injects directory deleted", "name", name, "dir", removed.Dir)
	}

	for _, leftover := range removed.Leftovers {
		plog.Info(plog.TypeSystem, "staging leftover deleted", "name", name, "path", leftover)
	}

	if err != nil {
		return removed, keep, injectsDeleteError(name, base, err)
	}

	return removed, keep, nil
}

// injectsDeleteError describes a failure to find or remove the injects of
// name in base, with a remedy for a permission error.
func injectsDeleteError(name, base string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("deleting the injects of branch name %s: %w; run phenix as a user who can write to %s", name, err, base)
	}

	return fmt.Errorf("deleting the injects of branch name %s: %w", name, err)
}

// deleteRequestError describes a failed delete request for name, and says
// what it deleted: nothing for a refusal (4xx), and not the injects for any
// other failure. With force, a refusal can follow the stop of the experiment.
// A real delete that got no answer may have deleted the experiment.
func deleteRequestError(ctx context.Context, name string, opts deleteOptions, err error) error {
	what := "deleting the workflow deployment of branch name " + name
	if opts.DryRun {
		what = "dry run of the delete of branch name " + name
	}

	var apiErr *workflowAPIError

	switch {
	case errors.Is(err, errServerUnreachable) && !opts.DryRun:
		return fmt.Errorf(
			"%w; the phenix server may have deleted the experiment; run the command again",
			socketRequestError(what, opts.Socket, err),
		)
	case errors.Is(err, errServerUnreachable):
		return socketRequestError(what, opts.Socket, err)
	case errors.As(err, &apiErr) && apiErr.Status < http.StatusInternalServerError:
		return fmt.Errorf("%s: %w; nothing was deleted", what, err)
	default:
		return mayStillFinish(ctx, fmt.Errorf("%s: %w; the injects were not deleted", what, err))
	}
}

// socketRequestError describes a failed request made while doing what,
// naming the socket when the server did not answer.
func socketRequestError(what, socket string, err error) error {
	if errors.Is(err, errServerUnreachable) {
		return fmt.Errorf("%s via %s: %w", what, socket, err)
	}

	return fmt.Errorf("%s: %w", what, err)
}
