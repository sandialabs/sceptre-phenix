package cmd

import (
	"strings"
	"testing"

	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/vlan"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// TestVLANCmdListAndBlacklistReads checks what phenix vlan list prints when
// minimega does not answer and when nothing conflicts, and the errors of vlan
// list and vlan blacklist when they cannot read the VLANs.
func TestVLANCmdListAndBlacklistReads(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	down := func(mmtest.Command) []*minicli.Response {
		return []*minicli.Response{mmtest.Error("head", "minimega is not running")}
	}
	quiet := func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "namespace" {
			return []*minicli.Response{mmtest.Tabular("head", []string{"namespace", "vlans", "active"})}
		}

		if cmd.Base == "vlans blacklist" {
			return []*minicli.Response{mmtest.Tabular("head", []string{"vlan"})}
		}

		return nil
	}
	noBlacklist := func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "vlans blacklist" {
			return []*minicli.Response{mmtest.Error("head", "blacklist unavailable")}
		}

		return quiet(cmd)
	}

	tests := []struct {
		name         string
		args         []string
		handler      mmtest.Handler
		failingStore bool
		prefix       string   // the start of the output
		has, hasNot  []string // in the output
		humanized    string
	}{
		{
			name: "list when minimega does not answer", args: []string{"list"}, handler: down,
			prefix: "minimega did not answer (", has: []string{"the table shows the phēnix side only", "alpha", "101"},
		},
		{
			name: "list with nothing to report", args: []string{"list"}, handler: quiet,
			has: []string{"alpha", "101"}, hasNot: []string{"did not answer", "SEVERITY"},
		},
		{
			name:         "list when the store fails",
			args:         []string{"list"},
			handler:      quiet,
			failingStore: true,
			humanized:    "Unable to read the VLANs",
		},
		{
			name:      "blacklist when minimega fails",
			args:      []string{"blacklist"},
			handler:   noBlacklist,
			humanized: "Unable to read the VLAN blacklist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.failingStore {
				vlanCmdUseFailingStore(t)
			} else {
				vmCmdUseStore(t, vlanCmdExperiment(t, "alpha", map[string]int{"EXP_1": 101}, 0, 0))
			}

			vlan.Forget()
			t.Cleanup(vlan.Forget)

			original := mm.DefaultMM
			mm.DefaultMM = new(mm.Minimega)               //nolint:reassign // the real one, against the fake minimega
			t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore

			mmtest.Use(t, tt.handler)

			out, err := vlanCmdRun(tt.args...)
			if msg := expCmdCheckErr(err, "", tt.humanized); msg != "" {
				t.Fatalf("phenix vlan %s %s", strings.Join(tt.args, " "), msg)
			}

			if !strings.HasPrefix(out, tt.prefix) {
				t.Errorf("phenix vlan %s output does not start with %q:\n%s", strings.Join(tt.args, " "), tt.prefix, out)
			}

			for _, s := range tt.has {
				if !strings.Contains(out, s) {
					t.Errorf("phenix vlan %s output has no %q:\n%s", strings.Join(tt.args, " "), s, out)
				}
			}

			for _, s := range tt.hasNot {
				if strings.Contains(out, s) {
					t.Errorf("phenix vlan %s output has %q:\n%s", strings.Join(tt.args, " "), s, out)
				}
			}
		})
	}
}
