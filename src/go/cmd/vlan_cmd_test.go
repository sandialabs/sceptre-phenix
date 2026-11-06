package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"

	"phenix/store"
	"phenix/store/storetest"
)

// vlanCmdExperiment returns the stored config of the stopped vmCmdExperiment
// named name, with the given VLAN aliases and range.
func vlanCmdExperiment(t *testing.T, name string, aliases map[string]int, minID, maxID int) store.Config {
	t.Helper()

	cfg := vmCmdExperiment(t, name, false)
	cfg.Spec["vlans"] = map[string]any{"aliases": aliases, "min": minID, "max": maxID}

	return cfg
}

// vlanCmdUseFailingStore makes a store that cannot list configs the phenix
// store until the test ends.
func vlanCmdUseFailingStore(t *testing.T) {
	t.Helper()

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().List(gomock.Any()).Return(nil, errors.New("store down")).AnyTimes()

	storetest.Use(t, m)
}

// vlanCmdRun runs phenix vlan with args and returns what the command writes
// to its output, and its error.
func vlanCmdRun(args ...string) (string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	vlanCmd := newVlanCmd()
	vlanCmd.AddCommand(newVlanAliasCmd(), newVlanRangeCmd())
	root.AddCommand(vlanCmd)
	root.SetArgs(append([]string{"vlan"}, args...))

	var out bytes.Buffer

	root.SetOut(&out)
	root.SetErr(io.Discard)

	_, err := root.ExecuteC()

	return out.String(), err
}

// vlanCmdCheck reports how err differs from a nil error, an error with the
// text wantErr, or a humanized error that starts with humanized.
func vlanCmdCheck(t *testing.T, args []string, err error, wantErr, humanized string) {
	t.Helper()

	if msg := expCmdCheckErr(err, wantErr, humanized); msg != "" {
		t.Errorf("phenix vlan %s %s", strings.Join(args, " "), msg)
	}

	// a humanized error is used as text, never as a format
	if err != nil && strings.Contains(err.Error(), "%!") {
		t.Errorf("phenix vlan %s error = %q, want no formatting verb errors", strings.Join(args, " "), err)
	}
}

// phenix vlan alias lists the aliases of every experiment or of one, and sets
// an alias, replacing one only with --force.
func TestVLANCmdAlias(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t,
		vlanCmdExperiment(t, "alpha", map[string]int{"EXP_1": 101}, 0, 0),
		vlanCmdExperiment(t, "beta", map[string]int{"EXP_2": 202}, 0, 0),
	)

	tests := []struct {
		name      string
		args      []string
		wantErr   string
		humanized string
		has       []string
		hasNot    []string
	}{
		{name: "all experiments", args: []string{"alias"}, has: []string{"alpha", "beta", "EXP_1", "101", "EXP_2", "202"}},
		{name: "one experiment", args: []string{"alias", "alpha"}, has: []string{"alpha", "EXP_1", "101"}, hasNot: []string{"beta"}},
		{name: "missing experiment", args: []string{"alias", "missing"}, humanized: "Unable to display aliases for the missing experiment"},
		{name: "set", args: []string{"alias", "alpha", "NEW", "150"}},
		{name: "set again", args: []string{"alias", "alpha", "NEW", "151"}, humanized: "Unable to set the alias for the alpha experiment"},
		{name: "set with force", args: []string{"alias", "alpha", "NEW", "151", "--force"}},
		{
			name:      "set a missing experiment",
			args:      []string{"alias", "100%d", "NEW", "151"},
			humanized: "Unable to set the alias for the 100%d experiment",
		},
		{
			name:    "ID is not a number",
			args:    []string{"alias", "alpha", "NEW", "x"},
			wantErr: "the VLAN identifier provided is not a valid integer",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, err := vlanCmdRun(test.args...)
			vlanCmdCheck(t, test.args, err, test.wantErr, test.humanized)

			for _, cell := range test.has {
				if !vmCmdTableHas(out, cell) {
					t.Errorf("phenix vlan %s output has no cell %q:\n%s", strings.Join(test.args, " "), cell, out)
				}
			}

			for _, cell := range test.hasNot {
				if vmCmdTableHas(out, cell) {
					t.Errorf("phenix vlan %s output has cell %q:\n%s", strings.Join(test.args, " "), cell, out)
				}
			}
		})
	}

	out, err := vlanCmdRun("alias", "alpha", "NEW")
	if err != nil || out != "151\n" {
		t.Errorf("phenix vlan alias alpha NEW = %q, %v, want \"151\\n\", nil", out, err)
	}
}

// phenix vlan range lists the ranges of every experiment or of one, and sets
// a range, replacing one only with --force. The beta alias has no ID yet.
func TestVLANCmdRange(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t,
		vlanCmdExperiment(t, "alpha", map[string]int{"EXP_1": 150}, 100, 200),
		vlanCmdExperiment(t, "beta", map[string]int{"UNSET": 0}, 0, 0),
	)

	tests := []struct {
		name      string
		args      []string
		wantErr   string
		humanized string
		has       []string
		hasNot    []string
	}{
		{name: "all experiments", args: []string{"range"}, has: []string{"alpha", "100 - 200", "beta", "0 - 0"}},
		{name: "one experiment", args: []string{"range", "alpha"}, has: []string{"alpha", "100 - 200"}, hasNot: []string{"beta"}},
		{
			name:      "missing experiment",
			args:      []string{"range", "missing"},
			humanized: "Unable to display VLAN range(s) for missing experiment",
		},
		{name: "set", args: []string{"range", "beta", "10", "20"}},
		{name: "set is shown", args: []string{"range", "beta"}, has: []string{"10 - 20"}},
		{
			name:      "set again",
			args:      []string{"range", "alpha", "120", "180"},
			humanized: "Unable to set the VLAN range for the alpha experiment",
		},
		{name: "set with force", args: []string{"range", "alpha", "120", "180", "--force"}},
		{
			name:      "set a missing experiment",
			args:      []string{"range", "100%d", "10", "20"},
			humanized: "Unable to set the VLAN range for the 100%d experiment",
		},
		{
			name:    "minimum is not a number",
			args:    []string{"range", "alpha", "x", "20"},
			wantErr: "the VLAN range minimum identifier provided is not a valid integer",
		},
		{
			name:    "maximum is not a number",
			args:    []string{"range", "alpha", "10", "x"},
			wantErr: "the VLAN range maximum identifier provided is not a valid integer",
		},
		{name: "two arguments", args: []string{"range", "alpha", "10"}, wantErr: "there were an unexpected number of arguments provided"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, err := vlanCmdRun(test.args...)
			vlanCmdCheck(t, test.args, err, test.wantErr, test.humanized)

			for _, cell := range test.has {
				if !vmCmdTableHas(out, cell) {
					t.Errorf("phenix vlan %s output has no cell %q:\n%s", strings.Join(test.args, " "), cell, out)
				}
			}

			for _, cell := range test.hasNot {
				if vmCmdTableHas(out, cell) {
					t.Errorf("phenix vlan %s output has cell %q:\n%s", strings.Join(test.args, " "), cell, out)
				}
			}
		})
	}

	out, err := vlanCmdRun("range", "alpha")
	if err != nil || !vmCmdTableHas(out, "120 - 180") {
		t.Errorf("phenix vlan range alpha after --force = %q, %v, want the range 120 - 180", out, err)
	}
}

// phenix vlan alias and range with no experiment return a humanized error
// when the store cannot list the experiments.
func TestVLANCmdListStoreError(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vlanCmdUseFailingStore(t)

	for args, humanized := range map[string]string{
		"alias": "Unable to display all aliases",
		"range": "Unable to display VLAN range(s)",
	} {
		_, err := vlanCmdRun(args)
		vlanCmdCheck(t, []string{args}, err, "", humanized)
	}
}
