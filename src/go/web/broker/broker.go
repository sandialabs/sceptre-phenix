package broker

import (
	"encoding/json"
	"fmt"

	"phenix/app"
	"phenix/util/pubsub"
)

var (
	clients    = make(map[*Client]bool)
	broadcast  = make(chan Publish, 1024)
	register   = make(chan *Client, 1024)
	unregister = make(chan *Client, 1024)
)

func Start() {
	triggerSub := pubsub.Subscribe("trigger-app")

	for {
		select {
		case pub := <-triggerSub:
			trigger := pub.(app.Publication)

			typ := fmt.Sprintf("apps/%s", trigger.App)

			policy := NewRequestPolicy("experiments/trigger", "create", trigger.Experiment)
			resource := NewResource(typ, trigger.Experiment, trigger.State)

			if trigger.State == "error" {
				result, _ := json.Marshal(map[string]interface{}{"error": trigger.Error.Error()})
				broadcast <- Publish{RequestPolicy: policy, Resource: resource, Result: result}
			} else {
				broadcast <- Publish{RequestPolicy: policy, Resource: resource, Result: nil}
			}
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
