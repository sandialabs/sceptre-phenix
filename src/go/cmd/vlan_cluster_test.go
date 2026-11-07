package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vlan"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// vlanCmdUseMinimega points minimega at a fake with the unowned namespace
// old-demo on 400, the running namespace of gamma on 101, and a blacklist of
// 999, and returns the commands it receives. removable says whether it can
// remove a blacklisted ID.
func vlanCmdUseMinimega(t *testing.T, removable bool) func() []mmtest.Command {
	t.Helper()

	vlan.Forget()
	t.Cleanup(vlan.Forget)

	original := mm.DefaultMM
	mm.DefaultMM = new(mm.Minimega) //nolint:reassign // the real one, against the fake minimega

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

	help := "Usage:\n\tclear vlans [prefix]\n"
	if removable {
		help += "\tclear vlans blacklist <vlan>\n"
	}

	return mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "namespace":
			return []*minicli.Response{mmtest.Tabular("head", []string{"namespace", "vlans", "active"},
				[]string{"old-demo", "", "false"}, []string{"gamma", "", "false"})}
		case cmd.Base == "vlans" && cmd.Namespace == "old-demo":
			return []*minicli.Response{mmtest.Tabular("head", []string{"alias", "vlan"}, []string{"P", "400"})}
		case cmd.Base == "vlans" && cmd.Namespace == "gamma":
			return []*minicli.Response{mmtest.Tabular("head", []string{"alias", "vlan"}, []string{"EXP_1", "101"})}
		case cmd.Base == "vlans blacklist":
			return []*minicli.Response{mmtest.Tabular("head", []string{"vlan"}, []string{"999"})}
		case cmd.Base == "help clear vlans":
			return []*minicli.Response{mmtest.Text("head", help)}
		}

		return nil
	})
}

// changes lists the commands that change minimega's VLANs.
func vlanCmdChanges(cmds []mmtest.Command) []string {
	var out []string

	for _, c := range cmds {
		if strings.HasPrefix(c.Base, "clear vlans") || strings.HasPrefix(c.Base, "vlans blacklist ") {
			out = append(out, strings.TrimSpace(c.Namespace+" "+c.Base))
		}
	}

	return out
}

// phenix vlan list, blacklist, release and pin-live show and change the
// cluster's VLANs, and refuse what the API refuses.
func TestVLANCmdCluster(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	running := vlanCmdExperiment(t, "gamma", map[string]int{"EXP_1": 0}, 0, 0)
	running.Status = map[string]any{"startTime": "2024-01-01T00:00:00Z"}

	tests := []struct {
		name      string
		removable bool
		args      []string
		wantErr   error
		has       []string
		hasNot    []string
		changes   []string
	}{
		{
			name: "list", args: []string{"list"},
			has: []string{"alpha", "beta", "old-demo", "999", "duplicate-pin", "Also used by beta (EXP_2) and gamma (EXP_1)."},
		},
		{name: "list one experiment", args: []string{"list", "--experiment", "beta"}, has: []string{"beta"}, hasNot: []string{"old-demo"}},
		{name: "list issues", args: []string{"list", "--issues"}, has: []string{"old-demo"}, hasNot: []string{"999"}},
		{name: "blacklist", args: []string{"blacklist"}, has: []string{"999"}},
		{name: "blacklist add", args: []string{"blacklist", "add", "300"}, changes: []string{"vlans blacklist 300"}},
		{name: "blacklist add of an allocated ID", args: []string{"blacklist", "add", "400"}, wantErr: vlan.ErrInUse},
		{name: "blacklist add of a bad ID", args: []string{"blacklist", "add", "x"}, wantErr: errBadVLANID},
		{
			name: "blacklist remove", removable: true, args: []string{"blacklist", "remove", "999"},
			changes: []string{"clear vlans blacklist 999"},
		},
		{name: "blacklist remove without support", args: []string{"blacklist", "remove", "999"}, wantErr: vlan.ErrUnsupported},
		{name: "release", args: []string{"release", "old-demo"}, changes: []string{"old-demo clear vlans"}},
		{name: "release of an experiment's namespace", args: []string{"release", "alpha"}, wantErr: vlan.ErrInUse},
		{name: "pin-live", args: []string{"pin-live", "gamma"}, has: []string{"gamma", "EXP_1", "101"}},
		{name: "pin-live of a stopped experiment", args: []string{"pin-live", "alpha"}, wantErr: vlan.ErrNotRunning},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vmCmdUseStore(t,
				vlanCmdExperiment(t, "alpha", map[string]int{"EXP_1": 101}, 0, 0),
				vlanCmdExperiment(t, "beta", map[string]int{"EXP_2": 101}, 0, 0),
				running,
			)
			received := vlanCmdUseMinimega(t, test.removable)

			out, err := vlanCmdRun(test.args...)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("phenix vlan %s error = %v, want %v", strings.Join(test.args, " "), err, test.wantErr)
			}

			for _, cell := range test.has {
				if !strings.Contains(out, cell) {
					t.Errorf("phenix vlan %s output has no %q:\n%s", strings.Join(test.args, " "), cell, out)
				}
			}

			for _, cell := range test.hasNot {
				if strings.Contains(out, cell) {
					t.Errorf("phenix vlan %s output has %q:\n%s", strings.Join(test.args, " "), cell, out)
				}
			}

			if got := vlanCmdChanges(received()); strings.Join(got, "|") != strings.Join(test.changes, "|") {
				t.Errorf("phenix vlan %s sent %q, want %q", strings.Join(test.args, " "), got, test.changes)
			}
		})
	}
}
