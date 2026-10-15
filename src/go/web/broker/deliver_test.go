package broker

import (
	"encoding/json"
	"testing"

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
		got := received(tc.c)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %d messages, want %v", name, len(got), tc.want)

			continue
		}

		for i, out := range got {
			if out.pub.Resource.Type != tc.want[i] {
				t.Errorf("%s: message %d is %s, want %s", name, i, out.pub.Resource.Type, tc.want[i])
			}
		}
	}
}

func TestBroadcastIsEncodedOnce(t *testing.T) {
	result := json.RawMessage(`{"name": "vm1", "running": true}`)
	Broadcast(nil, bt.NewResource("experiment/vm", "exp/vm1", "update"), result)

	out := <-broadcast

	want, err := json.Marshal(bt.Publish{
		Resource: bt.NewResource("experiment/vm", "exp/vm1", "update"), Result: result, Seq: out.pub.Seq,
	})
	if err != nil {
		t.Fatal(err)
	}

	if string(out.data) != string(want) {
		t.Errorf("data = %s, want %s", out.data, want)
	}

	b, err := encodeMessage(out)
	if err != nil || string(b) != string(want) {
		t.Errorf("encodeMessage() = %s, %v; want the broadcast's data", b, err)
	}
}

// A job message reaches the job's owner and the clients allowed to list jobs
// of its experiment, without the items' results, and with at most
// maxJobItems items.
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
		got := received(tc.c)
		if (len(got) == 1) != tc.want {
			t.Errorf("%s: got %d messages, want it: %t", name, len(got), tc.want)

			continue
		}

		if !tc.want {
			continue
		}

		var sent job.Job
		if err := json.Unmarshal(got[0].pub.Result, &sent); err != nil {
			t.Fatal(err)
		}

		if got[0].pub.Resource.Type != "job" || got[0].pub.Resource.Action != "update" {
			t.Errorf("%s: resource %+v", name, got[0].pub.Resource)
		}

		if len(sent.Items) != maxJobItems || sent.ItemsOmitted != 2 || sent.Items[0].Result != nil {
			t.Errorf("%s: %d items, %d omitted, result %s", name, len(sent.Items), sent.ItemsOmitted, sent.Items[0].Result)
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
			t.Errorf("job of no experiment, %s: got %d messages, want %d", name, len(got), tc.want)
		} else if tc.want == 1 && got[0].pub.Resource.Action != "remove" {
			t.Errorf("removal sent as %s", got[0].pub.Resource.Action)
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

// Every broadcast carries a seq greater than the last one's, so clients can
// order them against each other and against REST responses.
func TestBroadcastsCarryIncreasingSeq(t *testing.T) {
	var last uint64

	for range 3 {
		Broadcast(nil, bt.NewResource("experiment", "exp", "update"), json.RawMessage(`{}`))

		out := <-broadcast

		var msg struct {
			Seq uint64 `json:"seq"`
		}

		if err := json.Unmarshal(out.data, &msg); err != nil || msg.Seq <= last || msg.Seq != out.pub.Seq {
			t.Fatalf("broadcast %s after seq %d: %v; want a larger seq", out.data, last, err)
		}

		last = msg.Seq
	}
}
