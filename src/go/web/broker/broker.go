package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"phenix/api/job"
	"phenix/api/vm"
	"phenix/app"
	putil "phenix/util"
	"phenix/util/freshness"
	"phenix/util/plog"
	"phenix/util/pubsub"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/lifecycle"
	"phenix/web/util"
)

const (
	brokerChannelBuffer        = 1024
	logBufferSize              = 1024
	triggerStateError          = "error"
	logResourceType            = "log"
	experimentResourceType     = "experiment"
	vmsResourceType            = "experiment/vms"
	delayedStartScreenshotSize = "215"
	// how often the broker tells of the clients and log lines it dropped.
	dropReportInterval = 30 * time.Second
	// how often Shutdown checks whether clients have taken their close frame.
	closePollInterval = 10 * time.Millisecond
)

var (
	clients    = make(map[*Client]bool)                         //nolint:gochecknoglobals // global state
	broadcast  = make(chan encodedPublish, brokerChannelBuffer) //nolint:gochecknoglobals // global state
	register   = make(chan *Client, brokerChannelBuffer)        //nolint:gochecknoglobals // global state
	unregister = make(chan *Client, brokerChannelBuffer)        //nolint:gochecknoglobals // global state
	// a pending request to close every client; see Shutdown.
	closeAll = make(chan closeRequest, 1) //nolint:gochecknoglobals // global state

	// Log lines have their own queue, which drops lines rather than wait: every
	// plog call in the process sends one, and none may block on the broker.
	logs = make(chan encodedPublish, logBufferSize) //nolint:gochecknoglobals // global state

	clientCount atomic.Int64  //nolint:gochecknoglobals // global state
	slowDropped atomic.Uint64 //nolint:gochecknoglobals // global state
	logsDropped atomic.Uint64 //nolint:gochecknoglobals // global state
	// log lines dropped that clients have not yet been told of.
	logsUntold atomic.Uint64 //nolint:gochecknoglobals // global state
	// connections of closed clients still taking their close frame.
	closingConns atomic.Int64 //nolint:gochecknoglobals // global state

	// reports drops; run calls it on its own goroutine, since logging sends to
	// the broker.
	dropReporter = func(clients, lines uint64) { //nolint:gochecknoglobals // replaced in tests
		plog.Warn(
			plog.TypeSystem,
			"WebSocket broker dropped slow clients or live log lines",
			"clients", clients,
			"log_lines", lines,
		)
	}
)

// Stats counts the broker's clients and queued broadcasts.
type Stats struct {
	Clients int64 `json:"clients"`
	// Queued and Capacity are the broadcasts waiting for the broker loop and
	// how many fit.
	Queued   int `json:"queued"`
	Capacity int `json:"capacity"`
}

// CurrentStats returns the broker's counters. It is safe to call from any
// goroutine.
func CurrentStats() Stats {
	return Stats{
		Clients:  clientCount.Load(),
		Queued:   len(broadcast),
		Capacity: cap(broadcast),
	}
}

// Dropped returns how many clients the broker has dropped for being too slow,
// and how many log lines it has dropped from the live stream, since it started.
func Dropped() (uint64, uint64) {
	return slowDropped.Load(), logsDropped.Load()
}

// encodedPublish is a broadcast marshaled once, before it reaches the broker,
// rather than once for each client it goes to.
type encodedPublish struct {
	pub  bt.Publish
	data []byte
	// the state the message leaves a VM in, if it tells of one
	vm *vmState
	// log lines dropped just before this one
	skipped uint64
	// the user the message is for, besides the clients its policy allows
	owner string
}

// encode marshals pub once for all the clients it goes to. It stamps pub
// with the next seq unless it has one: it runs as the message is published,
// on the goroutine that made the change the message tells of, so the seq is
// greater than that of any read that could have missed the change.
func encode(pub bt.Publish) (encodedPublish, bool) {
	if pub.Seq == 0 {
		pub.Seq = freshness.Seq()
	}

	data, err := json.Marshal(pub)
	if err != nil {
		// logged on its own goroutine, since encode runs on the broker's too
		go plog.Error(plog.TypeSystem, "marshaling message to be published", "err", err)

		return encodedPublish{}, false
	}

	out := encodedPublish{pub: pub, data: data, vm: nil, skipped: 0, owner: ""}

	if state, ok := vmStateOf(pub); ok {
		out.vm = &state
	}

	return out, true
}

// publish encodes pub and queues it for the clients allowed to see it.
func publish(pub bt.Publish) {
	if out, ok := encode(pub); ok {
		broadcast <- out
	}
}

// closeRequest asks the broker loop to close every client, and every one
// that registers later, with code and reason.
type closeRequest struct {
	code   int
	reason string
	// how many clients it closed
	closed chan int
}

// Start runs the broker until ctx is done.
func Start(ctx context.Context) {
	trigger, delayed := pubsub.Subscribe("trigger-app"), pubsub.Subscribe("delayed-start")

	defer pubsub.Unsubscribe("trigger-app", trigger)
	defer pubsub.Unsubscribe("delayed-start", delayed)

	run(ctx, trigger, delayed)
}

// Shutdown closes every client with close code 1001 (going away) and
// lifecycle.CloseReason. It returns how many were still open when ctx ended.
func Shutdown(ctx context.Context) int {
	req := closeRequest{code: websocket.CloseGoingAway, reason: lifecycle.CloseReason, closed: make(chan int, 1)}

	// sent even when ctx is already done, as with no grace period
	select {
	case closeAll <- req:
	default:
		select {
		case closeAll <- req:
		case <-ctx.Done():
			return int(clientCount.Load() + closingConns.Load())
		}
	}

	select {
	case <-req.closed:
	case <-ctx.Done():
		return int(clientCount.Load() + closingConns.Load())
	}

	ticker := time.NewTicker(closePollInterval)
	defer ticker.Stop()

	for closingConns.Load() > 0 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return int(closingConns.Load())
		}
	}

	return 0
}

// run hands broadcasts to clients until ctx is done. It owns the clients map,
// so it does only map edits and non-blocking sends: a client too slow to keep
// up is dropped, and its connection is closed on another goroutine.
func run(ctx context.Context, triggerSub, delayedSub <-chan any) {
	ticker := time.NewTicker(dropReportInterval)
	defer ticker.Stop()

	var reportedClients, reportedLines uint64

	// set once every client has been closed for shutdown
	var closer *closeRequest

	for {
		select {
		case <-ctx.Done():
			// a shutdown asked for just before the loop was stopped
			select {
			case req := <-closeAll:
				closeClients(req)
			default:
			}

			return
		case req := <-closeAll:
			closer = &req

			closeClients(req)
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

			// delivered here: publish queues on the channel this loop drains,
			// and would block it once that is full
			if out, ok := encode(bt.Publish{RequestPolicy: policy, Resource: resource, Result: result, Seq: 0}); ok {
				deliver(out)
			}
		case pub := <-delayedSub:
			delayed, _ := pub.(string)

			expName, vmName, ok := strings.Cut(delayed, "/")
			if !ok {
				go plog.Error(plog.TypeSystem, "unexpected delayed-start publication", "name", delayed)

				continue
			}

			// asks minimega for the VM and a screenshot, so it runs on its own
			// rather than holding up every other client's messages
			go publishDelayedStart(delayed, expName, vmName)
		case cli := <-register:
			if closer != nil {
				cli.shutdown(closer.code, closer.reason)

				continue
			}

			clients[cli] = true

			clientCount.Add(1)
		case cli := <-unregister:
			// the client has already closed its connection
			forget(cli)
		case out := <-broadcast:
			deliver(out)
		case out := <-logs:
			if out.skipped > 0 {
				deliverSkipped(out.skipped)
			}

			deliver(out)
		case <-ticker.C:
			// lines dropped after the last line that made it through
			if n := logsUntold.Swap(0); n > 0 {
				deliverSkipped(n)
			}

			droppedClients, droppedLines := slowDropped.Load(), logsDropped.Load()

			if droppedClients != reportedClients || droppedLines != reportedLines {
				go dropReporter(droppedClients-reportedClients, droppedLines-reportedLines)

				reportedClients, reportedLines = droppedClients, droppedLines
			}
		}
	}
}

// closeClients closes every client for req, and tells how many it closed.
func closeClients(req closeRequest) {
	n := len(clients)

	for cli := range clients {
		forget(cli)
		cli.shutdown(req.code, req.reason)
	}

	req.closed <- n
}

// forget removes cli from the broker's clients.
func forget(cli *Client) {
	if _, ok := clients[cli]; ok {
		delete(clients, cli)
		clientCount.Add(-1)
	}
}

// drop disconnects a client whose queue is full. The client reconnects and
// reloads what it shows.
func drop(cli *Client) {
	forget(cli)
	slowDropped.Add(1)

	cli.shutdown(websocket.CloseTryAgainLater, "client too slow")
}

// deliver hands a broadcast to every client allowed to see it: its owner, if
// it has one, and the clients its policy allows. Log lines go only to clients
// that asked for them.
func deliver(out encodedPublish) {
	policy := out.pub.RequestPolicy
	isLog := out.pub.Resource != nil && out.pub.Resource.Type == logResourceType

	for cli := range clients {
		select {
		case <-cli.done:
			// gone, though its unregister may not have arrived yet
			forget(cli)

			continue
		default:
		}

		if isLog && !cli.logsSubscribed.Load() {
			continue
		}

		var allow bool

		switch {
		case out.owner != "" && cli.user == out.owner:
			allow = true
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
			drop(cli)
		}
	}
}

// deliverSkipped tells the clients showing logs that n log lines were dropped
// from their live stream. The log file still has them.
func deliverSkipped(n uint64) {
	ts := time.Now().UTC()

	entry, err := json.Marshal(plog.LogEntry{
		Time:      ts.UnixNano(),
		Timestamp: ts.Format(plog.TimestampFormat),
		Level:     "WARN",
		Type:      string(plog.TypeSystem),
		Message:   fmt.Sprintf("%d log lines skipped while the server was busy; reload the page to see them", n),
	})
	if err != nil {
		return
	}

	if out, ok := encode(logPublish(entry)); ok {
		deliver(out)
	}
}

func Broadcast(policy *bt.RequestPolicy, resource *bt.Resource, msg json.RawMessage) {
	publish(bt.Publish{RequestPolicy: policy, Resource: resource, Result: msg, Seq: 0})
}

// BroadcastLog sends a log entry to the clients showing logs that may read
// them. Every log call in the process comes through here, so it never waits:
// when the broker falls behind the line is dropped from the live stream (the
// log file still has it), and clients are told how many lines they missed.
func BroadcastLog(entry json.RawMessage) {
	out, ok := encode(logPublish(entry))
	if !ok {
		return
	}

	out.skipped = logsUntold.Swap(0)

	select {
	case logs <- out:
	default:
		logsUntold.Add(out.skipped + 1)
		logsDropped.Add(1)
	}
}

// maxJobItems is the most items of a job a job message carries; the rest are
// counted in ItemsOmitted, and GET /jobs/{id} returns them all.
const maxJobItems = 500

// PublishJob tells the job's owner, and the clients allowed to list jobs of
// its experiment (or, for a job of none, to list jobs/global), of a change to
// a job, or that it is gone (removed). The message leaves out the items'
// results, and items beyond maxJobItems.
func PublishJob(j job.Job, removed bool) {
	action := "update"
	if removed {
		action = "remove"
	}

	policy := bt.NewRequestPolicy("jobs", "list", j.Experiment)
	if j.Experiment == "" {
		policy = bt.NewRequestPolicy("jobs/global", "list", "")
	}

	if len(j.Items) > maxJobItems {
		j.ItemsOmitted = len(j.Items) - maxJobItems
		j.Items = j.Items[:maxJobItems]
	}

	items := make([]job.Item, len(j.Items))
	for i, item := range j.Items {
		item.Result = nil
		items[i] = item
	}

	j.Items = items

	body, err := json.Marshal(j)
	if err != nil {
		go plog.Error(plog.TypeSystem, "marshaling job to be published", "job", j.ID, "err", err)

		return
	}

	out, ok := encode(bt.Publish{
		RequestPolicy: policy,
		Resource:      bt.NewResource("job", j.ID, action),
		Result:        body,
		Seq:           0, // stamped by encode
	})
	if !ok {
		return
	}

	out.owner = j.Owner

	broadcast <- out
}

func logPublish(entry json.RawMessage) bt.Publish {
	return bt.Publish{
		RequestPolicy: bt.NewRequestPolicy("logs", "get", ""),
		Resource:      bt.NewResource(logResourceType, "phenix", "update"),
		Result:        entry,
		Seq:           0, // stamped by encode
	}
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
	v, err := vm.Get(context.Background(), expName, vmName)
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
		Seq:           0, // stamped by encode
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
		Seq:           0, // stamped by encode
	})
}
