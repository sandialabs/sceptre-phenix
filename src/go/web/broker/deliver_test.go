package broker

import (
	"encoding/json"
	"testing"

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

	saved := clients
	clients = make(map[*Client]bool)

	for _, c := range cs {
		clients[c] = true
	}

	t.Cleanup(func() { clients = saved })
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
	var (
		reader     = &Client{role: roleAllowing("logs", "get"), publish: make(chan any, 4)}
		notShowing = &Client{role: roleAllowing("logs", "get"), publish: make(chan any, 4)}
		forbidden  = &Client{role: roleAllowing("vms", "list"), publish: make(chan any, 4)}
	)

	reader.logsSubscribed.Store(true)
	forbidden.logsSubscribed.Store(true)

	withClients(t, reader, notShowing, forbidden)

	BroadcastLog(json.RawMessage(`{"msg": "hello"}`))
	deliver(<-broadcast)

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

	want, err := json.Marshal(bt.Publish{Resource: bt.NewResource("experiment/vm", "exp/vm1", "update"), Result: result})
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
