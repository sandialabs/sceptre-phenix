package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"phenix/api/workflow"
	"phenix/util/common"
)

// applyExample is one example of the apply command's help, run against a
// fake server. setup adjusts the options and returns the argument. want is
// the requests, wantBody the body of every workflow apply request, and
// staged the directory expected under the server's injects directory ("" for
// none).
type applyExample struct {
	name     string
	files    map[string]string
	setup    func(t *testing.T, dir string, opts *applyOptions) string
	want     []string
	wantBody string
	staged   string
}

// runApplyExample runs ex on a topology directory called helloworld and
// checks its requests, bodies and staged injects.
func runApplyExample(t *testing.T, ex applyExample) {
	t.Helper()

	dir := newTopologyDir(t, "helloworld", ex.files)
	injects := t.TempDir()
	fake := newFakePhenix(t, fakePhenix{
		options: serverOptions(injects),
		plan:    workflow.Plan{Action: workflow.ActionCreateAndStart, Experiment: "helloworld", Reason: ""},
	})

	opts := testApplyOptions(fake.socket, t.TempDir())
	arg := ex.setup(t, dir, &opts)

	if err := runWorkflowApply(t.Context(), opts, arg); err != nil {
		t.Fatalf("runWorkflowApply(%q) error = %v", arg, err)
	}

	checkRequests(t, fake, ex.want...)

	for _, req := range fake.recorded() {
		if strings.HasPrefix(req.path, "/api/v1/workflow/apply/") && req.body != ex.wantBody {
			t.Errorf("%s body = %q, want %q", req.path, req.body, ex.wantBody)
		}
	}

	if ex.staged == "" {
		assertEmptyDir(t, injects)

		return
	}

	if _, err := os.Stat(filepath.Join(injects, ex.staged, "scripts", "hello.sh")); err != nil {
		t.Errorf("injects not staged in %s: %v", filepath.Join(injects, ex.staged), err)
	}
}

func TestRunWorkflowApplyResolvesTheArgument(t *testing.T) {
	examples := []applyExample{
		{
			name:  "name lookup from any directory",
			files: fullTopology(),
			setup: func(t *testing.T, dir string, opts *applyOptions) string {
				t.Helper()

				opts.TopologiesBase = filepath.Dir(dir)
				t.Chdir(t.TempDir())

				return "helloworld"
			},
			want:     fullRun("helloworld", workflow.ActionCreateAndStart),
			wantBody: wfWorkflowYAML,
			staged:   "helloworld",
		},
		{
			name:  "current directory",
			files: fullTopology(),
			setup: func(t *testing.T, dir string, _ *applyOptions) string {
				t.Helper()

				t.Chdir(dir)

				return "."
			},
			want:     fullRun("helloworld", workflow.ActionCreateAndStart),
			wantBody: wfWorkflowYAML,
			staged:   "helloworld",
		},
	}

	for _, ex := range examples {
		t.Run(ex.name, func(t *testing.T) { runApplyExample(t, ex) })
	}
}

func TestRunWorkflowApplyFlagExamples(t *testing.T) {
	alt := strings.Replace(wfWorkflowYAML, "create: foo", "create: alt", 1)
	withAlt := fullTopology()
	withAlt["alt.yml"] = alt

	examples := []applyExample{
		{
			name:  "another branch name",
			files: fullTopology(),
			setup: func(t *testing.T, dir string, opts *applyOptions) string {
				t.Helper()

				opts.Name = "helloworld-2"

				return dir
			},
			want:     fullRun("helloworld-2", workflow.ActionCreateAndStart),
			wantBody: wfWorkflowYAML,
			staged:   "helloworld-2",
		},
		{
			name:  "another workflow config",
			files: withAlt,
			setup: func(t *testing.T, dir string, opts *applyOptions) string {
				t.Helper()

				opts.Config, opts.ConfigExplicit = "alt.yml", true

				return dir
			},
			want:     fullRun("helloworld", workflow.ActionCreateAndStart),
			wantBody: alt,
			staged:   "helloworld",
		},
		{
			name:  "dry run of another config under another name",
			files: withAlt,
			setup: func(t *testing.T, dir string, opts *applyOptions) string {
				t.Helper()

				opts.Name, opts.Config, opts.ConfigExplicit, opts.DryRun = "hw-test", "alt.yml", true, true

				return dir
			},
			want:     fullPreflight("hw-test"),
			wantBody: alt,
		},
	}

	for _, ex := range examples {
		t.Run(ex.name, func(t *testing.T) { runApplyExample(t, ex) })
	}
}

func TestWorkflowApplyCommand(t *testing.T) {
	cmd := newWorkflowApplyCmd()

	if cmd.Use != "apply <DIR|NAME>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "apply <DIR|NAME>")
	}

	flags := []struct {
		name, shorthand, def string
		hidden               bool
	}{
		{name: optionBranchName, shorthand: "b", def: "", hidden: false},
		{name: optionBranchNameAlias, shorthand: "", def: "", hidden: true},
		{name: "config", shorthand: "c", def: workflowConfigName, hidden: false},
		{name: "dry-run", shorthand: "n", def: "false", hidden: false},
		{name: "force", shorthand: "f", def: "false", hidden: false},
	}

	for _, want := range flags {
		flag := cmd.Flags().Lookup(want.name)
		if flag == nil {
			t.Errorf("flag --%s is missing", want.name)

			continue
		}

		if flag.Shorthand != want.shorthand || flag.DefValue != want.def {
			t.Errorf("flag --%s = -%s default %q, want -%s default %q", want.name, flag.Shorthand, flag.DefValue, want.shorthand, want.def)
		}

		if flag.Hidden != want.hidden {
			t.Errorf("flag --%s hidden = %t, want %t", want.name, flag.Hidden, want.hidden)
		}
	}

	for _, args := range [][]string{nil, {"a", "b"}} {
		if err := cmd.ValidateArgs(args); err == nil {
			t.Errorf("apply accepted %d arguments", len(args))
		}
	}

	if err := cmd.ValidateArgs([]string{"a"}); err != nil {
		t.Errorf("apply rejected one argument: %v", err)
	}

	lines := map[string]bool{}
	for line := range strings.SplitSeq(cmd.Example, "\n") {
		lines[strings.TrimSpace(line)] = true
	}

	for _, example := range []string{
		"phenix workflow apply /phenix/topologies/helloworld",
		"phenix workflow apply helloworld",
		"phenix workflow apply .",
		"phenix workflow apply helloworld -n",
		"phenix workflow apply helloworld -f",
		"phenix workflow apply helloworld -b helloworld-2",
		"phenix workflow apply helloworld -c alt.yml",
		"phenix workflow apply helloworld -b hw-test -c alt.yml -n",
		"phenix workflow apply ./topo",
		"phenix --unix-socket /run/phenix.sock workflow apply helloworld",
		"phenix --base-dir.topologies /srv/topologies workflow apply helloworld",
		"PHENIX_BASE_DIR_TOPOLOGIES=/srv/topologies phenix workflow apply helloworld",
		"phenix --base-dir.injects /srv/injects workflow apply helloworld",
		"phenix --log.level debug workflow apply helloworld -n",
		"docker exec phenix phenix workflow apply /phenix/topologies/helloworld -f",
	} {
		if !lines[example] {
			t.Errorf("Example is missing %q", example)
		}
	}

	// The help text with its line breaks and indents taken out.
	long := strings.Join(strings.Fields(cmd.Long), " ")
	reserved := "The names phenix.yml, phenix-configs and phenix-injects are reserved, in any letter case: " +
		"they cannot be the workflow's name, and no entry of phenix-injects/ may have one of them at its top level " +
		"or directly inside one of its top-level directories."

	if !strings.Contains(long, reserved) {
		t.Errorf("Long does not say %q", reserved)
	}

	// The help numbers the steps by the values the step field logs.
	for i, step := range []string{stepPreflight, stepPlan, stepInjects, stepConfigs, stepApply} {
		if want := fmt.Sprintf("%d. %s:", i+1, step); !strings.Contains(long, want) {
			t.Errorf("Long does not name step %q as %q", step, want)
		}
	}

	parent := newWorkflowCmd()

	var help bytes.Buffer

	parent.SetOut(&help)

	if err := parent.RunE(parent, nil); err != nil || !strings.Contains(help.String(), "Workflow management") {
		t.Errorf("workflow RunE() = %v, help = %q", err, help.String())
	}
}

func TestWorkflowApplyRequiresArgument(t *testing.T) {
	root := &cobra.Command{Use: appName, SilenceUsage: true}
	workflowCmd := newWorkflowCmd()
	workflowCmd.AddCommand(newWorkflowApplyCmd())
	root.AddCommand(workflowCmd)
	root.SetArgs([]string{"workflow", "apply"})

	var output bytes.Buffer

	root.SetOut(&output)
	root.SetErr(&output)

	if _, err := root.ExecuteC(); err == nil || !strings.Contains(err.Error(), "accepts 1 arg(s), received 0") {
		t.Fatalf("workflow apply error = %v, want a missing argument error", err)
	}

	if want := "Usage:\n  phenix workflow apply <DIR|NAME>"; !strings.Contains(output.String(), want) {
		t.Errorf("output = %q, want it to contain %q", output.String(), want)
	}
}

func TestApplyOptionsFromFlags(t *testing.T) {
	oldSocket, oldInjects, oldTopologies := common.UnixSocket, common.InjectsBase, common.TopologiesBase

	// The root command's PersistentPreRunE sets these from the global flags,
	// the settings and the environment; it does not run in this test.
	common.UnixSocket = "/run/phenix.sock"    //nolint:reassign // install test double
	common.InjectsBase = "/srv/injects"       //nolint:reassign // install test double
	common.TopologiesBase = "/srv/topologies" //nolint:reassign // install test double

	t.Cleanup(func() {
		common.UnixSocket = oldSocket         //nolint:reassign // restore test double
		common.InjectsBase = oldInjects       //nolint:reassign // restore test double
		common.TopologiesBase = oldTopologies //nolint:reassign // restore test double
	})

	defaults := applyOptions{
		Config:         workflowConfigName,
		Socket:         "/run/phenix.sock",
		InjectsBase:    "/srv/injects",
		TopologiesBase: "/srv/topologies",
	}

	// globals holds the value each global flag must parse to, and wantErr
	// the error applyOptionsFromFlags must return instead of options.
	tests := []struct {
		name    string
		args    []string
		globals map[string]string
		edit    func(opts *applyOptions)
		wantErr string
	}{
		{name: "defaults", args: []string{"workflow", "apply", "helloworld"}},
		{
			name: "force",
			args: []string{"workflow", "apply", "helloworld", "-f"},
			edit: func(opts *applyOptions) { opts.Force = true },
		},
		{
			name: "name, config and dry run",
			args: []string{"workflow", "apply", "helloworld", "-b", "hw-test", "-c", "alt.yml", "-n"},
			edit: func(opts *applyOptions) {
				opts.Name, opts.Config, opts.ConfigExplicit, opts.DryRun = "hw-test", "alt.yml", true, true
			},
		},
		{
			name: "branch name by its long flag",
			args: []string{"workflow", "apply", "helloworld", "--branch-name", "hw-test"},
			edit: func(opts *applyOptions) { opts.Name = "hw-test" },
		},
		{
			name: "name is still accepted as an alias of branch-name",
			args: []string{"workflow", "apply", "helloworld", "--name", "hw-test"},
			edit: func(opts *applyOptions) { opts.Name = "hw-test" },
		},
		{
			name: "branch-name and name with the same value",
			args: []string{"workflow", "apply", "helloworld", "-b", "hw-test", "--name", "hw-test"},
			edit: func(opts *applyOptions) { opts.Name = "hw-test" },
		},
		{
			name:    "branch-name and name with different values",
			args:    []string{"workflow", "apply", "helloworld", "-b", "hw-test", "--name", "hw-other"},
			wantErr: `--branch-name "hw-test" and --name "hw-other" differ; --name is an alias of --branch-name, so pass only one of them`,
		},
		{
			name: "global flags reach apply through the globals",
			args: []string{
				"--unix-socket", "/run/phenix.sock", "--base-dir.topologies", "/srv/topologies",
				"workflow", "apply", "helloworld",
			},
			globals: map[string]string{"unix-socket": "/run/phenix.sock", "base-dir.topologies": "/srv/topologies"},
		},
		{
			name:    "explicit injects base",
			args:    []string{"--base-dir.injects", "/srv/injects", "workflow", "apply", "helloworld"},
			globals: map[string]string{optionInjectsBase: "/srv/injects"},
			edit:    func(opts *applyOptions) { opts.InjectsBaseExplicit = true },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: appName}
			root.PersistentFlags().String("unix-socket", "/tmp/phenix.sock", "")
			root.PersistentFlags().String(optionInjectsBase, "", "")
			root.PersistentFlags().String("base-dir.topologies", "", "")

			workflowCmd := newWorkflowCmd()
			workflowCmd.AddCommand(newWorkflowApplyCmd())
			root.AddCommand(workflowCmd)

			cmd, rest, err := root.Find(tt.args)
			if err != nil {
				t.Fatalf("finding the command: %v", err)
			}

			if cmd.CommandPath() != "phenix workflow apply" {
				t.Fatalf("Find() = %q, want phenix workflow apply", cmd.CommandPath())
			}

			// Find removes only the command names. A global flag's value stays
			// with its flag, since every level knows the root's persistent flags.
			wantRest := slices.DeleteFunc(slices.Clone(tt.args), func(arg string) bool { return arg == "workflow" || arg == "apply" })
			if !slices.Equal(rest, wantRest) {
				t.Fatalf("Find() left %q, want %q", rest, wantRest)
			}

			if err := cmd.ParseFlags(rest); err != nil {
				t.Fatalf("parsing flags: %v", err)
			}

			for name, value := range tt.globals {
				if got, err := cmd.Flags().GetString(name); err != nil || got != value {
					t.Errorf("--%s = %q, %v; want %q", name, got, err, value)
				}
			}

			got, err := applyOptionsFromFlags(cmd)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("applyOptionsFromFlags() error = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("applyOptionsFromFlags() error = %v", err)
			}

			if got.Now == nil {
				t.Error("Now is nil")
			}

			got.Now = nil

			want := defaults
			if tt.edit != nil {
				tt.edit(&want)
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("applyOptionsFromFlags() = %+v, want %+v", got, want)
			}

			if args := cmd.Flags().Args(); !slices.Equal(args, []string{"helloworld"}) {
				t.Errorf("positional arguments = %q, want [helloworld]", args)
			}
		})
	}
}
