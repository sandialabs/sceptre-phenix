package broker

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"phenix/api/vm"
	"phenix/app"
	putil "phenix/util"
	"phenix/util/pubsub"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/rbac"
	"phenix/web/util"
)

const (
	brokerChannelBuffer = 1024
	triggerStateError   = "error"
)

var (
	clients    = make(map[*Client]bool)                     //nolint:gochecknoglobals // global state
	broadcast  = make(chan bt.Publish, brokerChannelBuffer) //nolint:gochecknoglobals // global state
	register   = make(chan *Client, brokerChannelBuffer)    //nolint:gochecknoglobals // global state
	unregister = make(chan *Client, brokerChannelBuffer)    //nolint:gochecknoglobals // global state
)

func Start() {
	triggerSub := pubsub.Subscribe("trigger-app")
	delayedSub := pubsub.Subscribe("delayed-start")

	for {
		select {
		case pub := <-triggerSub:
			trigger, _ := pub.(app.TriggerPublication)
			publishTrigger(trigger)
		case pub := <-delayedSub:
			delayed, _ := pub.(string)
			names := strings.Split(delayed, "/")

			v, err := vm.Get(names[0], names[1])
			if err != nil {
				continue
			}

			screenshot, err := util.GetScreenshot(names[0], names[1], "215")
			if err == nil {
				v.Screenshot = "data:image/png;base64," + base64.StdEncoding.EncodeToString(
					screenshot,
				)
			}

			body, err := marshaler.Marshal(util.VMToProtobuf(names[0], *v, nil))
			if err != nil {
				continue
			}

			policy := bt.NewRequestPolicy("vms/start", "update", strings.Join(names, "_"))
			resource := bt.NewResource("experiment/vm", delayed, "start")

			broadcast <- bt.Publish{
				RequestPolicy:   policy,
				RequestPolicies: nil,
				Resource:        resource,
				Result:          body,
			}
		case cli := <-register:
			clients[cli] = true
		case cli := <-unregister:
			if _, ok := clients[cli]; ok {
				cli.Stop()
				delete(clients, cli)
			}
		case pub := <-broadcast:
			deliver(clients, pub)
		}
	}
}

// deliver sends pub to each client whose role satisfies all of its request
// policies, dropping clients that cannot keep up.
func deliver(clients map[*Client]bool, pub bt.Publish) {
	policies := pub.RequestPolicies
	if pub.RequestPolicy != nil {
		policies = append([]*bt.RequestPolicy{pub.RequestPolicy}, policies...)
	}

	for cli := range clients {
		if requestPoliciesAllowed(cli.role, policies) {
			select {
			case cli.publish <- pub:
			default:
				cli.Stop()
				delete(clients, cli)
			}
		}
	}
}

func publishTrigger(trigger app.TriggerPublication) {
	var (
		policy   = bt.NewRequestPolicy("experiments/trigger", "create", trigger.Experiment)
		resource = bt.NewResource("apps/"+trigger.App, trigger.Experiment, trigger.State)
		result   []byte
	)

	if trigger.Verb != "" {
		policy.Verb = trigger.Verb
	}

	if trigger.Resource != "" {
		resource.Name = trigger.Resource
	}

	if trigger.State == triggerStateError {
		var humanized *putil.HumanizedError
		if errors.As(trigger.Error, &humanized) {
			result, _ = json.Marshal(map[string]any{triggerStateError: humanized.Humanized()})
		} else {
			result, _ = json.Marshal(map[string]any{triggerStateError: trigger.Error.Error()})
		}
	}

	broadcast <- bt.Publish{
		RequestPolicy:   nil,
		RequestPolicies: triggerPolicies(trigger.App, policy),
		Resource:        resource,
		Result:          result,
	}
}

func Broadcast(policy *bt.RequestPolicy, resource *bt.Resource, msg json.RawMessage) {
	broadcast <- bt.Publish{
		RequestPolicy:   policy,
		RequestPolicies: nil,
		Resource:        resource,
		Result:          msg,
	}
}

func BroadcastWithPolicies(
	policies []*bt.RequestPolicy,
	resource *bt.Resource,
	msg json.RawMessage,
) {
	broadcast <- bt.Publish{
		RequestPolicy:   nil,
		RequestPolicies: policies,
		Resource:        resource,
		Result:          msg,
	}
}

// triggerPolicies returns the policies a client needs to receive an app
// trigger event. Scorch run events go to everyone who can view Scorch for the
// experiment, matching Scorch pipeline updates, since Scorch runs are started
// and canceled with Scorch permissions rather than experiments/trigger.
func triggerPolicies(appName string, policy *bt.RequestPolicy) []*bt.RequestPolicy {
	if appName == "scorch" {
		return []*bt.RequestPolicy{
			bt.NewRequestPolicy("scorch", "get", ""),
			bt.NewRequestPolicy("experiments", "get", policy.ResourceName),
		}
	}

	return []*bt.RequestPolicy{policy}
}

func requestPoliciesAllowed(role rbac.Role, policies []*bt.RequestPolicy) bool {
	for _, policy := range policies {
		if policy == nil {
			continue
		}

		if policy.ResourceName == "" {
			if !role.Allowed(policy.Resource, policy.Verb) {
				return false
			}
		} else if !role.Allowed(policy.Resource, policy.Verb, policy.ResourceName) {
			return false
		}
	}

	return true
}
