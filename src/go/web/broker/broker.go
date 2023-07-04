package broker

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"phenix/api/vm"
	"phenix/app"
	"phenix/util/pubsub"
	"phenix/web/util"

	putil "phenix/util"
	bt "phenix/web/broker/brokertypes"
)

var (
	clients    = make(map[*Client]bool)
	broadcast  = make(chan bt.Publish, 1024)
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
				trigger = pub.(app.TriggerPublication)
				typ     = fmt.Sprintf("apps/%s", trigger.App)

				policy   = bt.NewRequestPolicy("experiments/trigger", "create", trigger.Experiment)
				resource = bt.NewResource(typ, trigger.Experiment, trigger.State)
			)

			if trigger.Verb != "" {
				policy.Verb = trigger.Verb
			}

			if trigger.Resource != "" {
				resource.Name = trigger.Resource
			}

			if trigger.State == "error" {
				var (
					humanized *putil.HumanizedError
					result    []byte
				)

				if errors.As(trigger.Error, &humanized) {
					result, _ = json.Marshal(map[string]any{"error": humanized.Humanized()})
				} else {
					result, _ = json.Marshal(map[string]any{"error": trigger.Error.Error()})
				}

				broadcast <- bt.Publish{RequestPolicy: policy, Resource: resource, Result: result}
			} else {
				broadcast <- bt.Publish{RequestPolicy: policy, Resource: resource, Result: nil}
			}
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

			policy := bt.NewRequestPolicy("vms/start", "update", strings.Join(names, "_"))
			resource := bt.NewResource("experiment/vm", delayed, "start")

			broadcast <- bt.Publish{RequestPolicy: policy, Resource: resource, Result: body}
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

func Broadcast(policy *bt.RequestPolicy, resource *bt.Resource, msg json.RawMessage) {
	broadcast <- bt.Publish{RequestPolicy: policy, Resource: resource, Result: msg}
}
