package broker

import (
	"encoding/json"
	"phenix/app"
	"phenix/util/pubsub"
)

var (
	clients    = make(map[*Client]bool)
	broadcast  = make(chan Publish)
	register   = make(chan *Client)
	unregister = make(chan *Client)
)

func Start() {
	sub := pubsub.Subscribe("trigger-app")

	for {
		select {
		case pub := <-sub:
			trigger := pub.(app.Publication)

			var action string

			switch trigger.State {
			case "start":
				action = "triggering"
			case "success":
				action = "trigger"
			case "error":
				action = "errorTriggering"
			default:
				continue
			}

			policy := NewRequestPolicy("experiments/trigger", "create", trigger.Experiment)
			resource := NewResource("experiment", trigger.Experiment, action)

			broadcast <- Publish{RequestPolicy: policy, Resource: resource, Result: nil}
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
