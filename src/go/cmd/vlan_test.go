package cmd

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/activeshadow/structs"
	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"

	"phenix/store"
	"phenix/types"
)

// useVLANAliasExperiment points the store at a mock serving one stopped
// experiment with the given VLAN aliases, and reports any other experiment as
// missing.
func useVLANAliasExperiment(t *testing.T, name string, aliases map[string]int) {
	t.Helper()

	exp := types.NewExperiment(store.ConfigMetadata{Name: name})
	exp.Spec.SetExperimentName(name)
	exp.Spec.VLANs().SetAliases(aliases)

	cfg := store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: exp.Metadata,
		Spec:     structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status:   structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	}

	ctrl := gomock.NewController(t)
	m := store.NewMockStore(ctrl)

	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(c *store.Config) error {
		if c.Metadata.Name != name {
			return errors.New("config does not exist")
		}

		*c = cfg

		return nil
	}).AnyTimes()

	orig := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = orig }) //nolint:reassign // restore after test
}

func runVLANAliasCmd(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	vlanCmd := newVlanCmd()
	vlanCmd.AddCommand(newVlanAliasCmd())
	root.AddCommand(vlanCmd)
	root.SetArgs(append([]string{"vlan", "alias"}, args...))

	var out bytes.Buffer

	root.SetOut(&out)
	root.SetErr(new(bytes.Buffer))

	_, err := root.ExecuteC()

	return out.String(), err
}

func TestVLANAliasLookup(t *testing.T) {
	useVLANAliasExperiment(t, "my-experiment", map[string]int{"MYVLAN": 105, "AUTO": 0})

	out, err := runVLANAliasCmd("my-experiment", "MYVLAN")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if out != "105\n" {
		t.Errorf("expected output %q, got %q", "105\n", out)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing alias",
			args: []string{"my-experiment", "OTHER"},
			want: "experiment my-experiment has no VLAN alias OTHER",
		},
		{
			name: "unassigned alias",
			args: []string{"my-experiment", "AUTO"},
			want: "experiment my-experiment assigns VLAN alias AUTO an ID when it starts",
		},
		{
			name: "missing experiment",
			args: []string{"missing", "MYVLAN"},
			want: "Unable to get VLAN alias MYVLAN for the missing experiment",
		},
		{
			name: "too many arguments",
			args: []string{"my-experiment", "MYVLAN", "105", "extra"},
			want: "unexpected number of arguments",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, err := runVLANAliasCmd(test.args...)
			if err == nil {
				t.Fatalf("expected an error, got output %q", out)
			}

			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("expected error to contain %q, got %q", test.want, err)
			}

			if out != "" {
				t.Errorf("expected no output, got %q", out)
			}
		})
	}
}

func TestVLANAliasCompletion(t *testing.T) {
	useVLANAliasExperiment(t, "my-experiment", map[string]int{"MYVLAN": 105, "MGMT": 101, "EXP-1": 0})

	cmd := newVlanAliasCmd()

	tests := []struct {
		name       string
		args       []string
		toComplete string
		want       []string
		directive  cobra.ShellCompDirective
	}{
		{
			name:      "all aliases",
			args:      []string{"my-experiment"},
			want:      []string{"EXP-1", "MGMT", "MYVLAN"},
			directive: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:       "alias prefix",
			args:       []string{"my-experiment"},
			toComplete: "M",
			want:       []string{"MGMT", "MYVLAN"},
			directive:  cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name:      "missing experiment",
			args:      []string{"missing"},
			directive: cobra.ShellCompDirectiveError,
		},
		{
			name:      "vlan id",
			args:      []string{"my-experiment", "MYVLAN"},
			directive: cobra.ShellCompDirectiveNoFileComp,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, directive := vlanAliasArgsCompletion(cmd, test.args, test.toComplete)

			if !slices.Equal(got, test.want) {
				t.Errorf("expected completions %q, got %q", test.want, got)
			}

			if directive != test.directive {
				t.Errorf("expected directive %d, got %d", test.directive, directive)
			}
		})
	}
}
