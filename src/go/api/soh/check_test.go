package soh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mitchellh/mapstructure"

	"phenix/app"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
	"phenix/util/mm"
)

func durationPtr(d time.Duration) *time.Duration { return &d }

// c2Settings is what sohMetadata.init makes of the C2 settings.
type c2Settings struct {
	AppearGrace, ClientGrace *time.Duration
	Concurrency              int
}

func TestSOHMetadataC2Settings(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		md      map[string]any
		want    c2Settings
		wantErr bool
	}{
		"defaults": {md: map[string]any{}, want: c2Settings{Concurrency: defaultC2Concurrency}},
		"set": {
			md:   map[string]any{"c2AppearGrace": "10s", "c2ClientGrace": "0s", "c2Concurrency": 4},
			want: c2Settings{AppearGrace: durationPtr(10 * time.Second), ClientGrace: durationPtr(0), Concurrency: 4},
		},
		"concurrency from stored JSON": {
			md: map[string]any{"c2Concurrency": float64(2)}, want: c2Settings{Concurrency: 2},
		},
		"no bound":          {md: map[string]any{"c2Concurrency": 0}, want: c2Settings{Concurrency: 0}},
		"bad appear grace":  {md: map[string]any{"c2AppearGrace": "soon"}, wantErr: true},
		"bad client grace":  {md: map[string]any{"c2ClientGrace": "10"}, wantErr: true},
		"bad startup delay": {md: map[string]any{"startupDelay": "later"}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var m sohMetadata

			if err := mapstructure.Decode(tc.md, &m); err != nil {
				t.Fatalf("mapstructure.Decode(%v) error = %v", tc.md, err)
			}

			err := m.init()
			if (err != nil) != tc.wantErr {
				t.Fatalf("init(%v) error = %v, want error %t", tc.md, err, tc.wantErr)
			}

			if err != nil {
				return
			}

			got := c2Settings{AppearGrace: m.c2AppearGrace, ClientGrace: m.c2ClientGrace, Concurrency: m.c2Concurrency}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("init(%v) C2 settings mismatch (-want +got):\n%s", tc.md, diff)
			}
		})
	}
}

// The run metadata of a run overrides the C2 settings, and each probe of the
// run gets the graces.
func TestRunChecksC2Settings(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := sohExperiment(t, map[string]any{
		"c2AppearGrace": "5s", "c2ClientGrace": "6s", "c2Concurrency": 3,
		"hostsToUseUUIDForC2Active": "web",
	}, []*v1.Node{sohNode("web", "linux")})

	useSOHMM(t, answerFrom(nil))

	for name, tc := range map[string]struct {
		md   map[string]any
		want c2Settings
	}{
		"settings": {
			md:   map[string]any{},
			want: c2Settings{AppearGrace: durationPtr(5 * time.Second), ClientGrace: durationPtr(6 * time.Second), Concurrency: 3},
		},
		"run metadata": {
			md: map[string]any{"c2AppearGrace": "1s", "c2ClientGrace": []string{"2s"}, "c2Concurrency": "7"},
			want: c2Settings{
				AppearGrace: durationPtr(time.Second), ClientGrace: durationPtr(2 * time.Second), Concurrency: 7,
			},
		},
		"bad run metadata": {
			md:   map[string]any{"c2AppearGrace": "later", "c2Concurrency": "many"},
			want: c2Settings{AppearGrace: durationPtr(5 * time.Second), ClientGrace: durationPtr(6 * time.Second), Concurrency: 3},
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSOH()
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			tc.md["checks"] = []string{"files"}

			if err := s.runChecks(app.SetContextMetadata(t.Context(), tc.md), exp); err != nil {
				t.Fatalf("runChecks(%v) error = %v", tc.md, err)
			}

			got := c2Settings{AppearGrace: s.md.c2AppearGrace, ClientGrace: s.md.c2ClientGrace, Concurrency: s.md.c2Concurrency}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("runChecks(%v) C2 settings mismatch (-want +got):\n%s", tc.md, diff)
			}

			f := c2Options(s.c2Options("exp", "web"), "appearGrace", "clientGrace", "idByUUID", "timeout")

			gotOpts := []any{time.Duration(f[0].Int()), time.Duration(f[1].Int()), f[2].Bool(), time.Duration(f[3].Int())}
			wantOpts := []any{*tc.want.AppearGrace, *tc.want.ClientGrace, true, defaultC2Timeout}

			if diff := cmp.Diff(wantOpts, gotOpts); diff != "" {
				t.Errorf("c2Options(exp, web) appear grace, client grace, UUID, timeout mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// Without the settings, the probes keep the mm defaults.
	s := newSOH()
	s.md.c2Timeout = time.Minute

	f := c2Options(s.c2Options("exp", "web"), "appearGrace", "clientGrace")
	got := []time.Duration{time.Duration(f[0].Int()), time.Duration(f[1].Int())}
	if want := []time.Duration{mm.DefaultC2AppearGrace, mm.DefaultC2ClientGrace}; !cmp.Equal(got, want) {
		t.Errorf("c2Options(exp, web) graces = %v, want the mm defaults", got)
	}
}

// inFlight answers each C2 command once all want commands have started, or
// after a short time, and records the most commands that ran at one time.
type inFlight struct {
	want    int32
	started atomic.Int32
	now     atomic.Int32
	most    atomic.Int32
	all     chan struct{}
	once    sync.Once
}

func newInFlight(want int32) *inFlight {
	return &inFlight{want: want, all: make(chan struct{})}
}

func (f *inFlight) answer(c c2Call) (string, error) {
	n := f.now.Add(1)
	defer f.now.Add(-1)

	for {
		most := f.most.Load()
		if n <= most || f.most.CompareAndSwap(most, n) {
			break
		}
	}

	if f.started.Add(1) >= f.want {
		f.once.Do(func() { close(f.all) })
	}

	select {
	case <-f.all:
	case <-time.After(200 * time.Millisecond):
	}

	switch {
	case c.Command == "cat /proc/loadavg":
		return "0.10 0.20 0.30 1/80 9", nil
	case strings.HasPrefix(c.Command, "stat "):
		return "present", nil
	}

	return "ok", nil
}

// The probes of a run run at most c2Concurrency at a time.
func TestRunChecksBoundsC2Probes(t *testing.T) { //nolint:paralleltest // replaces package globals
	nodes := []*v1.Node{sohNode("a", "linux"), sohNode("b", "linux"), sohNode("c", "linux")}
	files := map[string]any{"a": []any{"/f"}, "b": []any{"/f"}, "c": []any{"/f"}}

	for name, tc := range map[string]struct {
		md    map[string]any
		run   map[string]any
		limit int32
	}{
		"files, setting":        {md: map[string]any{"c2Concurrency": 2, "hostFiles": files}, run: map[string]any{"checks": "files"}, limit: 2},
		"files, run metadata":   {md: map[string]any{"hostFiles": files}, run: map[string]any{"checks": "files", "c2Concurrency": "1"}, limit: 1},
		"CPU load, setting":     {md: map[string]any{"c2Concurrency": 1}, run: map[string]any{"checks": "cpu-load"}, limit: 1},
		"CPU load, two at once": {md: map[string]any{"c2Concurrency": 2}, run: map[string]any{"checks": "cpu-load"}, limit: 2},
	} {
		t.Run(name, func(t *testing.T) {
			exp := sohExperiment(t, tc.md, nodes)
			fake := newInFlight(3)

			useSOHMM(t, fake.answer)

			s := newSOH(withRetryDelay(time.Millisecond))
			if err := s.decodeMetadata(exp); err != nil {
				t.Fatalf("decodeMetadata(exp) error = %v", err)
			}

			if err := s.runChecks(app.SetContextMetadata(t.Context(), tc.run), exp); err != nil {
				t.Fatalf("runChecks(%v) error = %v", tc.run, err)
			}

			if got := fake.started.Load(); got != 3 {
				t.Errorf("runChecks(%v) sent %d probes, want 3", tc.run, got)
			}

			if most := fake.most.Load(); most > tc.limit {
				t.Errorf("runChecks(%v) ran %d probes at one time, want at most %d", tc.run, most, tc.limit)
			}
		})
	}
}

// Custom tests and custom reachability tests also wait for a limiter slot.
func TestCustomProbesUseTheLimiter(t *testing.T) { //nolint:paralleltest // replaces package globals
	base := useSOHFilesDir(t)

	if err := os.MkdirAll(filepath.Join(base, "images", "exp"), 0o750); err != nil {
		t.Fatalf("os.MkdirAll(exp) error = %v", err)
	}

	for name, probe := range map[string]func(*SOH, *mm.StateGroup, int){
		"custom test": func(s *SOH, wg *mm.StateGroup, i int) {
			test := customHostTest{Name: fmt.Sprint("t", i), TestScript: "echo ok", TestStdout: "ok"}
			s.customTest(t.Context(), wg, "exp", sohNode("web", "linux"), test)
		},
		"custom reachability": func(s *SOH, wg *mm.StateGroup, i int) {
			s.connTest(t.Context(), wg, "exp", "web", "10.0.0.2", "tcp", 80+i, 0, "")
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newInFlight(3)

			useSOHMM(t, fake.answer)

			s := newSOH()
			s.limiter = mm.NewC2Limiter(1)

			wg := new(mm.StateGroup)

			for i := range 3 {
				probe(s, wg, i)
			}

			wg.Wait()

			if most := fake.most.Load(); most != 1 || fake.started.Load() != 3 {
				t.Errorf("%d %s probes ran at most %d at one time, want 3 probes, 1 at a time", fake.started.Load(), name, most)
			}
		})
	}
}

// A CPU load probe that cannot get a slot before the run ends records why.
func TestWaitForCPULoadWithoutASlot(t *testing.T) { //nolint:paralleltest // replaces package globals
	useSOHMM(t, answerFrom(nil))

	s := newSOH()
	s.limiter = mm.NewC2Limiter(1)
	s.nodes["web"] = sohNode("web", "linux")
	s.c2Hosts["web"] = struct{}{}

	if err := s.limiter.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if !s.waitForCPULoad(ctx, "exp") {
		t.Error("waitForCPULoad(no slot, done context) = false, want true")
	}

	want := fmt.Sprintf("waiting to run command 'cat /proc/loadavg': %v", context.Canceled)
	if got := s.status["web"].CPULoad; got != want {
		t.Errorf("waitForCPULoad(no slot, done context) CPU load = %q, want %q", got, want)
	}
}

// fakeClock is a clock that moves only when the test moves it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

// The network configuration probes of a host do not use up their time limit
// while they wait for a limiter slot.
func TestIsNetworkingConfiguredDeadlineStartsWithItsSlot(t *testing.T) { //nolint:paralleltest // replaces package globals
	var (
		mu    sync.Mutex
		tries int
	)

	useSOHMM(t, func(c2Call) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		if tries++; tries == 1 {
			return "no address yet", nil
		}

		return "inet 10.0.0.1/24", nil
	})

	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}

	s := newSOH(withRetryDelay(time.Millisecond), withClock(clock.now))
	s.md.c2Timeout = time.Minute
	s.limiter = mm.NewC2Limiter(1)

	// Another probe holds the only slot for longer than the time limit.
	if err := s.limiter.Acquire(t.Context()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	node := sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", ""))
	wg := new(mm.StateGroup)

	s.isNetworkingConfigured(t.Context(), wg, "exp", node, node.Network().Interfaces()[0])

	clock.add(2 * time.Minute)
	s.limiter.Release()
	wg.Wait()

	want := []mm.GroupState{mm.NewGroupSuccess("IP 10.0.0.1 configured", map[string]any{hostKey: "web"})}
	if diff := cmp.Diff(want, wg.States); diff != "" {
		t.Errorf("isNetworkingConfigured(queued past its time limit) results mismatch (-want +got):\n%s", diff)
	}
}

func TestRetryDeadline(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	d := newRetryDeadline(clock.now, time.Minute)

	steps := []struct {
		advance time.Duration
		queued  *time.Duration // a probe got a slot after this wait
		want    bool
	}{
		{advance: time.Hour, want: false},                   // no probe ran yet
		{queued: durationPtr(time.Hour), want: false},       // the limit starts now
		{advance: time.Minute, want: false},                 // at the limit
		{advance: time.Second, want: true},                  // past it
		{queued: durationPtr(2 * time.Second), want: false}, // a later wait does not count
		{advance: 2 * time.Second, want: true},
	}

	for i, step := range steps {
		clock.add(step.advance)

		if step.queued != nil {
			d.queued(*step.queued)
		}

		if got := d.passed(); got != step.want {
			t.Errorf("step %d: passed() = %t, want %t", i, got, step.want)
		}
	}
}

// appProfiles skips hosts without a profile, and stops when its consumer stops.
func TestAppProfilesStops(t *testing.T) { //nolint:paralleltest // replaces package globals
	profile := map[string]any{"sohProfile": map[string]any{"processes": []any{"nginx"}}}
	exp := sohExperiment(t, map[string]any{}, nil, &v2.ScenarioApp{NameF: "web-app", HostsF: []*v2.ScenarioAppHost{
		{HostnameF: "none"}, {HostnameF: "a", MetadataF: profile}, {HostnameF: "b", MetadataF: profile},
	}})

	s := newSOH()
	if err := s.decodeMetadata(exp); err != nil {
		t.Fatalf("decodeMetadata(exp) error = %v", err)
	}

	s.apps = exp.Spec.Scenario().Apps()

	var hosts []string

	for host := range s.appProfiles(sohLogger(t.Context(), "exp")) {
		hosts = append(hosts, host)

		break
	}

	if diff := cmp.Diff([]string{"a"}, hosts); diff != "" {
		t.Errorf("appProfiles() hosts until the first mismatch (-want +got):\n%s", diff)
	}
}
