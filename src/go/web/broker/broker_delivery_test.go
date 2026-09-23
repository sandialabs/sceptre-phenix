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

// newTestClients returns a client set with one client per role. The clients
// have no websocket, so tests must not fill their publish buffers.
func newTestClients(roles ...[]*v1.PolicySpec) (map[*Client]bool, []*Client) {
	var (
		set     = make(map[*Client]bool)
		clients = make([]*Client, 0, len(roles))
	)

	for _, policies := range roles {
		role := rbac.Role{Spec: &v1.RoleSpec{Policies: policies}}
		cli := NewClient(role, nil)

		set[cli] = true
		clients = append(clients, cli)
	}

	return set, clients
}

// received drains each client and reports which clients got a message.
func received(clients []*Client) []bool {
	got := make([]bool, len(clients))

	for i, cli := range clients {
		for len(cli.publish) > 0 {
			<-cli.publish

			got[i] = true
		}
	}

	return got
}

// nextBroadcast returns the message queued by a Broadcast call. The broker is
// not running in tests, so nothing else reads the queue.
func nextBroadcast(t *testing.T) bt.Publish {
	t.Helper()

	select {
	case pub := <-broadcast:
		return pub
	default:
		t.Fatal("no message was queued for broadcast")
	}

	return bt.Publish{}
}

func scorchGet() *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{"scorch"}, ResourceNames: nil, Verbs: []string{"get"}}
}

func policy(resource, verb string, names ...string) *v1.PolicySpec {
	return &v1.PolicySpec{Resources: []string{resource}, ResourceNames: names, Verbs: []string{verb}}
}

// TestBroadcastWithPoliciesFiltersScorchUpdates verifies Scorch pipeline and
// terminal updates reach only clients with Scorch access to the experiment.
func TestBroadcastWithPoliciesFiltersScorchUpdates(t *testing.T) {
	set, clients := newTestClients(
		[]*v1.PolicySpec{scorchGet(), policy("experiments", "get", "exp-a")},
		[]*v1.PolicySpec{scorchGet(), policy("experiments", "get", "exp-b")},
		[]*v1.PolicySpec{policy("experiments", "get", "exp-a")},
	)

	BroadcastWithPolicies(
		[]*bt.RequestPolicy{
			bt.NewRequestPolicy("scorch", "get", ""),
			bt.NewRequestPolicy("experiments", "get", "exp-a"),
		},
		bt.NewResource("apps/scorch", "exp-a", "pipeline-update"),
		nil,
	)

	deliver(set, nextBroadcast(t))

	want := []bool{true, false, false}
	got := received(clients)

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("client %d received update: got %t, want %t", i, got[i], want[i])
		}
	}
}

// TestDeliverCombinesSinglePolicyAndPolicies verifies a message with both a
// single request policy and a policy list requires all of them.
func TestDeliverCombinesSinglePolicyAndPolicies(t *testing.T) {
	set, clients := newTestClients(
		[]*v1.PolicySpec{scorchGet(), policy("experiments", "get", "exp-a")},
		[]*v1.PolicySpec{scorchGet()},
		[]*v1.PolicySpec{policy("experiments", "get", "exp-a")},
	)

	deliver(set, bt.Publish{
		RequestPolicy:   bt.NewRequestPolicy("experiments", "get", "exp-a"),
		RequestPolicies: []*bt.RequestPolicy{bt.NewRequestPolicy("scorch", "get", "")},
		Resource:        bt.NewResource("apps/scorch", "exp-a", "pipeline-update"),
		Result:          nil,
	})

	want := []bool{true, false, false}
	got := received(clients)

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("client %d received update: got %t, want %t", i, got[i], want[i])
		}
	}
}

// TestPublishTriggerScorchUsesScorchScope verifies Scorch run events go to
// users who can view Scorch for the experiment, while other app trigger events
// still require experiments/trigger.
func TestPublishTriggerScorchUsesScorchScope(t *testing.T) {
	set, clients := newTestClients(
		// Scorch viewer for exp-a without trigger permission.
		[]*v1.PolicySpec{scorchGet(), policy("experiments", "get", "exp-a")},
		// Trigger permission for exp-a without Scorch access.
		[]*v1.PolicySpec{policy("experiments", "get", "exp-a"), policy("experiments/trigger", "create", "exp-a")},
		// Scorch viewer for another experiment.
		[]*v1.PolicySpec{scorchGet(), policy("experiments", "get", "exp-b")},
	)

	tests := []struct {
		app  string
		want []bool
	}{
		{app: "scorch", want: []bool{true, false, false}},
		{app: "ntp", want: []bool{false, true, false}},
	}

	for _, test := range tests {
		publishTrigger(app.TriggerPublication{
			Experiment: "exp-a", App: test.app, State: "start",
		})

		deliver(set, nextBroadcast(t))

		got := received(clients)

		for i := range test.want {
			if got[i] != test.want[i] {
				t.Errorf("%s trigger: client %d received event: got %t, want %t", test.app, i, got[i], test.want[i])
			}
		}
	}
}

func TestPublishTriggerError(t *testing.T) {
	publishTrigger(app.TriggerPublication{
		Experiment: "exp-a",
		App:        "scorch",
		Verb:       "delete",
		Resource:   "exp-a/0",
		State:      triggerStateError,
		Error:      errors.New("run failed"),
	})

	pub := nextBroadcast(t)

	if pub.Resource.Type != "apps/scorch" || pub.Resource.Name != "exp-a/0" {
		t.Fatalf("unexpected resource: %+v", pub.Resource)
	}

	want := []bt.RequestPolicy{
		{Resource: "scorch", Verb: "get", ResourceName: ""},
		{Resource: "experiments", Verb: "get", ResourceName: "exp-a"},
	}

	if len(pub.RequestPolicies) != len(want) {
		t.Fatalf("unexpected request policies: %+v", pub.RequestPolicies)
	}

	for i := range want {
		if *pub.RequestPolicies[i] != want[i] {
			t.Fatalf("unexpected request policy %d: %+v", i, pub.RequestPolicies[i])
		}
	}

	var result map[string]string
	if err := json.Unmarshal(pub.Result, &result); err != nil {
		t.Fatalf("decoding trigger error result: %v", err)
	}

	if result[triggerStateError] != "run failed" {
		t.Fatalf("unexpected trigger error result: %v", result)
	}
}
