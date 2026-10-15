package broker

import (
	"encoding/json"
	"errors"
	"testing"

	"phenix/app"
	v1 "phenix/types/version/v1"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/rbac"
)

const (
	expA = "exp-a"
	expB = "exp-b"
)

// clientWith is a client whose role has a policy for each resource, verb and
// experiment triple.
func clientWith(triples ...[3]string) *Client {
	role := rbac.Role{Spec: &v1.RoleSpec{}}

	for _, p := range triples {
		role.Spec.Policies = append(role.Spec.Policies, &v1.PolicySpec{
			Resources: []string{p[0]}, ResourceNames: []string{p[2]}, Verbs: []string{p[1]},
		})
	}

	return &Client{role: role, publish: make(chan any, 4)}
}

// checkReceived fails the test unless each client got exactly the messages
// want says it should.
func checkReceived(t *testing.T, what string, want map[string]bool, cs map[string]*Client) {
	t.Helper()

	for name, c := range cs {
		if got := len(received(c)) == 1; got != want[name] {
			t.Errorf("%s, %s: received %t, want %t", what, name, got, want[name])
		}
	}
}

// A message broadcast with several policies reaches only the clients whose
// role passes every one of them, as SCORCH updates need scorch get and
// experiments get on their experiment.
//
//nolint:paralleltest // clients is shared package state
func TestBroadcastWithPoliciesNeedsEveryPolicy(t *testing.T) {
	cs := map[string]*Client{
		"SCORCH viewer of exp-a":     clientWith([3]string{scorchApp, "get", expA}, [3]string{"experiments", "get", expA}),
		"SCORCH viewer of exp-b":     clientWith([3]string{scorchApp, "get", expB}, [3]string{"experiments", "get", expB}),
		"experiment viewer of exp-a": clientWith([3]string{"experiments", "get", expA}),
		"scorch get without exp-a":   clientWith([3]string{scorchApp, "get", expA}, [3]string{"experiments", "get", expB}),
	}

	all := make([]*Client, 0, len(cs))
	for _, c := range cs {
		all = append(all, c)
	}

	withClients(t, all...)

	BroadcastWithPolicies(
		[]*bt.RequestPolicy{bt.NewRequestPolicy(scorchApp, "get", expA), bt.NewRequestPolicy("experiments", "get", expA)},
		bt.NewResource("apps/scorch", expA+"/0/0", "pipeline-update"),
		json.RawMessage(`{}`),
	)
	deliver(<-broadcast)

	checkReceived(t, "pipeline update", map[string]bool{"SCORCH viewer of exp-a": true}, cs)

	// no policies is a broadcast to every client
	BroadcastWithPolicies(nil, bt.NewResource("experiment", expA, "update"), nil)
	deliver(<-broadcast)

	checkReceived(t, "unrestricted", map[string]bool{
		"SCORCH viewer of exp-a": true, "SCORCH viewer of exp-b": true,
		"experiment viewer of exp-a": true, "scorch get without exp-a": true,
	}, cs)
}

// The progress of a SCORCH trigger goes to the clients that may view the
// experiment's SCORCH, as SCORCH runs are started with scorch permissions,
// while another app's goes to those that may trigger it, by the trigger's verb.
//
//nolint:paralleltest // clients is shared package state
func TestTriggerProgressOfScorchGoesToItsViewers(t *testing.T) {
	cs := map[string]*Client{
		"SCORCH viewer": clientWith([3]string{scorchApp, "get", expA}, [3]string{"experiments", "get", expA}),
		"trigger role": clientWith(
			[3]string{"experiments", "get", expA}, [3]string{"experiments/trigger", "create", expA},
			[3]string{"experiments/trigger", "delete", expA},
		),
		"SCORCH viewer of exp-b": clientWith([3]string{scorchApp, "get", expB}, [3]string{"experiments", "get", expB}),
	}

	all := make([]*Client, 0, len(cs))
	for _, c := range cs {
		all = append(all, c)
	}

	withClients(t, all...)

	for _, tc := range []struct {
		trigger app.TriggerPublication
		want    string
	}{
		{app.TriggerPublication{Experiment: expA, App: scorchApp, Resource: expA + "/0", State: "start"}, "SCORCH viewer"},
		{
			app.TriggerPublication{
				Experiment: expA, App: scorchApp, Verb: "delete", State: triggerStateError, Error: errors.New("run failed"),
			},
			"SCORCH viewer",
		},
		{app.TriggerPublication{Experiment: expA, App: "ntp", State: "start"}, "trigger role"},
		{app.TriggerPublication{Experiment: expA, App: "ntp", Verb: "delete", State: "success"}, "trigger role"},
	} {
		out, ok := encodeTrigger(tc.trigger)
		if !ok {
			t.Fatalf("encoding %+v", tc.trigger)
		}

		deliver(out)

		checkReceived(t, tc.trigger.App+" "+tc.trigger.State, map[string]bool{tc.want: true}, cs)
	}
}
