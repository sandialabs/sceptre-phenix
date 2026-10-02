package broker

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"phenix/api/vm"
	"phenix/app"
	putil "phenix/util"
	"phenix/util/plog"
	"phenix/util/pubsub"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/util"
)

const (
	brokerChannelBuffer        = 1024
	triggerStateError          = "error"
	logResourceType            = "log"
	delayedStartScreenshotSize = "215"
)

var (
	clients    = make(map[*Client]bool)                         //nolint:gochecknoglobals // global state
	broadcast  = make(chan encodedPublish, brokerChannelBuffer) //nolint:gochecknoglobals // global state
	register   = make(chan *Client, brokerChannelBuffer)        //nolint:gochecknoglobals // global state
	unregister = make(chan *Client, brokerChannelBuffer)        //nolint:gochecknoglobals // global state
)

// encodedPublish is a broadcast marshaled once, before it reaches the broker,
// rather than once for each client it goes to.
type encodedPublish struct {
	pub  bt.Publish
	data []byte
	// the state the message leaves a VM in, if it tells of one
	vm *vmState
}

// publish encodes pub and queues it for the clients allowed to see it.
func publish(pub bt.Publish) {
	data, err := json.Marshal(pub)
	if err != nil {
		plog.Error(plog.TypeSystem, "marshaling message to be published", "err", err)

		return
	}

	out := encodedPublish{pub: pub, data: data, vm: nil}

	if state, ok := vmStateOf(pub); ok {
		out.vm = &state
	}

	broadcast <- out
}

func Start() {
	triggerSub := pubsub.Subscribe("trigger-app")
	delayedSub := pubsub.Subscribe("delayed-start")

	for {
		select {
		case pub := <-triggerSub:
			var (
				trigger, _ = pub.(app.TriggerPublication)
				typ        = "apps/" + trigger.App

				policy   = bt.NewRequestPolicy("experiments/trigger", "create", trigger.Experiment)
				resource = bt.NewResource(typ, trigger.Experiment, trigger.State)
			)

			if trigger.Verb != "" {
				policy.Verb = trigger.Verb
			}

			if trigger.Resource != "" {
				resource.Name = trigger.Resource
			}

			var result json.RawMessage
			if trigger.State == triggerStateError {
				result = errorResult(trigger.Error)
			}

			publish(bt.Publish{RequestPolicy: policy, Resource: resource, Result: result})
		case pub := <-delayedSub:
			delayed, _ := pub.(string)

			expName, vmName, ok := strings.Cut(delayed, "/")
			if !ok {
				plog.Error(plog.TypeSystem, "unexpected delayed-start publication", "name", delayed)

				continue
			}

			// asks minimega for the VM and a screenshot, so it runs on its own
			// rather than holding up every other client's messages
			go publishDelayedStart(delayed, expName, vmName)
		case cli := <-register:
			clients[cli] = true
		case cli := <-unregister:
			if _, ok := clients[cli]; ok {
				cli.Stop()
				delete(clients, cli)
			}
		case out := <-broadcast:
			deliver(out)
		}
	}
}

// deliver hands a broadcast to every client allowed to see it. Log lines go
// only to clients that asked for them.
func deliver(out encodedPublish) {
	policy := out.pub.RequestPolicy
	isLog := out.pub.Resource != nil && out.pub.Resource.Type == logResourceType

	for cli := range clients {
		if isLog && !cli.logsSubscribed.Load() {
			continue
		}

		var allow bool

		switch {
		case policy == nil:
			allow = true
		case policy.ResourceName == "":
			allow = cli.role.Allowed(policy.Resource, policy.Verb)
		default:
			allow = cli.role.Allowed(policy.Resource, policy.Verb, policy.ResourceName)
		}

		if !allow {
			continue
		}

		select {
		case cli.publish <- out:
		default:
			cli.Stop()
			delete(clients, cli)
		}
	}
}

func Broadcast(policy *bt.RequestPolicy, resource *bt.Resource, msg json.RawMessage) {
	publish(bt.Publish{RequestPolicy: policy, Resource: resource, Result: msg})
}

// BroadcastLog sends a log entry to the clients showing logs that may read
// them.
func BroadcastLog(entry json.RawMessage) {
	publish(bt.Publish{
		RequestPolicy: bt.NewRequestPolicy("logs", "get", ""),
		Resource:      bt.NewResource(logResourceType, "phenix", "update"),
		Result:        entry,
	})
}

// errorResult is the result of an app error publication: the error's message,
// humanized when it can be.
func errorResult(err error) []byte {
	msg := err.Error()

	var humanized *putil.HumanizedError
	if errors.As(err, &humanized) {
		msg = humanized.Humanize()
	}

	result, _ := json.Marshal(map[string]string{triggerStateError: msg})

	return result
}

// publishDelayedStart tells clients a VM held back by delayed start is now
// running. The screenshot follows separately: minimega takes it one command at
// a time with everything else, and a user stopping the VM meanwhile must not
// be undone by a late start message.
func publishDelayedStart(delayed, expName, vmName string) {
	v, err := vm.Get(expName, vmName)
	if err != nil || !v.Running {
		return
	}

	body, err := marshaler.Marshal(util.VMToProtobuf(expName, *v, nil))
	if err != nil {
		return
	}

	// RBAC resource names for VMs are exp/vm, as in the REST handlers.
	policy := bt.NewRequestPolicy("vms/start", "update", delayed)

	publish(bt.Publish{
		RequestPolicy: policy,
		Resource:      bt.NewResource("experiment/vm", delayed, "start"),
		Result:        body,
	})

	screenshot, err := util.GetScreenshot(expName, vmName, delayedStartScreenshotSize)
	if err != nil {
		return
	}

	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(screenshot)

	body, err = json.Marshal(util.WithRoot("screenshot", encoded))
	if err != nil {
		return
	}

	publish(bt.Publish{
		RequestPolicy: policy,
		Resource:      bt.NewResource("experiment/vm/screenshot", delayed, "update"),
		Result:        body,
	})
}
