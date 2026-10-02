package broker

import (
	"encoding/json"
	"testing"

	bt "phenix/web/broker/brokertypes"
)

// A client tracks which VMs in view are running from the broadcasts it is
// sent: the running flag of the VM a message carries, else what the message's
// action implies. An experiment stopping stops all of its VMs. A reply to the
// client's own request changes nothing tracked.
func TestClientTracksRunningVMs(t *testing.T) {
	vm := func(name, action string) *bt.Resource { return bt.NewResource("experiment/vm", name, action) }
	experiment := func(action string) *bt.Resource { return bt.NewResource("experiment", "exp", action) }

	tests := map[string]struct {
		resource *bt.Resource
		result   string
		// sent to the client alone, as a reply to its own request
		reply bool
		// whether exp/vm1 runs before and after the message
		before, after bool
	}{
		"start":    {resource: vm("exp/vm1", "start"), before: false, after: true},
		"stop":     {resource: vm("exp/vm1", "stop"), before: true, after: false},
		"shutdown": {resource: vm("exp/vm1", "shutdown"), result: `{"name": "vm1", "running": false}`, before: true},
		"delete":   {resource: vm("exp/vm1", "delete"), before: true, after: false},
		// redeploying and resetting restart a VM without a start message
		"redeploy": {resource: vm("exp/vm1", "redeployed"), result: `{"name": "vm1", "running": true}`, after: true},
		"reset":    {resource: vm("exp/vm1", "reset"), before: false, after: true},
		"the VM a message carries wins over its action": {
			resource: vm("exp/vm1", "start"), result: `{"name": "vm1", "running": false}`, before: true, after: false,
		},
		"an update carrying the VM": {
			resource: vm("exp/vm1", "update"), result: `{"name": "vm1", "running": true}`, before: false, after: true,
		},
		"a VM out of view":                 {resource: vm("exp/vm9", "start"), before: false, after: false},
		"another experiment's VM":          {resource: vm("third/vm1", "start"), before: false, after: false},
		"a VM name without its experiment": {resource: vm("vm1", "start"), before: false, after: false},
		"an action that implies no state":  {resource: vm("exp/vm1", "restarting"), before: false, after: false},
		"an error": {
			resource: vm("exp/vm1", "error"), result: `{"error": "unable to start delayed VM vm1"}`, before: false, after: false,
		},
		"a screenshot": {
			resource: bt.NewResource("experiment/vm/screenshot", "exp/vm1", "update"), before: true, after: true,
		},
		// other resources name the VM too, with actions that would stop it
		"a forward to the VM deleted": {
			resource: bt.NewResource("experiment/vm/forward", "exp/vm1", "delete"), before: true, after: true,
		},
		"experiment stopping":        {resource: experiment("stopping"), before: true, after: false},
		"experiment stopped":         {resource: experiment("stop"), before: true, after: false},
		"experiment deleted":         {resource: experiment("delete"), before: true, after: false},
		"experiment starting":        {resource: experiment("starting"), before: true, after: true},
		"experiment failing to stop": {resource: experiment("errorStopping"), before: true, after: true},
		"another experiment stopping": {
			resource: bt.NewResource("experiment", "third", "stop"), before: true, after: true,
		},
		"a VM list the client asked for": {
			resource: bt.NewResource("experiment/vms", "exp", "list"),
			result:   `{"total": 2, "vms": [{"name": "vm1", "running": true}, {"name": "vm2", "running": true}]}`,
			reply:    true, before: false, after: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// exp/vm2 changes only with its experiment; other/vm1 never does
			c := &Client{publish: make(chan any, 1), vms: []vmScope{
				{exp: "exp", name: "vm1", running: tc.before},
				{exp: "exp", name: "vm2", running: tc.before},
				{exp: "other", name: "vm1", running: tc.before},
			}}

			var result json.RawMessage
			if tc.result != "" {
				result = json.RawMessage(tc.result)
			}

			if tc.reply {
				c.send(bt.Publish{Resource: tc.resource, Result: result})
				c.trackVMState(<-c.publish)
			} else {
				Broadcast(nil, tc.resource, result)
				c.trackVMState(<-broadcast)
			}

			vm2 := tc.before
			if tc.resource.Type == "experiment" {
				vm2 = tc.after
			}

			want := []bool{tc.after, vm2, tc.before}
			for i, v := range c.vms {
				if v.running != want[i] {
					t.Errorf("%s/%s running = %v, want %v", v.exp, v.name, v.running, want[i])
				}
			}
		})
	}
}
