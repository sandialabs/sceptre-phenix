package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/vlan"
	"phenix/store"
)

// vlanOutputConfigs are the experiments of the VLAN output tests: alpha with
// the pin EXP_1=101, the alias EXP_3 with no pin and the range 100-200, beta
// with the pin EXP_2=101, and the running gamma.
func vlanOutputConfigs(t *testing.T) []store.Config {
	t.Helper()

	running := vlanCmdExperiment(t, "gamma", map[string]int{"EXP_1": 0}, 0, 0)
	running.Status = map[string]any{"startTime": "2024-01-01T00:00:00Z"}

	return []store.Config{
		vlanCmdExperiment(t, "alpha", map[string]int{"EXP_1": 101, "EXP_3": 0}, 100, 200),
		vlanCmdExperiment(t, "beta", map[string]int{"EXP_2": 101}, 0, 0),
		running,
	}
}

// vlanOutputRun runs phenix vlan, with free and check, with args.
func vlanOutputRun(args ...string) (string, error) {
	vlanCmd := newVlanCmd()
	vlanCmd.AddCommand(newVlanAliasCmd(), newVlanRangeCmd(), newVlanListCmd(), newVlanBlacklistCmd(),
		newVlanReleaseCmd(), newVlanFreeCmd(), newVlanCheckCmd())

	out, err := outputRun(vlanCmd, append([]string{"vlan"}, args...)...)

	return out, err
}

// vlanOutputRow is the part of a row of GET /vlans that the tests check.
type vlanOutputRow struct {
	ID         int    `json:"id"`
	Alias      string `json:"alias"`
	Experiment string `json:"experiment"`
	Namespace  string `json:"namespace"`
}

// vlanOutputReport is the part of the body of GET /vlans that the tests check.
type vlanOutputReport struct {
	VLANs     []vlanOutputRow `json:"vlans"`
	Blacklist []int           `json:"blacklist"`
}

// The VLAN view commands print their tables as before, and the bodies of the
// API with -o json and -o yaml.
func TestVLANCmdOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vlanOutputConfigs(t)...)
	vlanCmdUseMinimega(t, true)

	runner := func(args ...string) func(...string) (string, error) {
		return func(more ...string) (string, error) {
			return vlanOutputRun(append(append([]string{}, args...), more...)...)
		}
	}

	report := vlanOutputReport{
		VLANs: []vlanOutputRow{
			{ID: 101, Alias: "EXP_1", Experiment: "alpha"}, {ID: 101, Alias: "EXP_2", Experiment: "beta"},
			{ID: 101, Alias: "EXP_1", Experiment: "gamma", Namespace: "gamma"}, {ID: 400, Alias: "P", Namespace: "old-demo"},
			{ID: 999},
		},
		Blacklist: []int{999},
	}

	if table := checkOutputFormats(t, "phenix vlan list", runner("list"), report); !strings.Contains(table, "old-demo") {
		t.Errorf("phenix vlan list = %q, want the namespace old-demo", table)
	}

	checkOutputFormats(t, "phenix vlan alias", runner("alias"), []vlanAliasRow{
		{Experiment: "alpha", Alias: "EXP_1", ID: 101}, {Experiment: "alpha", Alias: "EXP_3", ID: 0},
		{Experiment: "beta", Alias: "EXP_2", ID: 101},
	})
	checkOutputFormats(t, "phenix vlan alias beta", runner("alias", "beta"), []vlanAliasRow{{Experiment: "beta", Alias: "EXP_2", ID: 101}})

	lookup := checkOutputFormats(t, "phenix vlan alias alpha EXP_1", runner("alias", "alpha", "EXP_1"),
		vlanAliasRow{Experiment: "alpha", Alias: "EXP_1", ID: 101})
	if lookup != "101\n" {
		t.Errorf("phenix vlan alias alpha EXP_1 = %q, want %q", lookup, "101\n")
	}

	checkOutputFormats(t, "phenix vlan range alpha", runner("range", "alpha"), []vlanRangeRow{{Experiment: "alpha", Min: 100, Max: 200}})

	blacklist := checkOutputFormats(t, "phenix vlan blacklist", runner("blacklist"), vlanBlacklistBody{VLANs: []int{999}, Removable: true})
	if blacklist != "999\n" {
		t.Errorf("phenix vlan blacklist = %q, want %q", blacklist, "999\n")
	}

	checkOutputFormats(t, "phenix vlan free --min 100", runner("free", "--min", "100"), vlanBlock{Start: 100, End: 100})

	clean := checkOutputFormats(t, "phenix vlan check beta --alias EXP_2=102", runner("check", "beta", "--alias", "EXP_2=102"),
		vlanIssues{Issues: []vlan.Issue{}})
	if want := "The VLAN pins and range of beta have no issues.\n"; clean != want {
		t.Errorf("phenix vlan check beta --alias EXP_2=102 = %q, want %q", clean, want)
	}

	out, err := vlanOutputRun("check", "beta", "-o", "json")
	if err != nil || !strings.Contains(out, `"kind": "duplicate-pin"`) {
		t.Errorf("phenix vlan check beta -o json = %q, %v, want the duplicate pin issue", out, err)
	}
}

// phenix vlan list -o csv prints the rows as GET /vlans?format=csv does.
func TestVLANCmdListCSV(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	vmCmdUseStore(t, vlanOutputConfigs(t)...)
	vlanCmdUseMinimega(t, true)

	const header = "id,alias,experiment,namespace,running,states,phenix,minimega,vms,hosts,issues\n"

	for _, tt := range []struct {
		args []string
		want string
	}{
		{
			args: []string{"list", "-o", "csv"},
			want: header +
				"101,EXP_1,alpha,,false,pinned,101,,2,,duplicate-pin\n" +
				"101,EXP_2,beta,,false,pinned,101,,1,,duplicate-pin\n" +
				"101,EXP_1,gamma,gamma,true,allocated,,101,0,,duplicate-pin\n" +
				"400,P,,old-demo,false,allocated,,400,0,,unowned\n" +
				"999,,,,false,blacklisted,,999,0,,\n" +
				",EXP_3,alpha,,false,,,,0,,\n",
		},
		{
			args: []string{"list", "--experiment", "alpha", "-o", "csv"},
			want: header + "101,EXP_1,alpha,,false,pinned,101,,2,,duplicate-pin\n,EXP_3,alpha,,false,,,,0,,\n",
		},
		{args: []string{"list", "--state", "blacklisted", "-o", "csv"}, want: header + "999,,,,false,blacklisted,,999,0,,\n"},
	} {
		out, err := vlanOutputRun(tt.args...)
		if err != nil {
			t.Fatalf("phenix vlan %q error = %v", tt.args, err)
		}

		if diff := cmp.Diff(tt.want, out); diff != "" {
			t.Errorf("phenix vlan %q mismatch (-want +got):\n%s", tt.args, diff)
		}
	}

	if _, err := vlanOutputRun("alias", "-o", "csv"); err == nil || !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("phenix vlan alias -o csv error = %v, want an unknown output format", err)
	}
}

// The VLAN changes with --dry-run check what the change checks, print what
// they would change, and change nothing. -o and --dry-run go only where they
// apply.
func TestVLANCmdDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	tests := []struct {
		name      string
		args      []string
		removable bool
		want      string
		wantErr   error
	}{
		{
			name: "unpin", args: []string{"alias", "alpha", "EXP_1", "--unpin"},
			want: "would unpin VLAN alias EXP_1 of experiment alpha\n",
		},
		{name: "unpin in a running experiment", args: []string{"alias", "gamma", "EXP_1", "--unpin"}, wantErr: vlan.ErrRunning},
		{name: "unpin of a missing alias", args: []string{"alias", "alpha", "EXP_9", "--unpin"}, wantErr: vlan.ErrAliasNotFound},
		{name: "clear", args: []string{"range", "alpha", "--clear"}, want: "would clear the VLAN range of experiment alpha\n"},
		{name: "clear of a missing experiment", args: []string{"range", "nope", "--clear"}, wantErr: vlan.ErrNotFound},
		{name: "release", args: []string{"release", "old-demo"}, want: "would release the VLAN IDs of namespace old-demo\n"},
		{name: "release of an experiment's namespace", args: []string{"release", "alpha"}, wantErr: vlan.ErrInUse},
		{name: "release of a missing namespace", args: []string{"release", "none"}, wantErr: vlan.ErrNotFound},
		{
			name: "blacklist remove", args: []string{"blacklist", "remove", "999"}, removable: true,
			want: "would remove VLAN 999 from the minimega blacklist\n",
		},
		{name: "blacklist remove without support", args: []string{"blacklist", "remove", "999"}, wantErr: vlan.ErrUnsupported},
		{
			name:      "blacklist remove of an ID not on it",
			args:      []string{"blacklist", "remove", "300"},
			removable: true,
			wantErr:   vlan.ErrNotFound,
		},
		{name: "a view", args: []string{"alias", "alpha"}, wantErr: errVLANViewOnly},
		{name: "a pin", args: []string{"alias", "alpha", "EXP_3", "102"}, wantErr: errVLANViewOnly},
		{name: "a range", args: []string{"range", "alpha", "100", "300"}, wantErr: errVLANViewOnly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmCmdUseStore(t, vlanOutputConfigs(t)...)
			received := vlanCmdUseMinimega(t, tt.removable)

			args := append(append([]string{}, tt.args...), "--dry-run")

			out, err := vlanOutputRun(args...)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("phenix vlan %q error = %v, want %v", args, err, tt.wantErr)
			}

			if out != tt.want {
				t.Errorf("phenix vlan %q = %q, want %q", args, out, tt.want)
			}

			if changes := vlanCmdChanges(received()); len(changes) != 0 {
				t.Errorf("phenix vlan %q sent %q, want no change", args, changes)
			}

			if aliases, err := vlanOutputRun("alias", "alpha"); err != nil || !strings.Contains(aliases, "101") {
				t.Errorf("after phenix vlan %q, phenix vlan alias alpha = %q, %v, want the pin 101", args, aliases, err)
			}

			if ranges, err := vlanOutputRun("range", "alpha"); err != nil || !strings.Contains(ranges, "100 - 200") {
				t.Errorf("after phenix vlan %q, phenix vlan range alpha = %q, %v, want 100 - 200", args, ranges, err)
			}
		})
	}

	vmCmdUseStore(t, vlanOutputConfigs(t)...)
	vlanCmdUseMinimega(t, false)

	for _, args := range [][]string{{"alias", "alpha", "EXP_3", "102", "-o", "json"}, {"range", "alpha", "--clear", "-o", "yaml"}} {
		if _, err := vlanOutputRun(args...); !errors.Is(err, errVLANViewOnly) {
			t.Errorf("phenix vlan %q error = %v, want %v", args, err, errVLANViewOnly)
		}
	}
}
