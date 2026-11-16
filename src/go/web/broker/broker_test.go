package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	"phenix/api/job"
	"phenix/app"
	"phenix/store"
	"phenix/store/storetest"
	v1 "phenix/types/version/v1"
	putil "phenix/util"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/util/polltest"
	"phenix/util/pubsub"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/cache"
	"phenix/web/proto"
	"phenix/web/rbac"
)

func TestErrorResultCarriesMessage(t *testing.T) {
	t.Parallel()

	cause := errors.New("component foo failed")

	tests := map[string]struct {
		err  error
		want string
	}{
		"plain":     {err: fmt.Errorf("running run 0: %w", cause), want: "running run 0: component foo failed"},
		"humanized": {err: putil.HumanizeError(cause, "Unable to run SCORCH"), want: "Unable to run SCORCH (search error logs for "},
		"humanized under a wrap": {
			err:  fmt.Errorf("stage: %w", putil.HumanizeError(cause, "Unable to start")),
			want: "Unable to start (search error logs for ",
		},
		"humanized without a description": {err: putil.HumanizeError(cause, ""), want: "Component foo failed"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var result map[string]string
			if err := json.Unmarshal(errorResult(tc.err), &result); err != nil {
				t.Fatalf("decoding errorResult(%q): %v", tc.err, err)
			}

			if got := result["error"]; !strings.HasPrefix(got, tc.want) {
				t.Errorf("errorResult(%q) error = %q, want prefix %q", tc.err, got, tc.want)
			}
		})
	}
}

// encode stamps a seq unless the message has one, notes the VM state a VM
// message leaves, and refuses a message it cannot marshal.
func TestEncode(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pub    bt.Publish
		wantOK bool
		// 0 means any new seq
		wantSeq uint64
		wantVM  *vmState
	}{
		"stamps a seq": {pub: bt.Publish{Resource: bt.NewResource("experiment", "exp", "start")}, wantOK: true},
		"keeps a seq": {
			pub:    bt.Publish{Resource: bt.NewResource("experiment", "exp", "start"), Seq: 42},
			wantOK: true, wantSeq: 42,
		},
		"notes a VM's state": {
			pub:    bt.Publish{Resource: bt.NewResource("experiment/vm", "exp/vm1", "start")},
			wantOK: true, wantVM: &vmState{exp: "exp", name: "vm1", running: true},
		},
		"no resource": {pub: bt.Publish{Result: json.RawMessage(`{}`)}, wantOK: true},
		"a result that is not JSON": {
			pub: bt.Publish{Resource: bt.NewResource("experiment", "exp", "start"), Result: json.RawMessage(`{bad`)},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			out, ok := encode(tc.pub)
			if ok != tc.wantOK {
				t.Fatalf("encode() ok = %t, want %t", ok, tc.wantOK)
			}

			if !ok {
				if out.data != nil || out.pub.Resource != nil {
					t.Errorf("encode() of a message it refused = %+v, want the zero value", out)
				}

				return
			}

			if out.pub.Seq == 0 || (tc.wantSeq != 0 && out.pub.Seq != tc.wantSeq) {
				t.Errorf("encode() seq = %d, want %d (0: any new seq)", out.pub.Seq, tc.wantSeq)
			}

			var decoded bt.Publish
			if err := json.Unmarshal(out.data, &decoded); err != nil {
				t.Fatalf("decoding encode() data %s: %v", out.data, err)
			}

			if diff := cmp.Diff(out.pub.Resource, decoded.Resource); diff != "" || decoded.Seq != out.pub.Seq {
				t.Errorf("encode() data %s does not carry the message's resource and seq %d:\n%s", out.data, out.pub.Seq, diff)
			}

			if diff := cmp.Diff(tc.wantVM, out.vm, cmp.AllowUnexported(vmState{})); diff != "" {
				t.Errorf("encode() VM state mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// BroadcastLog queues a line with the count of lines dropped before it, drops
// a line that does not fit, and drops an unencodable line without counting it.
//
//nolint:paralleltest // swaps the log queue and its counters
func TestBroadcastLogQueues(t *testing.T) {
	tests := map[string]struct {
		entry  string
		full   bool
		untold uint64
		// what the queue holds after, and the counters after
		wantQueued, wantSkipped, wantUntold, wantDropped uint64
	}{
		"queues a line":                           {entry: `{"msg": "a"}`, wantQueued: 1},
		"queues a line with the drops before it":  {entry: `{"msg": "a"}`, untold: 3, wantQueued: 1, wantSkipped: 3},
		"drops a line that does not fit":          {entry: `{"msg": "a"}`, full: true, untold: 3, wantUntold: 4, wantDropped: 1},
		"drops a line that is not JSON uncounted": {entry: `{bad`, untold: 3, wantUntold: 3},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			withLogQueue(t)

			if tc.full {
				for range cap(logs) {
					logs <- encodedPublish{}
				}
			}

			logsUntold.Store(tc.untold)

			dropped := logsDropped.Load()

			BroadcastLog(json.RawMessage(tc.entry))

			queued := uint64(len(logs))
			if tc.full {
				queued -= uint64(cap(logs))
			}

			if queued != tc.wantQueued {
				t.Fatalf("BroadcastLog(%s) queued %d lines, want %d", tc.entry, queued, tc.wantQueued)
			}

			if got := logsUntold.Load(); got != tc.wantUntold {
				t.Errorf("BroadcastLog(%s) left %d untold drops, want %d", tc.entry, got, tc.wantUntold)
			}

			if got := logsDropped.Load() - dropped; got != tc.wantDropped {
				t.Errorf("BroadcastLog(%s) counted %d dropped lines, want %d", tc.entry, got, tc.wantDropped)
			}

			if tc.wantQueued == 0 {
				return
			}

			out := <-logs
			if out.skipped != tc.wantSkipped || out.pub.Resource.Type != logResourceType {
				t.Errorf("BroadcastLog(%s) queued %s line with %d skipped, want log with %d",
					tc.entry, out.pub.Resource.Type, out.skipped, tc.wantSkipped)
			}
		})
	}
}

// PublishJob caps the items it sends at maxJobItems, and sends nothing for a
// job it cannot marshal.
//
//nolint:paralleltest // reads the shared broadcast queue
func TestPublishJobItems(t *testing.T) {
	items := func(n int) []job.Item {
		out := make([]job.Item, n)
		for i := range out {
			out[i] = job.Item{Name: "vm", Result: json.RawMessage(`{"big": true}`)}
		}

		return out
	}

	tests := map[string]struct {
		job         job.Job
		wantSent    int
		wantItems   int
		wantOmitted int
	}{
		"no items":                {job: job.Job{ID: "j1"}, wantSent: 1},
		"the most items it sends": {job: job.Job{ID: "j1", Items: items(maxJobItems)}, wantSent: 1, wantItems: maxJobItems},
		"one item too many": {
			job: job.Job{ID: "j1", Items: items(maxJobItems + 1)}, wantSent: 1, wantItems: maxJobItems, wantOmitted: 1,
		},
		// time.Time marshals only years 0 to 9999
		"a job that does not marshal": {job: job.Job{ID: "j1", Created: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() {
				for len(broadcast) > 0 {
					<-broadcast
				}
			})

			PublishJob(tc.job, false)

			if got := len(broadcast); got != tc.wantSent {
				t.Fatalf("PublishJob(%s) queued %d messages, want %d", name, got, tc.wantSent)
			}

			if tc.wantSent == 0 {
				return
			}

			var sent struct {
				Items        []job.Item `json:"items"`
				ItemsOmitted int        `json:"itemsOmitted"`
			}
			if err := json.Unmarshal((<-broadcast).pub.Result, &sent); err != nil {
				t.Fatalf("decoding PublishJob(%s): %v", name, err)
			}

			if sent.Items == nil || len(sent.Items) != tc.wantItems || sent.ItemsOmitted != tc.wantOmitted {
				t.Errorf("PublishJob(%s) sent %d items (null: %t), %d omitted; want %d, not null, %d omitted",
					name, len(sent.Items), sent.Items == nil, sent.ItemsOmitted, tc.wantItems, tc.wantOmitted)
			}

			for i, item := range sent.Items {
				if item.Result != nil {
					t.Fatalf("PublishJob(%s) item %d result = %s, want none", name, i, item.Result)
				}
			}
		})
	}
}

// deliver forgets a client that is gone, though its unregister has not
// arrived, and sends it nothing.
//
//nolint:paralleltest // swaps the broker's clients
func TestDeliverForgetsGoneClients(t *testing.T) {
	gone, live := newFakeClient(nil), newFakeClient(nil)
	close(gone.done)

	withClients(t, gone, live)

	deliver(encodedPublish{})

	if _, ok := clients[gone]; ok || clientCount.Load() != 1 {
		t.Errorf("after deliver, the gone client is kept = %t, clients = %d; want false, 1", ok, clientCount.Load())
	}

	if got, want := len(gone.publish), 0; got != want {
		t.Errorf("deliver sent the gone client %d messages, want %d", got, want)
	}

	if got, want := len(live.publish), 1; got != want {
		t.Errorf("deliver sent the live client %d messages, want %d", got, want)
	}
}

// brokerDrainCloseAll empties the shutdown request queue when the test ends.
func brokerDrainCloseAll(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		select {
		case <-closeAll:
		default:
		}
	})
}

// Shutdown gives up when ctx ends, and returns the clients and connections
// still open.
//
//nolint:paralleltest // swaps the broker's clients and its shutdown queue
func TestShutdownGivesUpWhenContextEnds(t *testing.T) {
	polltest.Until(t, "earlier tests' connections closing", func() bool { return closingConns.Load() == 0 })

	t.Run("no broker loop takes the request", func(t *testing.T) {
		brokerDrainCloseAll(t)
		withClients(t, newFakeClient(nil), newFakeClient(nil))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if got := Shutdown(ctx); got != 2 {
			t.Errorf("Shutdown(done ctx) with no broker loop = %d open, want 2", got)
		}

		if len(closeAll) != 1 {
			t.Errorf("Shutdown(done ctx) queued %d requests, want 1 for the loop to take later", len(closeAll))
		}
	})

	t.Run("a request is already pending", func(t *testing.T) {
		brokerDrainCloseAll(t)
		withClients(t, newFakeClient(nil))

		closeAll <- closeRequest{closed: make(chan int, 1)}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if got := Shutdown(ctx); got != 1 {
			t.Errorf("Shutdown(done ctx) with a request pending = %d open, want 1", got)
		}
	})

	t.Run("a connection outlasts ctx", func(t *testing.T) {
		brokerDrainCloseAll(t)
		withClients(t)
		runBroker(t)

		closingConns.Add(1)
		t.Cleanup(func() { closingConns.Add(-1) })

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		if got := Shutdown(ctx); got != 1 {
			t.Errorf("Shutdown() with a connection still closing = %d open, want 1", got)
		}
	})
}

// A shutdown asked for just as the broker loop stops still closes every
// client and is answered, whichever the loop sees first.
//
//nolint:paralleltest // swaps the broker's clients and its shutdown queue
func TestRunAnswersShutdownWhileStopping(t *testing.T) {
	brokerDrainCloseAll(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// the loop picks at random between ctx and the request
	for range 16 {
		c := newFakeClient(newFakeConn(t))
		withClients(t, c)

		req := closeRequest{code: 1001, reason: "bye", closed: make(chan int, 1)}
		closeAll <- req

		run(ctx, nil, nil)

		select {
		case n := <-req.closed:
			if n != 1 || !isClosed(c.done) {
				t.Fatalf("run() stopping with a shutdown pending closed %d clients (client done: %t), want 1 (true)",
					n, isClosed(c.done))
			}
		default:
			t.Fatal("run() stopping with a shutdown pending did not answer it")
		}
	}
}

// brokerRun runs the broker loop on the given subscriptions until the test
// ends, like runBroker.
func brokerRun(t *testing.T, trigger, delayed <-chan any) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		run(ctx, trigger, delayed)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})
}

// brokerAdmin is a client that may see every message.
func brokerAdmin() *Client {
	c := newFakeClient(nil)
	c.role = rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"*", "*/*"}, ResourceNames: []string{"*", "*/*"}, Verbs: []string{"*"}},
	}}}

	return c
}

// brokerNext returns the next message c is sent.
func brokerNext(t *testing.T, c *Client) encodedPublish {
	t.Helper()

	var out encodedPublish

	polltest.Within(t, "the next message", func() { out, _ = (<-c.publish).(encodedPublish) })

	return out
}

// An app trigger goes to the clients allowed to trigger the experiment's
// apps, with the verb and resource name it gives, its error and its stage.
//
//nolint:paralleltest // runs the broker loop
func TestRunPublishesAppTriggers(t *testing.T) {
	tests := map[string]struct {
		trigger      app.TriggerPublication
		wantPolicy   *bt.RequestPolicy
		wantResource *bt.Resource
		wantResult   string
	}{
		"success": {
			trigger:      app.TriggerPublication{Experiment: "exp", App: "scorch", State: "success"},
			wantPolicy:   bt.NewRequestPolicy("experiments/trigger", "create", "exp"),
			wantResource: bt.NewResource("apps/scorch", "exp", "success"),
		},
		"a verb and resource of its own": {
			trigger: app.TriggerPublication{
				Experiment: "exp", Verb: "update", App: "scorch", Resource: "run-0", State: "start",
			},
			wantPolicy:   bt.NewRequestPolicy("experiments/trigger", "update", "exp"),
			wantResource: bt.NewResource("apps/scorch", "run-0", "start"),
		},
		"an error": {
			trigger:      app.TriggerPublication{Experiment: "exp", App: "scorch", State: "error", Error: errors.New("boom")},
			wantPolicy:   bt.NewRequestPolicy("experiments/trigger", "create", "exp"),
			wantResource: bt.NewResource("apps/scorch", "exp", "error"),
			wantResult:   `{"error":"boom"}`,
		},
		"a stage": {
			trigger:      app.TriggerPublication{Experiment: "exp", App: "soh", Stage: "configure", State: "success"},
			wantPolicy:   bt.NewRequestPolicy("experiments/trigger", "create", "exp"),
			wantResource: bt.NewResource("apps/soh", "exp", "success"),
			wantResult:   `{"stage":"configure"}`,
		},
		"an error of a stage": {
			trigger: app.TriggerPublication{
				Experiment: "exp", App: "soh", Stage: "cleanup", State: "error", Error: errors.New("boom"),
			},
			wantPolicy:   bt.NewRequestPolicy("experiments/trigger", "create", "exp"),
			wantResource: bt.NewResource("apps/soh", "exp", "error"),
			wantResult:   `{"error":"boom","stage":"cleanup"}`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c, other := brokerAdmin(), newFakeClient(nil)
			other.role = roleAllowingNames("experiments/trigger", "create", "other")
			withClients(t, c, other)

			trigger := make(chan any)
			brokerRun(t, trigger, nil)

			trigger <- tc.trigger

			out := brokerNext(t, c)

			if diff := cmp.Diff(tc.wantPolicy, out.pub.RequestPolicy); diff != "" {
				t.Errorf("trigger %+v: policy mismatch (-want +got):\n%s", tc.trigger, diff)
			}

			if diff := cmp.Diff(tc.wantResource, out.pub.Resource); diff != "" {
				t.Errorf("trigger %+v: resource mismatch (-want +got):\n%s", tc.trigger, diff)
			}

			if got := string(out.pub.Result); got != tc.wantResult {
				t.Errorf("trigger %+v: result = %s, want %s", tc.trigger, got, tc.wantResult)
			}

			// run delivers in order, so other getting this one proves tc.trigger reached it or was skipped
			trigger <- app.TriggerPublication{Experiment: "other", App: "scorch", State: "success"}

			if got := brokerNext(t, other).pub.Resource; got.Name != "other" {
				t.Errorf("trigger %+v: a client allowed only another experiment got %+v first, want its own", tc.trigger, got)
			}

			if n := len(other.publish); n != 0 {
				t.Errorf("trigger %+v: a client allowed only another experiment got %d more messages, want 0", tc.trigger, n)
			}
		})
	}
}

// brokerMM is a minimega running every VM it is asked about, with a
// screenshot of each unless shotErr is set.
type brokerMM struct {
	mm.MM

	shotErr error
}

func (m *brokerMM) GetVMInfo(...mm.Option) mm.VMs {
	return mm.VMs{{Name: "vm1", Running: true, State: "RUNNING"}}
}

func (m *brokerMM) GetVMScreenshot(...mm.Option) ([]byte, error) {
	if m.shotErr != nil {
		return nil, m.shotErr
	}

	return []byte("png"), nil
}

// brokerUseDelayedStart stores experiment exp with VM vm1, started when
// started is set, and installs fake as minimega with an empty web cache.
func brokerUseDelayedStart(t *testing.T, started bool, fake mm.MM) {
	t.Helper()

	storetest.Use(t)

	c, _ := store.NewConfig("experiment/exp")
	c.Spec = map[string]any{
		"experimentName": "exp",
		"topology":       map[string]any{"nodes": []map[string]any{{"general": map[string]any{"hostname": "vm1"}}}},
	}

	if started {
		c.Status = map[string]any{"startTime": "2024-01-01T00:00:00Z"}
	}

	if err := store.Create(c); err != nil {
		t.Fatalf("store.Create(experiment/exp) = %v", err)
	}

	originalMM, originalCache := mm.DefaultMM, cache.DefaultWebCache
	mm.DefaultMM, cache.DefaultWebCache = fake, cache.NewGoWebCache() //nolint:reassign // install test doubles

	t.Cleanup(func() {
		mm.DefaultMM, cache.DefaultWebCache = originalMM, originalCache //nolint:reassign // restore test doubles
	})
}

// brokerDescribe names the messages queued for broadcast, with the VM or
// screenshot each carries, and checks their policy.
func brokerDescribe(t *testing.T) []string {
	t.Helper()

	var got []string

	for len(broadcast) > 0 {
		out := <-broadcast

		if diff := cmp.Diff(bt.NewRequestPolicy("vms/start", "update", "exp/vm1"), out.pub.RequestPolicy); diff != "" {
			t.Errorf("%s message policy mismatch (-want +got):\n%s", out.pub.Resource.Type, diff)
		}

		desc := out.pub.Resource.Type + " " + out.pub.Resource.Name + " " + out.pub.Resource.Action

		switch out.pub.Resource.Type {
		case "experiment/vm":
			var v proto.VM
			if err := protojson.Unmarshal(out.pub.Result, &v); err != nil {
				t.Fatalf("decoding the start message %s: %v", out.pub.Result, err)
			}

			desc += fmt.Sprintf(" %s running=%t", v.GetName(), v.GetRunning())
		case "experiment/vm/screenshot":
			var shot map[string]string
			if err := json.Unmarshal(out.pub.Result, &shot); err != nil {
				t.Fatalf("decoding the screenshot message %s: %v", out.pub.Result, err)
			}

			desc += " " + shot["screenshot"]
		}

		got = append(got, desc)
	}

	return got
}

// A VM held back by delayed start is announced once it runs, then its
// screenshot when minimega has one; nothing is sent for a VM not running.
//
//nolint:paralleltest // replaces the store, minimega and the web cache
func TestPublishDelayedStart(t *testing.T) {
	const (
		start = "experiment/vm exp/vm1 start vm1 running=true"
		shot  = "experiment/vm/screenshot exp/vm1 update data:image/png;base64,cG5n"
	)

	tests := map[string]struct {
		noExperiment bool
		started      bool
		vm           string
		shotErr      error
		want         []string
	}{
		"a running VM":                       {started: true, vm: "vm1", want: []string{start, shot}},
		"a running VM without a screenshot":  {started: true, vm: "vm1", shotErr: errors.New("no screen"), want: []string{start}},
		"a VM of an experiment not started":  {vm: "vm1"},
		"a VM not in the experiment":         {started: true, vm: "vm9"},
		"a VM of an experiment that is gone": {noExperiment: true, vm: "vm1"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(func() {
				for len(broadcast) > 0 {
					<-broadcast
				}
			})

			if tc.noExperiment {
				storetest.Use(t)
			} else {
				brokerUseDelayedStart(t, tc.started, &brokerMM{shotErr: tc.shotErr})
			}

			publishDelayedStart("exp/"+tc.vm, "exp", tc.vm)

			if diff := cmp.Diff(tc.want, brokerDescribe(t)); diff != "" {
				t.Errorf("publishDelayedStart(exp/%s) messages mismatch (-want +got):\n%s", tc.vm, diff)
			}
		})
	}
}

// The broker loop announces a delayed start it is told of, and logs an
// error for a delayed-start name without an experiment.
//
//nolint:paralleltest // runs the broker loop and replaces the store and minimega
func TestRunPublishesDelayedStarts(t *testing.T) {
	brokerUseDelayedStart(t, true, &brokerMM{})

	logged := brokerCaptureLogs(t, "error", "unexpected delayed-start publication")

	c := brokerAdmin()
	withClients(t, c)

	delayed := make(chan any)
	brokerRun(t, nil, delayed)

	delayed <- "vm1"
	delayed <- "exp/vm1"

	got := make([]string, 0, 2)

	for range 2 {
		out := brokerNext(t, c)
		got = append(got, out.pub.Resource.Type+" "+out.pub.Resource.Name)
	}

	want := []string{"experiment/vm exp/vm1", "experiment/vm/screenshot exp/vm1"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("messages after delayed starts vm1 and exp/vm1 mismatch (-want +got):\n%s", diff)
	}

	polltest.Until(t, "the unexpected delayed-start error", func() bool { return len(logged()) > 0 })

	wantLog := []string{"ERROR unexpected delayed-start publication name=vm1"}
	if diff := cmp.Diff(wantLog, logged()); diff != "" {
		t.Errorf("logged after delayed start vm1 mismatch (-want +got):\n%s", diff)
	}
}

// brokerCaptureLogs records the log lines at level or above that hold part,
// until the test ends.
func brokerCaptureLogs(t *testing.T, level, part string) func() []string {
	t.Helper()

	var (
		mu    sync.Mutex
		lines []string
	)

	name := "broker-test-" + t.Name()

	plog.AddHandler(name, plog.NewUIHandler(level, func(_ time.Time, level, _, msg string) {
		if !strings.Contains(msg, part) {
			return
		}

		mu.Lock()
		defer mu.Unlock()

		lines = append(lines, level+" "+msg)
	}))
	t.Cleanup(func() { plog.RemoveHandler(name) })

	return func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), lines...)
	}
}

// The default drop reporter warns of the clients and log lines dropped.
//
//nolint:paralleltest // adds a log handler
func TestDropReporterWarns(t *testing.T) {
	logged := brokerCaptureLogs(t, "warn", "WebSocket broker dropped")

	dropReporter(2, 5)

	want := []string{"WARN WebSocket broker dropped slow clients or live log lines clients=2 log_lines=5"}
	if diff := cmp.Diff(want, logged()); diff != "" {
		t.Errorf("dropReporter(2, 5) logged mismatch (-want +got):\n%s", diff)
	}
}

// Start runs the broker on the app trigger publications until ctx ends, then
// stops listening for them.
//
//nolint:paralleltest // runs the broker loop on the shared app triggers
func TestStartListensForAppTriggers(t *testing.T) {
	c := brokerAdmin()
	withClients(t, c)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	trigger := app.TriggerPublication{Experiment: "exp", App: "scorch", State: "success"}

	// a publication before Start subscribes reaches no one
	polltest.Until(t, "a trigger reaching the client", func() bool {
		pubsub.Publish("trigger-app", trigger)

		return len(c.publish) > 0
	})

	if out := brokerNext(t, c); out.pub.Resource.Type != "apps/scorch" {
		t.Errorf("Start() sent %s for an app trigger, want apps/scorch", out.pub.Resource.Type)
	}

	cancel()
	polltest.Within(t, "Start returning", func() { <-stopped })

	// with no subscriber left, a publication does not wait
	polltest.Within(t, "publishing after Start returned", func() {
		pubsub.Publish("trigger-app", trigger)
		pubsub.Publish("delayed-start", "exp/vm1")
	})
}
