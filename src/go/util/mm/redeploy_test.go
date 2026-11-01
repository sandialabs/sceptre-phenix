package mm

import (
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"

	"phenix/util/mm/mmtest"
)

func TestRedeployVMKeepsItsType(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		vmType, want string
	}{
		"container": {vmType: "container", want: "vm launch container a"},
		"kvm":       {vmType: "kvm", want: "vm launch kvm a"},
		"unknown":   {vmType: "", want: "vm launch kvm a"},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == vmInfoCmd {
					return []*minicli.Response{mmtest.Tabular("head", []string{"name", "type", "disks"}, []string{"a", tc.vmType, ""})}
				}

				return nil
			})

			if err := (Minimega{}).RedeployVM(NS("exp"), VMName("a")); err != nil {
				t.Fatalf("RedeployVM(a) error = %v", err)
			}

			var launches []string

			for _, base := range mmtest.Bases(received()) {
				if strings.HasPrefix(base, "vm launch ") {
					launches = append(launches, base)
				}
			}

			if diff := cmp.Diff([]string{tc.want}, launches); diff != "" {
				t.Errorf("RedeployVM(a) launches mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
