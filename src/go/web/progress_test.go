package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	gwebsocket "github.com/gorilla/websocket"

	"phenix/api/experiment"
	ifaces "phenix/types/interfaces"
	"phenix/util/polltest"
	"phenix/web/broker"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/middleware"
	"phenix/web/proto"
)

// useLiveProgress records p as the progress of an operation of this process on experiment name until the test ends.
func useLiveProgress(t *testing.T, name string, p ifaces.OperationProgress) {
	t.Helper()

	owner := new(progressSink)

	liveProgress.claim(name, owner)
	liveProgress.set(name, owner, p)

	t.Cleanup(func() { liveProgress.release(name, owner) })
}

// sentMessage is a websocket message a progressSink sent: its action, and its body decoded.
type sentMessage struct {
	Policy string
	Action string
	Body   map[string]any
}

// sinkRecorder records the messages of a progressSink.
type sinkRecorder struct {
	mu   sync.Mutex
	sent []sentMessage
}

func (r *sinkRecorder) send(policy *bt.RequestPolicy, resource *bt.Resource, body json.RawMessage) {
	var decoded map[string]any
	_ = json.Unmarshal(body, &decoded)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.sent = append(r.sent, sentMessage{Policy: policy.Resource, Action: resource.Action, Body: decoded})
}

func (r *sinkRecorder) messages() []sentMessage {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sentMessage(nil), r.sent...)
}

// progressBody is p as a progress message body decodes.
func progressBody(p ifaces.OperationProgress) map[string]any {
	return map[string]any{
		"operation": p.Operation, "percent": p.Percent, "phase": p.Phase,
		"done": float64(p.Done), "total": float64(p.Total), "step": p.Step,
	}
}

// A sink sends each progress at once, but a holding sink keeps the latest post-start progress and a background
// error until release. Once it ends, by the phase done or by an error, it sends nothing more and the board forgets
// the experiment. The error body names the operation, the phase and step it was in, and the error.
func TestProgressSink(t *testing.T) { //nolint:paralleltest // shares liveProgress
	var (
		launch   = ifaces.OperationProgress{Operation: "start", Percent: 0.3, Phase: "launch-vms", Done: 1, Total: 4, Step: "launch"}
		post     = ifaces.OperationProgress{Operation: "start", Percent: 0.9, Phase: "post-start-apps", Done: 0, Total: 1, Step: "soh"}
		done     = ifaces.OperationProgress{Operation: "start", Percent: 1, Phase: "done"}
		cleanup  = ifaces.OperationProgress{Operation: "stop", Percent: 0, Phase: "cleanup-apps", Done: 0, Total: 2, Step: "ntp"}
		failure  = errors.New("boom")
		startPol = "experiments/start"
		stopPol  = "experiments/stop"
	)

	tests := []struct {
		name      string
		operation string
		hold      bool
		run       func(s *progressSink)
		want      []sentMessage
		wantBoard *ifaces.OperationProgress
	}{
		{
			name: "sends each progress at once", operation: "start",
			run:       func(s *progressSink) { s.report(launch); s.report(post) },
			want:      []sentMessage{{startPol, "progress", progressBody(launch)}, {startPol, "progress", progressBody(post)}},
			wantBoard: &post,
		},
		{
			name: "holds the post-start progress until release", operation: "start", hold: true,
			run: func(s *progressSink) {
				s.report(launch)
				s.report(post)
				s.report(done)
				s.release()
			},
			want: []sentMessage{{startPol, "progress", progressBody(launch)}, {startPol, "progress", progressBody(done)}},
		},
		{
			name: "holds a background error until release", operation: "start", hold: true,
			run: func(s *progressSink) {
				s.report(post)
				s.fail(failure)
				s.report(done)
				s.release()
			},
			want: []sentMessage{
				{startPol, "progress", progressBody(post)},
				{startPol, "errorStarting", map[string]any{
					"operation": "start", "phase": "post-start-apps", "step": "soh", "error": "boom",
				}},
			},
		},
		{
			name: "sends a stop error at once", operation: "stop",
			run: func(s *progressSink) { s.report(cleanup); s.fail(failure); s.fail(failure) },
			want: []sentMessage{
				{stopPol, "progress", progressBody(cleanup)},
				{stopPol, "errorStopping", map[string]any{
					"operation": "stop", "phase": "cleanup-apps", "step": "ntp", "error": "boom",
				}},
			},
		},
		{
			name: "names no phase for an error before the first", operation: "stop",
			run: func(s *progressSink) { s.fail(failure) },
			want: []sentMessage{{stopPol, "errorStopping", map[string]any{
				"operation": "stop", "phase": "", "step": "", "error": "boom",
			}}},
		},
		{
			name: "sends nothing once another operation claims the experiment", operation: "start",
			run: func(s *progressSink) {
				s.report(post)
				newProgressSink("exp", "stop", false, func(*bt.RequestPolicy, *bt.Resource, json.RawMessage) {})
				s.report(done)
			},
			want: []sentMessage{{startPol, "progress", progressBody(post)}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { liveProgress.entries = make(map[string]progressEntry) })

			rec := new(sinkRecorder)
			s := newProgressSink("exp", tc.operation, tc.hold, rec.send)

			tc.run(s)

			if diff := cmp.Diff(tc.want, rec.messages()); diff != "" {
				t.Errorf("sink messages mismatch (-want +got):\n%s", diff)
			}

			got, ok := liveProgress.get("exp")

			var gotBoard *ifaces.OperationProgress
			if ok {
				gotBoard = &got
			}

			if diff := cmp.Diff(tc.wantBoard, gotBoard); diff != "" {
				t.Errorf("board progress mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// An experiment that another process starts or stops reports the status and progress its live marker records; a
// stale marker or one without progress gives no progress.
func TestExperimentProgressElsewhere(t *testing.T) { //nolint:paralleltest // replaces the store and minimega
	now := time.Now().UTC().Format(time.RFC3339)

	marker := func(phase string, percent float64) map[string]any {
		m := map[string]any{"since": now, "pid": 7, "host": "elsewhere"}
		if phase != "" {
			m["progress"] = map[string]any{
				"operation": "start", "percent": percent, "phase": phase, "done": 1, "total": 2, "step": "ntp",
			}
		}

		return m
	}

	var (
		starting = namedExperiment(t, "starting", "")
		stopping = namedExperiment(t, "stopping", "2024-01-01T00:00:00Z")
		plain    = namedExperiment(t, "plain", "")
		stale    = namedExperiment(t, "stale", "2024-01-01T00:00:00Z")
	)

	starting.Status = map[string]any{"starting": marker("pre-start-apps", 0.075)}
	stopping.Status["stopping"] = marker("cleanup-apps", 0.2)
	plain.Status = map[string]any{"starting": marker("", 0)}

	old := marker("cleanup-apps", 0.2)
	old["since"] = time.Now().UTC().Add(-experiment.StartMarkerMaxAge - time.Minute).Format(time.RFC3339)
	stale.Status["stopping"] = old

	useTestStore(t, starting, stopping, plain, stale)
	useTestMM(t, new(testMM))

	server := serveAs(t, "alice", testRole("experiments", "list", "*")).URL

	var list proto.ExperimentList
	if code := getAPI(t, server+"/api/v1/experiments?vms=false", &list); code != http.StatusOK {
		t.Fatalf("GET /experiments = %d, want %d", code, http.StatusOK)
	}

	type state struct {
		Status   string
		Percent  float64
		Progress *ifaces.OperationProgress
	}

	got := make(map[string]state)

	for _, exp := range list.GetExperiments() {
		var p *ifaces.OperationProgress
		if pb := exp.GetProgress(); pb != nil {
			p = &ifaces.OperationProgress{
				Operation: pb.GetOperation(), Percent: pb.GetPercent(), Phase: pb.GetPhase(),
				Done: int(pb.GetDone()), Total: int(pb.GetTotal()), Step: pb.GetStep(),
			}
		}

		got[exp.GetName()] = state{Status: exp.GetStatus(), Percent: exp.GetPercent(), Progress: p}
	}

	progress := func(phase string, percent float64) *ifaces.OperationProgress {
		return &ifaces.OperationProgress{Operation: "start", Percent: percent, Phase: phase, Done: 1, Total: 2, Step: "ntp"}
	}

	want := map[string]state{
		"starting": {Status: "starting", Percent: 0.075, Progress: progress("pre-start-apps", 0.075)},
		"stopping": {Status: "stopping", Percent: 0.2, Progress: progress("cleanup-apps", 0.2)},
		"plain":    {Status: "starting"},
		"stale":    {Status: "started"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GET /experiments progress mismatch (-want +got):\n%s", diff)
	}
}

// experimentListener records the experiment messages of one experiment that an admin client of the broker gets.
type experimentListener struct {
	mu     sync.Mutex
	got    []sentMessage
	probes int
}

// listenExperiment runs the broker for the rest of the test with one admin client, which records the experiment
// messages of exp.
func listenExperiment(t *testing.T, exp string) *experimentListener {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)

		broker.Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-stopped
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broker.ServeWS(w, r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyRole, adminRole())))
	}))
	t.Cleanup(server.Close)

	conn, resp, err := gwebsocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dialing the broker: %v", err)
	}

	resp.Body.Close()
	t.Cleanup(func() { conn.Close() })

	l := new(experimentListener)

	go func() {
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}

			// the broker may send several messages in one frame
			for dec := json.NewDecoder(bytes.NewReader(b)); ; {
				var m struct {
					Resource struct {
						Type   string `json:"type"`
						Name   string `json:"name"`
						Action string `json:"action"`
					} `json:"resource"`
					Result map[string]any `json:"result"`
				}

				if dec.Decode(&m) != nil {
					break
				}

				l.mu.Lock()

				switch {
				case m.Resource.Type == "experiment/probe":
					l.probes++
				case m.Resource.Type == "experiment" && m.Resource.Name == exp:
					l.got = append(l.got, sentMessage{Policy: "", Action: m.Resource.Action, Body: m.Result})
				}

				l.mu.Unlock()
			}
		}
	}()

	l.flush(t)

	return l
}

// flush waits until the client gets a probe sent after every message before it, and returns those messages.
func (l *experimentListener) flush(t *testing.T) []sentMessage {
	t.Helper()

	l.mu.Lock()
	want := l.probes + 1
	l.mu.Unlock()

	polltest.Until(t, "the broker client getting a probe", func() bool {
		broker.Broadcast(nil, bt.NewResource("experiment/probe", "x", "probe"), nil)
		time.Sleep(5 * time.Millisecond)

		l.mu.Lock()
		defer l.mu.Unlock()

		return l.probes >= want
	})

	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.got)
}

// actions lists the actions of msgs, and the phase of each progress message.
func actions(msgs []sentMessage) []string {
	out := make([]string, 0, len(msgs))

	for _, m := range msgs {
		if m.Action == "progress" {
			out = append(out, fmt.Sprintf("progress %v %v", m.Body["operation"], m.Body["phase"]))

			continue
		}

		out = append(out, m.Action)
	}

	return slices.Compact(out)
}

// A start sends starting, its progress, start, the progress of its post-start apps and done, with a percent that
// never goes down; a stop then sends stopping, its progress and stop.
//
//nolint:paralleltest // replaces the store and minimega, and runs the broker
func TestStartAndStopMessages(t *testing.T) {
	useTestStore(t, namedExperiment(t, "up", ""))
	useJobs(t)
	useStartMM(t)
	experimentStopClearCancelers(t, "up")

	server := serveAs(t, "alice", adminRole()).URL

	t.Run("start and stop", func(t *testing.T) {
		listener := listenExperiment(t, "up")

		if resp := do(t, http.MethodPost, server+"/api/v1/experiments/up/start", ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("POST start = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
		}

		polltest.Until(t, "the start's done progress", func() bool {
			msgs := listener.flush(t)

			return len(msgs) > 0 && msgs[len(msgs)-1].Body["phase"] == "done"
		})

		if resp := do(t, http.MethodPost, server+"/api/v1/experiments/up/stop", ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("POST stop = %d, want %d: %s", resp.StatusCode, http.StatusOK, readBody(t, resp))
		}

		msgs := listener.flush(t)

		polltest.Until(t, "the background part of the start", func() bool { return finishStartGoroutines() == 0 })

		want := []string{
			"starting", "progress start pre-start-apps", "progress start prepare", "progress start launch-vms",
			"progress start start-vms", "start", "progress start post-start-apps", "progress start done",
			"stopping", "progress stop cleanup-apps", "progress stop stop-vms", "progress stop cleanup", "stop",
		}

		if diff := cmp.Diff(want, actions(msgs)); diff != "" {
			t.Errorf("start and stop messages mismatch (-want +got):\n%s", diff)
		}

		last := map[any]float64{}

		for _, m := range msgs {
			if m.Action != "progress" {
				continue
			}

			percent, _ := m.Body["percent"].(float64)
			if op := m.Body["operation"]; percent < last[op] || percent > 1 {
				t.Errorf("%v progress %v at %v after %v, want it in [%v, 1]", op, m.Body["phase"], percent, last[op], last[op])
			}

			last[m.Body["operation"]] = percent
		}

		if _, ok := liveProgress.get("up"); ok {
			t.Error("liveProgress holds up after its stop, want nothing")
		}
	})
}

// finishStartGoroutines counts the goroutines that run the background part of an experiment start.
func finishStartGoroutines() int {
	buf := make([]byte, 1<<20)

	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "phenix/api/experiment.finishStart(")
		}

		buf = make([]byte, 2*len(buf))
	}
}

// followStart gives the sink the first background error of a start, and no error of a start that a stop canceled.
func TestFollowStartReportsTheFirstError(t *testing.T) { //nolint:paralleltest // shares liveProgress
	t.Cleanup(func() { liveProgress.entries = make(map[string]progressEntry) })

	post := ifaces.OperationProgress{Operation: "start", Percent: 0.9, Phase: "post-start-apps", Done: 0, Total: 1, Step: "soh"}

	for _, canceled := range []bool{false, true} {
		rec := new(sinkRecorder)
		sink := newProgressSink("exp", "start", false, rec.send)
		sink.report(post)

		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}

		ch := make(chan error)
		followStart(ctx, "exp", ch, sink)

		// each send returns once followStart took the error before it
		ch <- errors.New("boom")
		ch <- errors.New("stopping experiment: gone")
		ch <- errors.New("one more")
		close(ch)
		cancel()

		want := []sentMessage{{"experiments/start", "progress", progressBody(post)}}
		if !canceled {
			want = append(want, sentMessage{"experiments/start", "errorStarting", map[string]any{
				"operation": "start", "phase": "post-start-apps", "step": "soh", "error": "boom",
			}})
		}

		if diff := cmp.Diff(want, rec.messages()); diff != "" {
			t.Errorf("followStart(canceled %t) messages mismatch (-want +got):\n%s", canceled, diff)
		}
	}
}
