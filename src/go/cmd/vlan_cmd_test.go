package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/spf13/cobra"

	"phenix/api/vlan"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
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
	vlanCmd.AddCommand(newVlanAliasCmd(), newVlanRangeCmd(), newVlanListCmd(), newVlanBlacklistCmd(),
		newVlanReleaseCmd(), newVlanPinLiveCmd())
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

// vlanCmdCase is one phenix vlan command line and what it must give: an
// error that wantErr is (or a humanized error that starts with humanized), the
// error text parts errHas, the output cells has and not hasNot, and then the
// output of the command line after. With conflict set, the error must be a
// *vlan.ConflictError.
type vlanCmdCase struct {
	name      string
	args      []string
	conflict  bool
	wantErr   error
	humanized string
	errHas    []string
	has       []string
	hasNot    []string
	after     []string
	afterHas  []string
}

// vlanCmdRunCase runs tc against a new store of configs and the fake minimega
// of vlanCmdUseMinimega.
func vlanCmdRunCase(t *testing.T, tc vlanCmdCase, configs ...store.Config) {
	t.Helper()

	vmCmdUseStore(t, configs...)
	vlanCmdUseMinimega(t, false)

	out, err := vlanCmdRun(tc.args...)

	args := strings.Join(tc.args, " ")

	var conflict *vlan.ConflictError

	switch {
	case tc.humanized != "":
		vlanCmdCheck(t, tc.args, err, "", tc.humanized)
	case tc.conflict:
		if !errors.As(err, &conflict) {
			t.Errorf("phenix vlan %s error = %v, want a VLAN conflict", args, err)
		}
	case !errors.Is(err, tc.wantErr):
		t.Errorf("phenix vlan %s error = %v, want %v", args, err, tc.wantErr)
	}

	for _, part := range tc.errHas {
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Errorf("phenix vlan %s error = %v, want it to contain %q", args, err, part)
		}
	}

	if err != nil && strings.Contains(err.Error(), "%!") {
		t.Errorf("phenix vlan %s error = %q, want no formatting verb errors", args, err)
	}

	for _, cell := range tc.has {
		if !vmCmdTableHas(out, cell) {
			t.Errorf("phenix vlan %s output has no cell %q:\n%s", args, cell, out)
		}
	}

	for _, cell := range tc.hasNot {
		if vmCmdTableHas(out, cell) {
			t.Errorf("phenix vlan %s output has cell %q:\n%s", args, cell, out)
		}
	}

	if tc.after == nil {
		return
	}

	out, err = vlanCmdRun(tc.after...)
	if err != nil {
		t.Fatalf("phenix vlan %s after %s error = %v", strings.Join(tc.after, " "), args, err)
	}

	for _, part := range tc.afterHas {
		if !strings.Contains(out, part) {
			t.Errorf("phenix vlan %s after %s = %q, want it to contain %q", strings.Join(tc.after, " "), args, out, part)
		}
	}
}

// phenix vlan alias lists the aliases of every experiment or of one, pins an
// alias with the conflict checks of the API, and unpins one. The fake
// minimega gives the namespace old-demo the ID 400 and blacklists 999.
func TestVLANCmdAlias(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	running := vlanCmdExperiment(t, "delta", map[string]int{"EXP_9": 0}, 0, 0)
	running.Status = map[string]any{"startTime": "2024-01-01T00:00:00Z"}

	configs := []store.Config{
		vlanCmdExperiment(t, "alpha", map[string]int{"EXP_1": 101, "NEW": 0}, 0, 0),
		vlanCmdExperiment(t, "beta", map[string]int{"EXP_2": 202}, 0, 0),
		vlanCmdExperiment(t, "ranged", map[string]int{"R": 0}, 100, 200),
		running,
	}

	tests := []vlanCmdCase{
		{name: "all experiments", args: []string{"alias"}, has: []string{"alpha", "beta", "EXP_1", "101", "EXP_2", "202"}},
		{name: "one experiment", args: []string{"alias", "alpha"}, has: []string{"alpha", "EXP_1", "101"}, hasNot: []string{"beta"}},
		{name: "missing experiment", args: []string{"alias", "missing"}, humanized: "Unable to display aliases for the missing experiment"},
		{name: "pin", args: []string{"alias", "alpha", "NEW", "150"}, after: []string{"alias", "alpha", "NEW"}, afterHas: []string{"150"}},
		{
			name:     "pin a pinned alias again",
			args:     []string{"alias", "alpha", "EXP_1", "151"},
			after:    []string{"alias", "alpha", "EXP_1"},
			afterHas: []string{"151"},
		},
		{
			name: "pin an ID that another experiment pins", args: []string{"alias", "alpha", "NEW", "202"},
			conflict: true,
			errHas: []string{
				"VLAN conflict in experiment alpha", "VLAN ID 202 of alias NEW: Also pinned by beta (EXP_2).", "--force",
			},
		},
		{
			name: "pin an ID that minimega blacklists", args: []string{"alias", "alpha", "NEW", "999"},
			conflict: true, errHas: []string{"minimega blacklists 999."},
		},
		{
			name: "pin an ID that a namespace uses", args: []string{"alias", "alpha", "NEW", "400"},
			conflict: true, errHas: []string{"the namespace old-demo"},
		},
		{
			name: "pin with force", args: []string{"alias", "alpha", "NEW", "202", "--force"},
			after: []string{"alias", "alpha", "NEW"}, afterHas: []string{"202"},
		},
		{name: "pin a missing alias", args: []string{"alias", "alpha", "OTHER", "150"}, wantErr: vlan.ErrAliasNotFound},
		{name: "pin outside the range", args: []string{"alias", "ranged", "R", "300"}, wantErr: vlan.ErrInvalid},
		{name: "pin in a running experiment", args: []string{"alias", "delta", "EXP_9", "150"}, wantErr: vlan.ErrRunning},
		{name: "pin in a missing experiment", args: []string{"alias", "100%d", "NEW", "151"}, wantErr: vlan.ErrNotFound},
		{
			name:     "unpin",
			args:     []string{"alias", "alpha", "EXP_1", "--unpin"},
			has:      nil,
			after:    []string{"alias", "alpha"},
			afterHas: []string{"EXP_1"},
		},
		{name: "unpin a missing alias", args: []string{"alias", "alpha", "OTHER", "--unpin"}, wantErr: vlan.ErrAliasNotFound},
		{name: "unpin in a running experiment", args: []string{"alias", "delta", "EXP_9", "--unpin"}, wantErr: vlan.ErrRunning},
		{name: "unpin with an ID", args: []string{"alias", "alpha", "EXP_1", "150", "--unpin"}, wantErr: errVLANArgs},
		{name: "unpin with force", args: []string{"alias", "alpha", "EXP_1", "--unpin", "--force"}, wantErr: errVLANArgs},
		{name: "force without an ID", args: []string{"alias", "alpha", "--force"}, wantErr: errVLANArgs},
		{name: "ID is not a number", args: []string{"alias", "alpha", "NEW", "x"}, wantErr: errBadVLANID},
		{name: "ID 0", args: []string{"alias", "alpha", "NEW", "0"}, wantErr: errBadVLANID},
		{name: "ID above 4094", args: []string{"alias", "alpha", "NEW", "4095"}, wantErr: errBadVLANID},
		{name: "four arguments", args: []string{"alias", "alpha", "NEW", "150", "x"}, wantErr: errVLANArgs},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { vlanCmdRunCase(t, tc, configs...) })
	}

	t.Run("unpinned alias has no ID", func(t *testing.T) {
		vmCmdUseStore(t, configs...)
		vlanCmdUseMinimega(t, false)

		if _, err := vlanCmdRun("alias", "alpha", "EXP_1", "--unpin"); err != nil {
			t.Fatalf("phenix vlan alias alpha EXP_1 --unpin error = %v", err)
		}

		if _, err := vlanCmdRun("alias", "alpha", "EXP_1"); !errors.Is(err, vlan.ErrAliasUnassigned) {
			t.Errorf("phenix vlan alias alpha EXP_1 after --unpin error = %v, want %v", err, vlan.ErrAliasUnassigned)
		}
	})
}

// phenix vlan alias pins nothing when minimega does not answer, unless forced.
func TestVLANCmdAliasMinimegaDown(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vlanCmdExperiment(t, "alpha", map[string]int{"NEW": 0}, 0, 0))

	vlan.Forget()
	t.Cleanup(vlan.Forget)

	original := mm.DefaultMM
	mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

	mmtest.Use(t, func(mmtest.Command) []*minicli.Response {
		return []*minicli.Response{mmtest.Error("head", "minimega is down")}
	})

	if _, err := vlanCmdRun("alias", "alpha", "NEW", "150"); !errors.Is(err, vlan.ErrMinimegaDown) {
		t.Errorf("phenix vlan alias alpha NEW 150 error = %v, want %v", err, vlan.ErrMinimegaDown)
	}

	if _, err := vlanCmdRun("alias", "alpha", "NEW", "150", "--force"); err != nil {
		t.Errorf("phenix vlan alias alpha NEW 150 --force error = %v, want nil", err)
	}
}

// phenix vlan range lists the ranges of every experiment or of one, sets a
// range with the overlap check of the API, and clears one. The beta alias
// has no ID yet.
func TestVLANCmdRange(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	running := vlanCmdExperiment(t, "delta", map[string]int{}, 0, 0)
	running.Status = map[string]any{"startTime": "2024-01-01T00:00:00Z"}

	configs := []store.Config{
		vlanCmdExperiment(t, "alpha", map[string]int{"EXP_1": 150}, 100, 200),
		vlanCmdExperiment(t, "beta", map[string]int{"UNSET": 0}, 0, 0),
		running,
	}

	tests := []vlanCmdCase{
		{name: "all experiments", args: []string{"range"}, has: []string{"alpha", "100 - 200", "beta", "0 - 0"}},
		{name: "one experiment", args: []string{"range", "alpha"}, has: []string{"alpha", "100 - 200"}, hasNot: []string{"beta"}},
		{
			name:      "missing experiment",
			args:      []string{"range", "missing"},
			humanized: "Unable to display VLAN range(s) for missing experiment",
		},
		{name: "set", args: []string{"range", "beta", "10", "20"}, after: []string{"range", "beta"}, afterHas: []string{"10 - 20"}},
		{
			name: "replace a range", args: []string{"range", "alpha", "120", "180"},
			after: []string{"range", "alpha"}, afterHas: []string{"120 - 180"},
		},
		{
			name: "set a range that overlaps another experiment", args: []string{"range", "beta", "150", "250"},
			conflict: true,
			errHas:   []string{"VLAN conflict in experiment beta", "VLAN range 150–200 overlaps alpha.", "--force"},
		},
		{
			name: "set an overlapping range with force", args: []string{"range", "beta", "150", "250", "--force"},
			after: []string{"range", "beta"}, afterHas: []string{"150 - 250"},
		},
		{name: "set a range that leaves out a pin", args: []string{"range", "alpha", "160", "180"}, wantErr: vlan.ErrInvalid},
		{name: "set in a running experiment", args: []string{"range", "delta", "10", "20"}, wantErr: vlan.ErrRunning},
		{name: "set in a missing experiment", args: []string{"range", "100%d", "10", "20"}, wantErr: vlan.ErrNotFound},
		{name: "clear", args: []string{"range", "alpha", "--clear"}, after: []string{"range", "alpha"}, afterHas: []string{"0 - 0"}},
		{name: "clear in a running experiment", args: []string{"range", "delta", "--clear"}, wantErr: vlan.ErrRunning},
		{name: "clear with a range", args: []string{"range", "alpha", "1", "2", "--clear"}, wantErr: errVLANArgs},
		{name: "clear with force", args: []string{"range", "alpha", "--clear", "--force"}, wantErr: errVLANArgs},
		{name: "force without a range", args: []string{"range", "alpha", "--force"}, wantErr: errVLANArgs},
		{
			name:    "minimum is not a number",
			args:    []string{"range", "alpha", "x", "20"},
			wantErr: errBadVLANID,
			errHas:  []string{"the VLAN range minimum"},
		},
		{name: "minimum 0", args: []string{"range", "alpha", "0", "20"}, wantErr: errBadVLANID, errHas: []string{"the VLAN range minimum"}},
		{
			name:    "maximum is not a number",
			args:    []string{"range", "alpha", "10", "x"},
			wantErr: errBadVLANID,
			errHas:  []string{"the VLAN range maximum"},
		},
		{
			name:    "maximum above 4094",
			args:    []string{"range", "alpha", "10", "5000"},
			wantErr: errBadVLANID,
			errHas:  []string{"the VLAN range maximum"},
		},
		{name: "minimum above maximum", args: []string{"range", "alpha", "30", "20"}, wantErr: errBadVLANID},
		{name: "two arguments", args: []string{"range", "alpha", "10"}, wantErr: errVLANArgs},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { vlanCmdRunCase(t, tc, configs...) })
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
