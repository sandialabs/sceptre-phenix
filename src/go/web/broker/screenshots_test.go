package broker

import (
	"context"
	"encoding/json"
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
	useFakeShots(t, map[string]string{"exp/vm1": "one", "exp/gone": "old"})

	frames.frames[shotKey{exp: "exp", vm: "gone", width: "200"}] = frame{}

	var (
		showing = viewer("200", "vm1")
		left    = viewer("200", "vm1")
	)

	close(left.done)

	withClients(t, showing, left)

	shotRound.Store(true)
	startScreenshotRound(context.Background())

	if got := received(showing); len(got) != 0 {
		t.Errorf("a round started during another sent %d messages, want none", len(got))
	}

	shotRound.Store(false)
	startScreenshotRound(context.Background())

	polltest.Until(t, "the round ending", func() bool { return !shotRound.Load() })

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
