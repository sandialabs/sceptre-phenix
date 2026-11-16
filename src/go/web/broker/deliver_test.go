package broker

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/api/job"
	v1 "phenix/types/version/v1"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/rbac"
)

func roleAllowing(resource, verb string) rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{resource}, Verbs: []string{verb}},
	}}}
}

// withClients swaps the broker's clients for the given ones for one test.
func withClients(t *testing.T, cs ...*Client) {
	t.Helper()

	saved, savedCount := clients, clientCount.Load()
	clients = make(map[*Client]bool)

	for _, c := range cs {
		clients[c] = true
	}

	// counted as the broker loop counts the clients it registers
	clientCount.Store(int64(len(cs)))

	t.Cleanup(func() {
		clients = saved

		clientCount.Store(savedCount)
	})
}

func received(c *Client) []encodedPublish {
	var got []encodedPublish

	for len(c.publish) > 0 {
		out, _ := (<-c.publish).(encodedPublish)
		got = append(got, out)
	}

	return got
}

//nolint:paralleltest // clients is shared package state
func TestLogsGoOnlyToSubscribedClientsAllowedToReadThem(t *testing.T) {
	withLogQueue(t)

	var (
		reader     = &Client{role: roleAllowing("logs", "get"), publish: make(chan any, 4)}
		notShowing = &Client{role: roleAllowing("logs", "get"), publish: make(chan any, 4)}
		forbidden  = &Client{role: roleAllowing("vms", "list"), publish: make(chan any, 4)}
	)

	reader.logsSubscribed.Store(true)
	forbidden.logsSubscribed.Store(true)

	withClients(t, reader, notShowing, forbidden)

	BroadcastLog(json.RawMessage(`{"msg": "hello"}`))
	deliver(<-logs)

	Broadcast(nil, bt.NewResource("experiment", "exp", "start"), nil)
	deliver(<-broadcast)

	for name, tc := range map[string]struct {
		c    *Client
		want []string
	}{
		"subscribed":     {reader, []string{"log", "experiment"}},
		"not subscribed": {notShowing, []string{"experiment"}},
		"forbidden":      {forbidden, []string{"experiment"}},
	} {
		var types []string
		for _, out := range received(tc.c) {
			types = append(types, out.pub.Resource.Type)
		}

		if diff := cmp.Diff(tc.want, types); diff != "" {
			t.Errorf("deliver to the %s client: resource types mismatch (-want +got):\n%s", name, diff)
		}
	}
}

func TestBroadcastIsEncodedOnce(t *testing.T) {
	result := json.RawMessage(`{"name": "vm1", "running": true}`)
	Broadcast(nil, bt.NewResource("experiment/vm", "exp/vm1", "update"), result)

	out := <-broadcast

	want := map[string]any{
		"resource": map[string]any{"type": "experiment/vm", "name": "exp/vm1", "action": "update"},
		"result":   map[string]any{"name": "vm1", "running": true},
		"seq":      float64(out.pub.Seq),
	}

	var got map[string]any
	if err := json.Unmarshal(out.data, &got); err != nil {
		t.Fatalf("decoding Broadcast(experiment/vm exp/vm1 update) data %s: %v", out.data, err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Broadcast(experiment/vm exp/vm1 update) data mismatch (-want +got):\n%s", diff)
	}

	// the bytes are the contract here: encodeMessage reuses the broadcast's encoding
	if b, err := encodeMessage(out); err != nil || string(b) != string(out.data) {
		t.Errorf("encodeMessage(broadcast) = %s, %v; want the broadcast's data %s", b, err, out.data)
	}

	Broadcast(nil, bt.NewResource("experiment/vm", "exp/vm1", "update"), result)

	if next := <-broadcast; out.pub.Seq == 0 || next.pub.Seq <= out.pub.Seq {
		t.Errorf("Broadcast seqs = %d then %d, want nonzero and increasing", out.pub.Seq, next.pub.Seq)
	}
}

// A job message reaches its owner and clients allowed to list its experiment's
// jobs, capped at maxJobItems without results.
//
//nolint:paralleltest // clients is shared package state
func TestJobsGoToTheirOwnerAndJobAdmins(t *testing.T) {
	var (
		owner    = &Client{role: roleAllowing("vms", "list"), user: "alice", publish: make(chan any, 4)}
		other    = &Client{role: roleAllowing("vms", "list"), user: "bob", publish: make(chan any, 4)}
		admin    = &Client{role: jobsAdmin(), user: "admin", publish: make(chan any, 4)}
		allExps  = &Client{role: roleAllowingNames("jobs", "list", "*"), user: "erin", publish: make(chan any, 4)}
		expAdmin = &Client{role: roleAllowingNames("jobs", "list", "exp1"), user: "carol", publish: make(chan any, 4)}
		other2   = &Client{role: roleAllowingNames("jobs", "list", "exp2"), user: "dave", publish: make(chan any, 4)}
	)

	withClients(t, owner, other, admin, allExps, expAdmin, other2)

	items := make([]job.Item, maxJobItems+2)
	for i := range items {
		items[i] = job.Item{Name: "vm", Status: job.ItemSucceeded, Result: json.RawMessage(`{"big":true}`)}
	}

	PublishJob(job.Job{ID: "j1", Owner: "alice", Experiment: "exp1", Items: items}, false)
	deliver(<-broadcast)

	for name, tc := range map[string]struct {
		c    *Client
		want bool
	}{
		"owner": {owner, true}, "another user": {other, false}, "jobs admin": {admin, true},
		"admin of every experiment": {allExps, true},
		"admin of the experiment":   {expAdmin, true}, "admin of another experiment": {other2, false},
	} {
		wantCount := 0
		if tc.want {
			wantCount = 1
		}

		got := received(tc.c)
		if len(got) != wantCount {
			t.Errorf("PublishJob(j1 of exp1) to the %s client: %d messages, want %d", name, len(got), wantCount)

			continue
		}

		if !tc.want {
			continue
		}

		if r := got[0].pub.Resource; r.Type != "job" || r.Action != "update" {
			t.Errorf("PublishJob(j1 of exp1) to the %s client: resource %s %s, want job update", name, r.Type, r.Action)
		}

		var sent job.Job
		if err := json.Unmarshal(got[0].pub.Result, &sent); err != nil {
			t.Errorf("decoding PublishJob(j1 of exp1) to the %s client: %v", name, err)

			continue
		}

		if len(sent.Items) != maxJobItems || sent.ItemsOmitted != 2 || sent.Items[0].Result != nil {
			t.Errorf("PublishJob(j1 of exp1, %d items) to the %s client = %d items, %d omitted, first result %s; want %d, 2, none",
				len(items), name, len(sent.Items), sent.ItemsOmitted, sent.Items[0].Result, maxJobItems)
		}
	}

	// a job of no experiment reaches only admins of jobs/global, not even
	// admins of every experiment's jobs
	PublishJob(job.Job{ID: "j2", Owner: "bob"}, true)
	deliver(<-broadcast)

	for name, tc := range map[string]struct {
		c    *Client
		want int
	}{
		"owner": {other, 1}, "jobs admin": {admin, 1}, "admin of every experiment": {allExps, 0},
		"admin of an experiment": {expAdmin, 0},
	} {
		if got := received(tc.c); len(got) != tc.want {
			t.Errorf("PublishJob(j2 of no experiment) to the %s client: %d messages, want %d", name, len(got), tc.want)
		} else if tc.want == 1 && got[0].pub.Resource.Action != "remove" {
			t.Errorf("PublishJob(j2, removed) to the %s client: action %s, want remove", name, got[0].pub.Resource.Action)
		}
	}
}

// jobsAdmin may list every job, of any experiment or of none.
func jobsAdmin() rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"jobs"}, ResourceNames: []string{"*"}, Verbs: []string{"list"}},
		{Resources: []string{"jobs/global"}, Verbs: []string{"list"}},
	}}}
}

func roleAllowingNames(resource, verb string, names ...string) rbac.Role {
	return rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{resource}, ResourceNames: names, Verbs: []string{verb}},
	}}}
}
