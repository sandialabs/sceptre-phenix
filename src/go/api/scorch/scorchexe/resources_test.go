package scorchexe

import (
	"errors"
	"testing"

	"phenix/api/scorch/scorchmd"
)

func TestResourceClaimsRemainExclusiveAtBreakpoints(t *testing.T) {
	status := &scorchmd.ScorchStatus{
		Executions: map[string]*scorchmd.Execution{
			"0": {ID: "setup", Run: 0, State: scorchmd.StateWaiting, Resources: []string{"tcpdump"}},
		},
	}
	if err := checkResources([]string{"tcpdump"}, status); !errors.Is(err, ErrResourceConflict) {
		t.Fatal("same-type destructive cleanup allowed", err)
	}
	if err := checkResources([]string{"*"}, status); !errors.Is(err, ErrResourceConflict) {
		t.Fatal("namespace reset allowed", err)
	}
	if err := checkResources(nil, status); err != nil {
		t.Fatal("independent work rejected", err)
	}
	status.Executions["0"].State = scorchmd.StateInterrupted
	if err := checkResources([]string{"tcpdump"}, status); !errors.Is(err, ErrResourceConflict) {
		t.Fatal("interrupted owner released resources", err)
	}
	status.Executions["0"].State = scorchmd.StateSucceeded
	if err := checkResources([]string{"tcpdump"}, status); err != nil {
		t.Fatal("completed owner retained claims", err)
	}
}
