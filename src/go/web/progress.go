package web

import (
	"encoding/json"
	"sync"

	"phenix/api/experiment"
	ifaces "phenix/types/interfaces"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/proto"
)

// progressBoard holds the latest progress of each start and stop that this process runs, for the experiment lists.
// Each entry belongs to the operation that claimed it last, so the progress of a start that a stop interrupts cannot
// replace the progress of the stop.
type progressBoard struct {
	mu      sync.Mutex
	entries map[string]progressEntry
}

// progressEntry is the operation that owns the board entry of an experiment, and its latest progress, if any.
type progressEntry struct {
	owner    *progressSink
	progress *ifaces.OperationProgress
}

//nolint:gochecknoglobals // shared with the list handlers
var liveProgress = &progressBoard{mu: sync.Mutex{}, entries: make(map[string]progressEntry)}

// claim gives the entry of experiment name to owner, with no progress yet.
func (b *progressBoard) claim(name string, owner *progressSink) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.entries[name] = progressEntry{owner: owner, progress: nil}
}

// set records p as the progress of experiment name, if owner owns its entry, and reports whether it did.
func (b *progressBoard) set(name string, owner *progressSink, p ifaces.OperationProgress) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.entries[name].owner != owner {
		return false
	}

	b.entries[name] = progressEntry{owner: owner, progress: &p}

	return true
}

// release removes the entry of experiment name, if owner owns it.
func (b *progressBoard) release(name string, owner *progressSink) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.entries[name].owner == owner {
		delete(b.entries, name)
	}
}

// get returns the latest progress of experiment name, if an operation of this process reported one.
func (b *progressBoard) get(name string) (ifaces.OperationProgress, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[name]
	if !ok || entry.progress == nil {
		return ifaces.OperationProgress{}, false
	}

	return *entry.progress, true
}

// operationError is the body of an errorStarting or errorStopping message: the operation, the phase and step it
// was in, and its error.
type operationError struct {
	Operation string `json:"operation"`
	Phase     string `json:"phase"`
	Step      string `json:"step"`
	Error     string `json:"error"`
}

// broadcaster sends a websocket message, as broker.Broadcast does.
type broadcaster func(*bt.RequestPolicy, *bt.Resource, json.RawMessage)

// progressSink sends the progress of one start or stop of an experiment to the websocket clients, as progress
// messages, and to liveProgress. A sink that holds sends the post-start progress and a background error only when
// release is called, after the start message.
type progressSink struct {
	name      string
	operation string
	policy    *bt.RequestPolicy
	errAction string
	send      broadcaster

	mu      sync.Mutex
	last    ifaces.OperationProgress
	held    bool
	pending *ifaces.OperationProgress
	failed  []byte
	ended   bool
}

// newProgressSink is the sink of operation (experiment.OperationStart or OperationStop) of experiment name, which
// claims its entry of liveProgress and sends its messages with send. With hold, it holds post-start progress until
// release.
func newProgressSink(name, operation string, hold bool, send broadcaster) *progressSink {
	s := &progressSink{
		name:      name,
		operation: operation,
		policy:    bt.NewRequestPolicy("experiments/start", "update", name),
		errAction: "errorStarting",
		send:      send,
		mu:        sync.Mutex{},
		last:      ifaces.OperationProgress{Operation: operation, Percent: 0, Phase: "", Done: 0, Total: 0, Step: ""},
		held:      hold,
		pending:   nil,
		failed:    nil,
		ended:     false,
	}

	if operation == experiment.OperationStop {
		s.policy = bt.NewRequestPolicy("experiments/stop", "update", name)
		s.errAction = "errorStopping"
	}

	liveProgress.claim(name, s)

	return s
}

// report is the experiment.ProgressFunc of the sink. It drops p once the sink ended or another operation claimed
// the experiment. Progress with the phase done ends the sink.
func (s *progressSink) report(p ifaces.OperationProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended || !liveProgress.set(s.name, s, p) {
		return
	}

	s.last = p

	if p.Phase == experiment.PhaseDone {
		s.ended = true

		liveProgress.release(s.name, s)
	}

	if s.held && (p.Phase == experiment.PhasePostStartApps || p.Phase == experiment.PhaseDone) {
		s.pending = &p

		return
	}

	s.broadcast("progress", p)
}

// fail sends the error message of the operation, which failed with err in its latest phase and step, and ends the
// sink. A sink that holds sends it on release.
func (s *progressSink) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return
	}

	s.ended = true

	liveProgress.release(s.name, s)

	body, _ := json.Marshal(operationError{
		Operation: s.operation, Phase: s.last.Phase, Step: s.last.Step, Error: err.Error(),
	})

	if s.held {
		s.failed = body

		return
	}

	s.send(s.policy, bt.NewResource("experiment", s.name, s.errAction), body)
}

// release stops holding: it sends the held progress and then the held error, if any.
func (s *progressSink) release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.held = false

	if s.pending != nil {
		s.broadcast("progress", *s.pending)
		s.pending = nil
	}

	if s.failed != nil {
		s.send(s.policy, bt.NewResource("experiment", s.name, s.errAction), s.failed)
		s.failed = nil
	}
}

// end ends the sink without a message, and removes its progress from liveProgress.
func (s *progressSink) end() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ended = true

	liveProgress.release(s.name, s)
}

// broadcast sends p as a message with action. The caller holds s.mu.
func (s *progressSink) broadcast(action string, p ifaces.OperationProgress) {
	body, _ := json.Marshal(p)

	s.send(s.policy, bt.NewResource("experiment", s.name, action), body)
}

// markerProgress is the progress that the live marker m records, or nil when it records none.
func markerProgress(m ifaces.StartMarker) *ifaces.OperationProgress {
	if m.Progress.Phase == "" || !experiment.StartMarkerLive(m) {
		return nil
	}

	p := m.Progress

	return &p
}

// progressProto is p as the experiment lists give it, or nil for none.
func progressProto(p *ifaces.OperationProgress) *proto.ExperimentProgress {
	if p == nil {
		return nil
	}

	return &proto.ExperimentProgress{
		Operation: p.Operation,
		Percent:   p.Percent,
		Phase:     p.Phase,
		Done:      uint32(max(p.Done, 0)),  //nolint:gosec // G115 step counts fit in a uint32
		Total:     uint32(max(p.Total, 0)), //nolint:gosec // G115 step counts fit in a uint32
		Step:      p.Step,
	}
}
