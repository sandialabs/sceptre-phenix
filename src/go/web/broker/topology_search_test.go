package broker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/gorilla/websocket"

	"phenix/api/vm"
	v1 "phenix/types/version/v1"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/rbac"
)

// A topology search answers with the nodes of the term, hostname when the request names none.
//
//nolint:paralleltest // replaces the client's topology call
func TestSearchTopologyAnswers(t *testing.T) {
	withDeps(t)

	deps.topology = func(exp string) (vm.TopologySearch, error) {
		if exp != "exp" {
			t.Errorf("topology(%q), want exp", exp)
		}

		return vm.TopologySearch{
			Hostname: map[string]int{"web": 2},
			IP:       map[string][]int{"10.0.0.9": {3}, "10.0.0.1": {1}, "10.1.0.1": {0}},
		}, nil
	}

	role := rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"experiments/topology"}, ResourceNames: []string{"exp"}, Verbs: []string{"get"}},
	}}}

	_, conn := dialTestClient(t, role)

	tests := []struct {
		request string
		want    map[string]any
	}{
		{
			`{"term": "ip", "value": "10.0.0.0/24"}`,
			map[string]any{"term": "ip", "value": "10.0.0.0/24", "results": map[string]any{"nodes": []any{1.0, 3.0}}},
		},
		{
			`{"value": "web"}`,
			map[string]any{"term": "hostname", "value": "web", "results": map[string]any{"nodes": []any{2.0}}},
		},
	}

	for _, tt := range tests {
		msg := `{"resource": {"type": "experiment/topology", "name": "exp", "action": "search"}, "request": ` +
			tt.request + `}`
		if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
			t.Fatalf("conn.WriteMessage(%s) = %v", msg, err)
		}

		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("conn.SetReadDeadline() = %v", err)
		}

		var reply bt.Publish
		if err := conn.ReadJSON(&reply); err != nil {
			t.Fatalf("reading the reply to %s: %v", msg, err)
		}

		if diff := cmp.Diff(bt.NewResource("experiment/topology", "exp", "search"), reply.Resource); diff != "" {
			t.Errorf("reply to %s: resource mismatch (-want +got):\n%s", msg, diff)
		}

		var got map[string]any
		if err := json.Unmarshal(reply.Result, &got); err != nil {
			t.Fatalf("decoding the reply %s: %v", reply.Result, err)
		}

		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("reply to %s mismatch (-want +got):\n%s", msg, diff)
		}
	}
}
