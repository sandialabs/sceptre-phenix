package broker

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"phenix/api/vm"
	"phenix/app"
	"phenix/util/pubsub"
	"phenix/web/util"
)

var (
	clients    = make(map[*Client]bool)
	broadcast  = make(chan Publish, 1024)
	register   = make(chan *Client, 1024)
	unregister = make(chan *Client, 1024)
)

func Start() {
	triggerSub := pubsub.Subscribe("trigger-app")
	delayedSub := pubsub.Subscribe("delayed-start")

	for {
		select {
		case pub := <-triggerSub:
			var (
				trigger = pub.(app.Publication)
				state   string
			)

			switch trigger.State {
			case "start":
				state = "triggered"
			case "error":
				state = "triggerError"
			case "success":
				state = "triggerSuccess"
			default:
				continue
			}

			var (
				policy   = NewRequestPolicy("experiments/trigger", "create", trigger.Experiment)
				resource = NewResource("experiment/apps", trigger.Experiment, state)
				result   = map[string]any{"app": trigger.App}
			)

			if trigger.State == "error" {
				result["error"] = trigger.Error.Error()
			}

			payload, _ := json.Marshal(result)

			broadcast <- Publish{RequestPolicy: policy, Resource: resource, Result: payload}
		case pub := <-delayedSub:
			delayed := pub.(string)
			names := strings.Split(delayed, "/")

			v, err := vm.Get(names[0], names[1])
			if err != nil {
				continue
			}

			screenshot, err := util.GetScreenshot(names[0], names[1], "215")
			if err == nil {
				v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)
			}

			body, err := marshaler.Marshal(util.VMToProtobuf(names[0], *v, nil))
			if err != nil {
				continue
			}

			policy := NewRequestPolicy("vms/start", "update", strings.Join(names, "_"))
			resource := NewResource("experiment/vm", delayed, "start")

			broadcast <- Publish{RequestPolicy: policy, Resource: resource, Result: body}
		case cli := <-register:
			clients[cli] = true
		case cli := <-unregister:
			if _, ok := clients[cli]; ok {
				cli.Stop()
				delete(clients, cli)
			}
		case pub := <-broadcast:
			for cli := range clients {
				var (
					policy = pub.RequestPolicy
					allow  bool
				)

				if policy == nil {
					allow = true
				} else if policy.ResourceName == "" {
					allow = cli.role.Allowed(policy.Resource, policy.Verb)
				} else {
					allow = cli.role.Allowed(policy.Resource, policy.Verb, policy.ResourceName)
				}

				if allow {
					select {
					case cli.publish <- pub:
					default:
						cli.Stop()
						delete(clients, cli)
					}
				}
			}
		}
	}
}

func Broadcast(policy *RequestPolicy, resource *Resource, msg json.RawMessage) {
	broadcast <- Publish{RequestPolicy: policy, Resource: resource, Result: msg}
}
