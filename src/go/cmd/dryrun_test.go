package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"phenix/api/config"
	"phenix/store/storetest"
)

// dryRunCommands are the commands that take the --dry-run of addDryRunFlag.
// Add each destructive command to it when it gets the flag.
func dryRunCommands() []string {
	return []string{
		"phenix admin role delete",
		"phenix admin user delete",
		"phenix config apply",
		"phenix config delete",
		"phenix config delete all",
		"phenix disk commit",
		"phenix disk delete",
		"phenix disk rebase",
		"phenix disk rename",
		"phenix disk resize",
		"phenix experiment delete",
		"phenix experiment files delete",
		"phenix experiment scorch",
		"phenix experiment trigger",
		"phenix image delete",
		"phenix job cancel",
		"phenix settings unset",
		"phenix vlan alias",
		"phenix vlan blacklist remove",
		"phenix vlan range",
		"phenix vlan release",
		"phenix vm capture stop",
		"phenix vm capture stop-all",
		"phenix vm capture stop-subnet",
		"phenix vm capture stop-vlan",
		"phenix vm forward remove",
		"phenix vm kill",
		"phenix vm net disconnect",
		"phenix vm reset-disk",
		"phenix vm shutdown",
		"phenix vm snapshot delete",
	}
}

func TestDryRunSkip(t *testing.T) {
	t.Parallel()

	for _, dry := range []bool{false, true} {
		cmd := &cobra.Command{Use: "x"}
		d := addDryRunFlag(cmd)

		var out bytes.Buffer
		cmd.SetOut(&out)

		if dry {
			if err := cmd.Flags().Set(dryRunFlagName, "true"); err != nil {
				t.Fatalf("setting --dry-run: %v", err)
			}
		}

		if got := d.skip("delete", "experiment foo"); got != dry {
			t.Errorf("skip(delete, experiment foo) with --dry-run=%t = %t, want %t", dry, got, dry)
		}

		want := ""
		if dry {
			want = "would delete experiment foo\n"
		}

		if diff := cmp.Diff(want, out.String()); diff != "" {
			t.Errorf("skip output with --dry-run=%t mismatch (-want +got):\n%s", dry, diff)
		}
	}
}

// Each command in dryRunCommands has the --dry-run of addDryRunFlag.
func TestDryRunCommandsHaveFlag(t *testing.T) { //nolint:paralleltest // rootCmd.Find changes the flags of the command tree
	for _, path := range dryRunCommands() {
		cmd := commandAtPath(t, path)

		f := cmd.Flags().Lookup(dryRunFlagName)
		if f == nil || f.Usage != dryRunFlagUsage || f.Value.Type() != "bool" {
			t.Errorf("%s --dry-run = %+v, want the flag of addDryRunFlag", path, f)
		}
	}
}

// experiment delete --dry-run shows the experiments that it would stop and
// delete, and changes none of them.
func TestExpCmdDeleteDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		args      []string
		want      string
		humanized string
	}{
		{name: "stopped", args: []string{"delete", "stopped", "--dry-run"}, want: "would delete experiment stopped\n"},
		{name: "running without --force", args: []string{"delete", "running", "--dry-run"}},
		{
			name: "running with --force", args: []string{"delete", "running", "--force", "--dry-run"},
			want: "would stop experiment running\nwould delete experiment running\n",
		},
		{
			name: "all with --force", args: []string{"delete", "all", "-f", "--dry-run"},
			want: "would stop experiment dry\nwould delete experiment dry\n" +
				"would stop experiment running\nwould delete experiment running\n" +
				"would delete experiment stopped\n",
		},
		{
			name: "missing", args: []string{"delete", "missing", "--dry-run"},
			humanized: "Unable to delete the missing experiment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expCmdFixture(t, nil)

			out, err := expCmdOutput(tt.args...)
			if msg := expCmdCheckErr(err, "", tt.humanized); msg != "" {
				t.Errorf("experiment %q %s", tt.args, msg)
			}

			if diff := cmp.Diff(tt.want, out); diff != "" {
				t.Errorf("experiment %q output mismatch (-want +got):\n%s", tt.args, diff)
			}

			for name, want := range map[string]string{"stopped": "stopped", "running": "running", "dry": "dry-run"} {
				if got := expCmdState(t, name); got != want {
					t.Errorf("experiment %q left %s %s, want %s", tt.args, name, got, want)
				}
			}
		})
	}
}

// config delete --dry-run and config delete all --dry-run show the configs
// that they would delete, and delete none of them.
func TestConfigDeleteDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{
			name: "two configs", args: []string{"delete", "topology/alpha", "image/disk", "--dry-run"},
			want: "would delete configuration topology/alpha\nwould delete configuration image/disk\n",
		},
		{
			name: "missing config", args: []string{"delete", "topology/alpha", "topology/none", "--dry-run"},
			wantErr: "Unable to delete the topology/none configuration",
		},
		{
			name: "all of a kind", args: []string{"delete", "all", "image", "--dry-run"},
			want: "would delete configuration image/disk\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storetest.UseBolt(t)
			storeConfigTestConfigs(t, "topology/alpha", "image/disk")

			out, err := runConfigTestCommand(tt.args...)

			if tt.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
					t.Errorf("config %q error = %v, want %q", tt.args, err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("config %q error = %v", tt.args, err)
			}

			if tt.wantErr == "" {
				if diff := cmp.Diff(tt.want, out); diff != "" {
					t.Errorf("config %q output mismatch (-want +got):\n%s", tt.args, diff)
				}
			}

			for _, name := range []string{"topology/alpha", "image/disk"} {
				if _, err := config.Get(name); err != nil {
					t.Errorf("config %q deleted %s: config.Get error = %v", tt.args, name, err)
				}
			}
		})
	}

	t.Run("all", func(t *testing.T) {
		storetest.UseBolt(t)
		storeConfigTestConfigs(t, "topology/alpha", "image/disk")

		configs, err := config.List(allExperiments)
		if err != nil {
			t.Fatalf("config.List(all) error = %v", err)
		}

		var want strings.Builder
		for _, c := range configs {
			want.WriteString("would delete configuration " + strings.ToLower(c.Kind) + "/" + c.Metadata.Name + "\n")
		}

		out, err := runConfigTestCommand("delete", "all", "--dry-run")
		if err != nil {
			t.Fatalf("config delete all --dry-run error = %v", err)
		}

		if diff := cmp.Diff(want.String(), out); diff != "" {
			t.Errorf("config delete all --dry-run output mismatch (-want +got):\n%s", diff)
		}

		after, err := config.List(allExperiments)
		if err != nil || len(after) != len(configs) {
			t.Errorf("config delete all --dry-run left %d configs (error %v), want %d", len(after), err, len(configs))
		}
	})
}

// commandAtPath returns the command of the phenix command tree at path, such
// as "phenix config delete".
func commandAtPath(t *testing.T, path string) *cobra.Command {
	t.Helper()

	names := strings.Fields(path)
	if len(names) == 0 || names[0] != rootCmd.Name() {
		t.Fatalf("command path %q does not start with %q", path, rootCmd.Name())
	}

	cmd, rest, err := rootCmd.Find(names[1:])
	if err != nil || len(rest) > 0 || cmd.CommandPath() != path {
		t.Fatalf("rootCmd.Find(%q) = %s, %q, %v; want the command", names[1:], cmd.CommandPath(), rest, err)
	}

	return cmd
}
