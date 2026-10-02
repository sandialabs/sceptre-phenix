package mm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/util/mm/mmtest"
)

func TestGetVMStates(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	header := []string{"name", "state"}

	for name, tc := range map[string]struct {
		answer  []*minicli.Response // one per host in the mesh
		want    map[string]string
		wantErr string // substring of the error, "" for none
	}{
		"reads every host's VMs from one command": {
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"a", "RUNNING"}),
				mmtest.Tabular("compute1", header, []string{"b", "PAUSED"}, []string{"c", "BUILDING"}),
			},
			want: map[string]string{"a": "RUNNING", "b": "PAUSED", "c": "BUILDING"},
		},
		"fails when a host cannot answer": {
			answer: []*minicli.Response{
				mmtest.Tabular("head", header, []string{"a", "RUNNING"}),
				{Host: "compute1", Error: "host unreachable"},
			},
			wantErr: "host unreachable",
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == vmInfoSummaryCmd {
					return tc.answer
				}

				return nil
			})

			states, err := Minimega{}.GetVMStates(NS("exp"))

			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("GetVMStates: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("GetVMStates = %v, %v; want an error containing %q", states, err, tc.wantErr)
			}

			if !reflect.DeepEqual(states, tc.want) {
				t.Fatalf("GetVMStates = %v, want %v", states, tc.want)
			}

			if cmds := received(); len(cmds) != 1 || cmds[0].Namespace != "exp" {
				t.Fatalf("sent %+v, want one command in namespace exp", cmds)
			}
		})
	}
}
