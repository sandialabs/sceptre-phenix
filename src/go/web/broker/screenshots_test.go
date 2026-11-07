package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/polltest"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/rbac"
)

// fakeShots answers screenshot requests with the current picture of each VM,
// counting the requests for each screenshot.
type fakeShots struct {
	mu       sync.Mutex
	pictures map[string]string // by exp/vm
	asked    map[shotKey]int
}

func useFakeShots(t *testing.T, pictures map[string]string) *fakeShots {
	t.Helper()

	withDeps(t)

	frames.mu.Lock()
	saved := frames.frames
	frames.frames = make(map[shotKey]frame)
	frames.mu.Unlock()

	t.Cleanup(func() {
		frames.mu.Lock()
		frames.frames = saved
		frames.mu.Unlock()
	})

	fake := &fakeShots{pictures: pictures, asked: make(map[shotKey]int)}

	deps.screenshot = func(_ context.Context, exp, vm, width string) ([]byte, error) {
		fake.mu.Lock()
		defer fake.mu.Unlock()

		fake.asked[shotKey{exp: exp, vm: vm, width: width}]++

		return []byte(fake.pictures[exp+"/"+vm]), nil
	}

	return fake
}

func (f *fakeShots) paint(vm, picture string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pictures[vm] = picture
}

func (f *fakeShots) requests() map[shotKey]int {
	f.mu.Lock()
	defer f.mu.Unlock()

	asked := f.asked
	f.asked = make(map[shotKey]int)

	return asked
}

// viewer is a client showing the given running VMs of exp at width.
func viewer(width string, vms ...string) *Client {
	c := newFakeClient(nil)
	c.shotSize = width

	for _, vm := range vms {
		c.vms = append(c.vms, vmScope{exp: "exp", name: vm, running: true})
	}

	return c
}

// shotsOf returns, by VM, the pictures in the screenshot messages c was sent.
func shotsOf(t *testing.T, c *Client) map[string]string {
	t.Helper()

	got := make(map[string]string)

	for _, out := range received(c) {
		var result struct {
			Screenshot string `json:"screenshot"`
		}

		if err := json.Unmarshal(out.pub.Result, &result); err != nil {
			t.Fatalf("decoding screenshot message %s: %v", out.data, err)
		}

		got[out.pub.Resource.Name] = result.Screenshot
	}

	return got
}

// Each screenshot in view is taken and encoded once, and the same message
// goes to every client showing that VM at that width.
//
//nolint:paralleltest // replaces the client's dependencies and the frame cache
func TestScreenshotsAreEncodedOnce(t *testing.T) {
	fake := useFakeShots(t, map[string]string{"exp/vm1": "one", "exp/vm2": "two"})

	var (
		first  = viewer("200", "vm1", "vm2")
		second = viewer("200", "vm1")
		wider  = viewer("400", "vm1")
	)

	// stopped VMs are not screenshotted
	wider.vms = append(wider.vms, vmScope{exp: "exp", name: "off", running: false})

	pushFrames(context.Background(), wantedShots([]*Client{first, second, wider}))

	want := map[shotKey]int{
		{exp: "exp", vm: "vm1", width: "200"}: 1,
		{exp: "exp", vm: "vm2", width: "200"}: 1,
		{exp: "exp", vm: "vm1", width: "400"}: 1,
	}
	if diff := cmp.Diff(want, fake.requests()); diff != "" {
		t.Errorf("screenshots asked for mismatch (-want +got):\n%s", diff)
	}

	firstGot, secondGot := received(first), received(second)

	if len(firstGot) != 2 || len(secondGot) != 1 {
		t.Fatalf("messages sent = %d and %d, want 2 and 1", len(firstGot), len(secondGot))
	}

	// vm1 sorts first, so it is the first client's first message
	if &firstGot[0].data[0] != &secondGot[0].data[0] {
		t.Error("clients showing vm1 at one width were sent separately encoded messages, want one shared message")
	}

	if diff := cmp.Diff(map[string]string{"exp/vm1": "data:image/png;base64,b25l"}, shotsOf(t, wider)); diff != "" {
		t.Errorf("screenshots sent at 400 mismatch (-want +got):\n%s", diff)
	}
}

// A client is sent a VM's screenshot again only once its pixels change, or
// once the client lists its VMs or is told of a change to the VM.
//
//nolint:paralleltest // replaces the client's dependencies and the frame cache
func TestUnchangedScreenshotsAreNotResent(t *testing.T) {
	fake := useFakeShots(t, map[string]string{"exp/vm1": "one", "exp/vm2": "two"})

	c := viewer("200", "vm1", "vm2")

	steps := []struct {
		name   string
		change func()
		want   map[string]string
	}{
		{
			name: "sends every screenshot at first",
			want: map[string]string{"exp/vm1": "data:image/png;base64,b25l", "exp/vm2": "data:image/png;base64,dHdv"},
		},
		{
			name: "sends nothing while the pictures stay the same",
			want: map[string]string{},
		},
		{
			name:   "sends the screenshot whose picture changed",
			change: func() { fake.paint("exp/vm2", "new") },
			want:   map[string]string{"exp/vm2": "data:image/png;base64,bmV3"},
		},
		{
			name: "sends the screenshot of a VM the page was told of",
			change: func() {
				out, _ := encode(
					bt.Publish{
						Resource: bt.NewResource("experiment/vm", "exp/vm1", "update"),
						Result:   json.RawMessage(`{"running": true}`),
					},
				)
				c.trackVMState(out)
			},
			want: map[string]string{"exp/vm1": "data:image/png;base64,b25l"},
		},
		{
			name: "sends every screenshot again after the VMs are listed again",
			change: func() {
				c.clearVMs()
				setVMs(c, vmScope{exp: "exp", name: "vm1", running: true}, vmScope{exp: "exp", name: "vm2", running: true})
			},
			want: map[string]string{"exp/vm1": "data:image/png;base64,b25l", "exp/vm2": "data:image/png;base64,bmV3"},
		},
	}

	for _, step := range steps {
		if step.change != nil {
			step.change()
		}

		c.pushScreenshots()

		if diff := cmp.Diff(step.want, shotsOf(t, c)); diff != "" {
			t.Errorf("%s: screenshots sent mismatch (-want +got):\n%s", step.name, diff)
		}
	}
}

// The broker's round sends every client its screenshots, keeps only the
// frames still in view, and is skipped while the last one runs.
//
//nolint:paralleltest // replaces the clients, their dependencies and the frame cache
func TestScreenshotRound(t *testing.T) {
	fake := useFakeShots(t, map[string]string{"exp/vm1": "one", "exp/gone": "old"})

	// hold keeps the first round in its screenshot request until it closes
	entered, hold := make(chan struct{}, 2), make(chan struct{})
	take := deps.screenshot
	deps.screenshot = func(ctx context.Context, exp, vm, width string) ([]byte, error) {
		entered <- struct{}{}
		<-hold

		return take(ctx, exp, vm, width)
	}

	frames.frames[shotKey{exp: "exp", vm: "gone", width: "200"}] = frame{}

	var (
		showing = viewer("200", "vm1")
		left    = viewer("200", "vm1")
	)

	close(left.done)

	withClients(t, showing, left)

	startScreenshotRound(context.Background())
	polltest.Until(t, "the round asking for a screenshot", func() bool { return len(entered) > 0 })

	startScreenshotRound(context.Background())
	close(hold)

	polltest.Until(t, "the round ending", func() bool { return !shotRound.Load() })

	if diff := cmp.Diff(map[shotKey]int{{exp: "exp", vm: "vm1", width: "200"}: 1}, fake.requests()); diff != "" {
		t.Errorf("screenshot requests of a round and one started during it mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]string{"exp/vm1": "data:image/png;base64,b25l"}, shotsOf(t, showing)); diff != "" {
		t.Errorf("screenshots sent mismatch (-want +got):\n%s", diff)
	}

	if got := received(left); len(got) != 0 {
		t.Errorf("a client that left was sent %d messages, want none", len(got))
	}

	frames.mu.Lock()
	_, kept := frames.frames[shotKey{exp: "exp", vm: "gone", width: "200"}]
	frames.mu.Unlock()

	if kept {
		t.Error("the frame of a screenshot no client shows was kept, want it dropped")
	}
}

// gapSpec is an experiment spec without a topology that calls gap the second
// time its topology is read, which listVMs does once the VMs are in view.
type gapSpec struct {
	ifaces.ExperimentSpec

	reads int
	gap   func()
}

//nolint:ireturn // implements ExperimentSpec
func (s *gapSpec) Topology() ifaces.TopologySpec {
	if s.reads++; s.reads == 2 {
		s.gap()
	}

	return nil
}

// A screenshot sent while a VM list is being answered reaches the page before
// the list, which replaces it, so the next round sends it again.
//
//nolint:paralleltest // replaces the client's dependencies and the frame cache
func TestScreenshotSentBeforeListIsResent(t *testing.T) {
	useFakeShots(t, map[string]string{"exp/vm1": "one"})

	c := newFakeClient(nil)
	c.role = rbac.Role{Spec: &v1.RoleSpec{Policies: []*v1.PolicySpec{
		{Resources: []string{"vms"}, ResourceNames: []string{"exp/*"}, Verbs: []string{"list"}},
	}}}
	spec := &gapSpec{gap: func() { c.pushScreenshots() }}

	deps.experiment = func(string) (*types.Experiment, error) {
		return &types.Experiment{Spec: spec}, nil
	}
	deps.vms = func(context.Context, *types.Experiment) ([]mm.VM, freshness.Observation) {
		return []mm.VM{{Name: "vm1", Running: true}}, freshness.Observation{}
	}

	// held to the end, so the list's own push (and a late one after the test) never runs
	c.shotMu.Lock()

	c.listVMs(listJob{req: bt.Request{
		Resource: bt.NewResource("experiment/vms", "exp", "list"),
		Payload:  json.RawMessage(`{}`),
	}, view: c.view})

	order := func() []string {
		var got []string

		for len(c.publish) > 0 {
			switch msg := (<-c.publish).(type) {
			case bt.Publish:
				got = append(got, "list")
			case encodedPublish:
				got = append(got, "screenshot "+msg.pub.Resource.Name)
			}
		}

		return got
	}

	if diff := cmp.Diff([]string{"screenshot exp/vm1", "list"}, order()); diff != "" {
		t.Fatalf("messages sent while listing mismatch (-want +got):\n%s", diff)
	}

	c.pushScreenshots()

	if diff := cmp.Diff([]string{"screenshot exp/vm1"}, order()); diff != "" {
		t.Errorf("messages sent after the list mismatch (-want +got):\n%s", diff)
	}
}

// pushFrames stops once ctx ends, takes no screenshot for clients that left,
// and skips a screenshot it cannot take, logging only unexpected errors.
//
//nolint:paralleltest // replaces the client's dependencies and the frame cache
func TestPushFramesSkips(t *testing.T) {
	boom := errors.New("boom")

	tests := map[string]struct {
		cancelFirst bool
		leave       bool
		// the error the screenshot of vm1 fails with; cancel ends ctx first
		vm1Err    error
		cancel    bool
		wantAsked []string
		wantSent  map[string]string
		wantLogs  []string
	}{
		"every screenshot": {
			wantAsked: []string{"vm1", "vm2"},
			wantSent:  map[string]string{"exp/vm1": "data:image/png;base64,b25l", "exp/vm2": "data:image/png;base64,dHdv"},
		},
		"ctx ended before":  {cancelFirst: true, wantSent: map[string]string{}},
		"every viewer left": {leave: true, wantSent: map[string]string{}},
		"a VM minimega does not have": {
			vm1Err: mm.ErrVMNotFound, wantAsked: []string{"vm1", "vm2"},
			wantSent: map[string]string{"exp/vm2": "data:image/png;base64,dHdv"},
		},
		"a VM without a screenshot": {
			vm1Err: fmt.Errorf("wrapped: %w", mm.ErrScreenshotNotFound), wantAsked: []string{"vm1", "vm2"},
			wantSent: map[string]string{"exp/vm2": "data:image/png;base64,dHdv"},
		},
		"another error": {
			vm1Err: boom, wantAsked: []string{"vm1", "vm2"},
			wantSent: map[string]string{"exp/vm2": "data:image/png;base64,dHdv"},
			wantLogs: []string{"ERROR getting screenshot for WebSocket client err=boom"},
		},
		"ctx ended during a screenshot": {
			vm1Err: context.Canceled, cancel: true, wantAsked: []string{"vm1"}, wantSent: map[string]string{},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			useFakeShots(t, nil)

			logged := brokerCaptureLogs(t, "error", "getting screenshot")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			var (
				mu    sync.Mutex
				asked []string
			)

			deps.screenshot = func(_ context.Context, _, vm, _ string) ([]byte, error) {
				mu.Lock()
				asked = append(asked, vm)
				mu.Unlock()

				if vm == "vm1" && tc.vm1Err != nil {
					if tc.cancel {
						cancel()
					}

					return nil, tc.vm1Err
				}

				return []byte(map[string]string{"vm1": "one", "vm2": "two"}[vm]), nil
			}

			c := viewer("200", "vm1", "vm2")
			wanted := wantedShots([]*Client{c})

			if tc.leave {
				close(c.done)
			}

			if tc.cancelFirst {
				cancel()
			}

			pushFrames(ctx, wanted)

			if diff := cmp.Diff(tc.wantAsked, asked); diff != "" {
				t.Errorf("screenshots asked for mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantSent, shotsOf(t, c)); diff != "" {
				t.Errorf("screenshots sent mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(tc.wantLogs, logged()); diff != "" {
				t.Errorf("errors logged mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// sendFrame sends a frame only to a client showing the VM running at the
// frame's width, once per picture, and keeps it for the next round when the
// client's queue is full.
func TestSendFrame(t *testing.T) {
	t.Parallel()

	key := shotKey{exp: "exp", vm: "vm1", width: "200"}
	f := frame{hash: pngHash{1}, msg: encodedPublish{data: []byte("one")}}

	tests := map[string]struct {
		client   func() *Client
		wantSent bool
		// the hash the client keeps for vm1 after
		wantLast *pngHash
	}{
		"a new frame":                   {client: func() *Client { return viewer("200", "vm1") }, wantSent: true, wantLast: &f.hash},
		"a client at another width":     {client: func() *Client { return viewer("400", "vm1") }},
		"a VM out of the client's view": {client: func() *Client { return viewer("200", "vm2") }},
		"a VM stopped": {
			client: func() *Client {
				c := viewer("200")
				c.vms = []vmScope{{exp: "exp", name: "vm1", running: false}}

				return c
			},
		},
		"the frame last sent": {
			client: func() *Client {
				c := viewer("200", "vm1")
				c.sent = map[vmKey]pngHash{{exp: "exp", name: "vm1"}: f.hash}

				return c
			},
			wantLast: &f.hash,
		},
		"a changed frame": {
			client: func() *Client {
				c := viewer("200", "vm1")
				c.sent = map[vmKey]pngHash{{exp: "exp", name: "vm1"}: {2}}

				return c
			},
			wantSent: true, wantLast: &f.hash,
		},
		"a full queue": {
			client: func() *Client {
				c := viewer("200", "vm1")
				c.publish = make(chan any)

				return c
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := tc.client()
			c.sendFrame(key, f)

			if got := len(c.publish) == 1; got != tc.wantSent {
				t.Errorf("sendFrame() sent = %t, want %t", got, tc.wantSent)
			}

			var last *pngHash
			if h, ok := c.sent[vmKey{exp: "exp", name: "vm1"}]; ok {
				last = &h
			}

			if diff := cmp.Diff(tc.wantLast, last); diff != "" {
				t.Errorf("sendFrame() last frame kept mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A screenshot that cannot be taken gives its error and leaves the frame
// cache as it was.
//
//nolint:paralleltest // replaces the client's dependencies and the frame cache
func TestFrameCacheGetError(t *testing.T) {
	useFakeShots(t, nil)

	key := shotKey{exp: "exp", vm: "vm1", width: "200"}
	kept := frame{hash: pngHash{1}}
	frames.frames[key] = kept

	deps.screenshot = func(context.Context, string, string, string) ([]byte, error) { return nil, mm.ErrVMNotFound }

	got, err := frames.get(context.Background(), key)
	if !errors.Is(err, mm.ErrVMNotFound) {
		t.Errorf("frames.get() error = %v, want %v", err, mm.ErrVMNotFound)
	}

	if diff := cmp.Diff(frame{}, got, cmp.AllowUnexported(frame{}, encodedPublish{})); diff != "" {
		t.Errorf("frames.get() on error mismatch (-want +got):\n%s", diff)
	}

	if frames.frames[key].hash != kept.hash || len(frames.frames) != 1 {
		t.Errorf("frames.get() on error changed the cache to %v", frames.frames)
	}
}

// retain keeps only the frames of the screenshots wanted.
func TestFrameCacheRetain(t *testing.T) {
	t.Parallel()

	keep, drop := shotKey{exp: "exp", vm: "vm1", width: "200"}, shotKey{exp: "exp", vm: "vm1", width: "400"}

	tests := map[string]struct {
		wanted map[shotKey][]*Client
		want   []shotKey
	}{
		"some wanted": {wanted: map[shotKey][]*Client{keep: nil}, want: []shotKey{keep}},
		"none wanted": {wanted: map[shotKey][]*Client{}, want: nil},
		"nil wanted":  {wanted: nil, want: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fc := &frameCache{frames: map[shotKey]frame{keep: {}, drop: {}}}
			fc.retain(tc.wanted)

			got := slices.Collect(maps.Keys(fc.frames))
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(shotKey{})); diff != "" {
				t.Errorf("retain() kept mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A screenshot message names the VM, carries the PNG as a data URL and has no
// policy, as it goes only to the clients showing the VM.
func TestEncodeFrame(t *testing.T) {
	t.Parallel()

	out, err := encodeFrame(shotKey{exp: "exp", vm: "vm1", width: "200"}, []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatalf("encodeFrame() error = %v", err)
	}

	var got struct {
		Resource *bt.Resource      `json:"resource"`
		Result   map[string]string `json:"result"`
		Seq      uint64            `json:"seq"`
	}
	if err := json.Unmarshal(out.data, &got); err != nil {
		t.Fatalf("decoding encodeFrame() data %s: %v", out.data, err)
	}

	if diff := cmp.Diff(bt.NewResource("experiment/vm/screenshot", "exp/vm1", "update"), got.Resource); diff != "" {
		t.Errorf("encodeFrame() resource mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]string{"screenshot": "data:image/png;base64,iVBORw=="}, got.Result); diff != "" {
		t.Errorf("encodeFrame() result mismatch (-want +got):\n%s", diff)
	}

	if out.pub.RequestPolicy != nil || got.Seq == 0 {
		t.Errorf("encodeFrame() policy = %v, seq = %d; want none and a seq", out.pub.RequestPolicy, got.Seq)
	}
}
