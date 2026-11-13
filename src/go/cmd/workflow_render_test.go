package cmd

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"phenix/api/workflow"
	"phenix/store"
	"phenix/util/envsubst"
)

// renderTestOptions returns the options of a plain render, with env as the
// -e values and shell as the shell.
func renderTestOptions(env, shell []string) sourceOptions {
	return sourceOptions{
		Name:           "",
		Config:         workflowConfigName,
		ConfigExplicit: false,
		TopologiesBase: "",
		Env:            env,
		EnvFiles:       nil,
		Environ:        shell,
	}
}

// renderTestFiles returns a topology directory whose files use variables, the
// branch name, an escape, and a value that holds the branch name.
func renderTestFiles() map[string]string {
	return map[string]string{
		".env": "SITE=lab\nNODES=from-the-file\n",
		"phenix-configs/topology.yml": "kind: Topology\nmetadata:\n  name: ${SITE}-${BRANCH_NAME}\n" +
			"spec:\n  count: ${NODES}\n  dir: ${DIR}\n  keep: $${NODES}\n",
		"phenix-configs/image.json":   `{"kind": "Image", "metadata": {"name": "${BRANCH_NAME:x}"}, "spec": {"size": ${SIZE:4}}}`,
		"phenix-injects/scripts/a.sh": "echo ${NODES}\n",
		workflowConfigName:            "kind: Workflow\nspec:\n  auto:\n    create: ${BRANCH_NAME}\n  topology: ${SITE}-${BRANCH_NAME}\n",
	}
}

func TestRunWorkflowRender(t *testing.T) {
	dir := newTopologyDir(t, "helloworld", renderTestFiles())

	opts := renderTestOptions([]string{"NODES=3", "DIR=/phenix/injects/${BRANCH_NAME}"}, []string{"BRANCH_NAME=shell", "SITE=shell"})
	opts.Name = "hw-2"

	var out bytes.Buffer

	if err := runWorkflowRender(&out, opts, dir); err != nil {
		t.Fatalf("runWorkflowRender() error = %v", err)
	}

	want := "# phenix-configs/topology.yml\n" +
		"kind: Topology\nmetadata:\n  name: lab-hw-2\nspec:\n  count: 3\n  dir: /phenix/injects/hw-2\n  keep: ${NODES}\n" +
		"---\n# phenix-configs/image.json\n" +
		`{"kind": "Image", "metadata": {"name": "hw-2"}, "spec": {"size": 4}}` + "\n" +
		"---\n# phenix.yml\n" +
		"kind: Workflow\nspec:\n  auto:\n    create: hw-2\n  topology: lab-hw-2\n"

	if diff := cmp.Diff(want, out.String()); diff != "" {
		t.Errorf("runWorkflowRender() output mismatch (-want +got):\n%s", diff)
	}
}

// TestRunWorkflowRenderMatchesApply checks that render prints each file as
// the server has it after an apply: the body that apply sends, with
// ${BRANCH_NAME} filled in as the server does it.
func TestRunWorkflowRenderMatchesApply(t *testing.T) {
	dir := newTopologyDir(t, "helloworld", renderTestFiles())
	env := []string{"NODES=3", "DIR=/phenix/injects/${BRANCH_NAME}"}

	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(t.TempDir()),
		plan:    workflow.Plan{Action: workflow.ActionCreate, Experiment: "helloworld", Reason: ""},
	})

	applyOpts := testApplyOptions(fake.socket, t.TempDir())
	applyOpts.Env, applyOpts.DryRun = env, true

	if err := runWorkflowApply(t.Context(), applyOpts, dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	paths := []string{"phenix-configs/topology.yml", "phenix-configs/image.json", workflowConfigName}
	branch := envsubst.Fixed("helloworld")

	var want strings.Builder

	// The first request reads the server options. The dry runs follow, one
	// for each file, in the order that render prints.
	for i, req := range fake.recorded()[1:] {
		if i > 0 {
			want.WriteString("---\n")
		}

		stored := string(store.ExpandBranchName([]byte(req.body), branch))
		want.WriteString("# " + filepath.FromSlash(paths[i]) + "\n" + strings.TrimSuffix(stored, "\n") + "\n")
	}

	var out bytes.Buffer

	if err := runWorkflowRender(&out, renderTestOptions(env, nil), dir); err != nil {
		t.Fatalf("runWorkflowRender() error = %v", err)
	}

	if diff := cmp.Diff(want.String(), out.String()); diff != "" {
		t.Errorf("runWorkflowRender() output differs from what apply sends (-apply +render):\n%s", diff)
	}
}

func TestRunWorkflowRenderErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		env     []string
		wantErr string
		wantIs  error
	}{
		{
			name:    "only injects",
			files:   map[string]string{"phenix-injects/a.sh": "echo\n", "phenix-configs/notes.txt": "no config\n"},
			wantErr: "nothing to render: ",
		},
		{name: "a variable that is not set", files: renderTestFiles(), env: []string{"NODES=3"}, wantIs: errVariables},
		{
			name:   "a -e variable that no file uses",
			files:  renderTestFiles(),
			env:    []string{"NODES=3", "DIR=/d", "EXTRA=1"},
			wantIs: errVariables,
		},
		{name: "-e sets the branch name", files: renderTestFiles(), env: []string{"BRANCH_NAME=lab"}, wantIs: envsubst.ErrReserved},
		{
			name:    "a config that is not a mapping",
			files:   map[string]string{"phenix-configs/bad.yml": "- a\n"},
			wantErr: "bad.yml:1:1: expected a mapping at the top level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "helloworld", tt.files)

			var out bytes.Buffer

			err := runWorkflowRender(&out, renderTestOptions(tt.env, nil), dir)

			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("runWorkflowRender() error = %v, want %v", err, tt.wantIs)
			}

			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("runWorkflowRender() error = %v, want it to contain %q", err, tt.wantErr)
			}

			if err == nil || out.Len() != 0 {
				t.Errorf("runWorkflowRender() = %v with %d bytes of output, want an error and no output", err, out.Len())
			}
		})
	}
}

func TestWorkflowRenderCommand(t *testing.T) {
	dir := newTopologyDir(t, "helloworld", map[string]string{
		"alt.yml": "kind: Workflow\nspec:\n  topology: ${BRANCH_NAME}-${SITE}\n",
	})

	root := &cobra.Command{Use: appName, SilenceUsage: true, SilenceErrors: true}
	workflowCmd := newWorkflowCmd()
	workflowCmd.AddCommand(newWorkflowRenderCmd())
	root.AddCommand(workflowCmd)
	root.SetArgs([]string{"workflow", "render", dir, "-b", "hw-2", "-c", "alt.yml", "-e", "SITE=lab"})

	var out bytes.Buffer

	root.SetOut(&out)

	if err := root.Execute(); err != nil {
		t.Fatalf("phenix workflow render error = %v", err)
	}

	if want := "# alt.yml\nkind: Workflow\nspec:\n  topology: hw-2-lab\n"; out.String() != want {
		t.Errorf("phenix workflow render output = %q, want %q", out.String(), want)
	}

	cmd := newWorkflowRenderCmd()

	for _, name := range []string{optionBranchName, optionBranchNameAlias, "config", envFlagName, envFileFlagName} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s is missing", name)
		}
	}

	for _, name := range []string{"dry-run", "force", optionFollowInjectSymlinks} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("render has the apply flag --%s", name)
		}
	}

	if err := cmd.ValidateArgs(nil); err == nil {
		t.Error("render accepted no argument")
	}

	lines := map[string]bool{}
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		lines[strings.TrimSpace(line)] = true
	}

	for _, example := range []string{
		"phenix workflow render helloworld",
		"phenix workflow render helloworld -b helloworld-2 -e NODES=3",
		"phenix workflow render ./topo --env-file site.env -c alt.yml",
	} {
		if !lines[example] {
			t.Errorf("Example is missing %q", example)
		}
	}
}
