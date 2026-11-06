package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/experiment"
	"phenix/store"
	"phenix/types"
	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/polltest"
)

// listed is the command that lists the experiment captures.
const listed = "capture pcap namespace"

func TestStart(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	tests := []struct {
		name    string
		req     StartRequest
		stopped bool
		prepare func(c *fakeCluster)
		// the start command, a regular expression; empty when no start is sent
		wantStart    string
		wantWarnings []string
		wantErr      error
	}{
		{
			name:      "starts a capture of every VLAN with the defaults",
			wantStart: `^capture pcap namespace exp1/files/exp1-\d{8}-\d{6}\.pcapng snaplen=1600 format=pcapng maxsize=10240M$`,
		},
		{
			name: "starts a capture of the VLANs, in the case the experiment gives them",
			req:  StartRequest{VLANs: []string{"exp_2", "EXP_2"}, Filter: "tcp port 80", Snaplen: 96, Format: "pcap"},
			wantStart: `^capture pcap namespace exp1/files/exp1-EXP_2-\d{8}-\d{6}\.pcap vlans=EXP_2 ` +
				`filter="tcp port 80" snaplen=96 format=pcap maxsize=10240M$`,
		},
		{
			name:      "sanitizes the name and forces its extension",
			req:       StartRequest{Name: "../my capture.pcap"},
			wantStart: `^capture pcap namespace exp1/files/_my_capture\.pcapng snaplen=1600 format=pcapng maxsize=10240M$`,
		},
		{
			name: "warns when the experiment uses more than half of the mirrors",
			prepare: func(c *fakeCluster) {
				c.mirrorsOn("compute2", "mega_bridge_exp1", testExp, 17)
			},
			wantStart:    `^capture pcap namespace `,
			wantWarnings: []string{"exp1 uses 18 of 32 mirrors on compute2 / mega_bridge_exp1"},
		},
		{
			name: "warns when few mirrors are left",
			prepare: func(c *fakeCluster) {
				c.mirrorsOn("compute1", "phenix", testExp, 17)
				c.mirrorsOn("compute1", "phenix", "", 11)
			},
			wantStart: `^capture pcap namespace `,
			wantWarnings: []string{
				"exp1 uses 18 of 32 mirrors on compute1 / phenix",
				"compute1 / phenix has 29 of 32 mirrors; 3 are left (18 by exp1, 11 by others)",
			},
		},
		{
			name: "counts only the bridges of the VLANs it captures",
			req:  StartRequest{VLANs: []string{"EXP_2"}},
			prepare: func(c *fakeCluster) {
				c.mirrorsOn("compute1", "phenix", "other", 32)
			},
			wantStart: `^capture pcap namespace exp1/files/exp1-EXP_2-`,
		},
		{
			name: "starts with a warning when the mirrors cannot be counted",
			prepare: func(c *fakeCluster) {
				c.failing = "mesh send"
			},
			wantStart: `^capture pcap namespace `,
			wantWarnings: []string{
				"cannot count Open vSwitch mirrors: listing the Open vSwitch mirrors of compute1, compute2: " +
					"listing the Open vSwitch mirrors of compute2: boom",
			},
		},
		{
			name: "refuses a capture that needs a mirror on a full bridge",
			prepare: func(c *fakeCluster) {
				c.mirrorsOn("compute1", "phenix", "other", 32)
			},
			wantErr: ErrBudget,
		},
		{
			name: "refuses a capture of an experiment that runs 32 captures",
			prepare: func(c *fakeCluster) {
				for i := range MaxExperimentCaptures {
					c.capture(map[string]string{"id": strconv.Itoa(i), "state": "running"})
				}
			},
			wantErr: ErrBudget,
		},
		{name: "refuses a VLAN of another experiment", req: StartRequest{VLANs: []string{"EXP_9"}}, wantErr: ErrInvalid},
		{name: "refuses a filter with a quote", req: StartRequest{Filter: `host "a"`}, wantErr: ErrInvalid},
		{name: "refuses a long filter", req: StartRequest{Filter: strings.Repeat("a", 1025)}, wantErr: ErrInvalid},
		{name: "refuses a short snaplen", req: StartRequest{Snaplen: 13}, wantErr: ErrInvalid},
		{name: "refuses a long snaplen", req: StartRequest{Snaplen: 65536}, wantErr: ErrInvalid},
		{name: "refuses another format", req: StartRequest{Format: "erf"}, wantErr: ErrInvalid},
		{name: "refuses a stopped experiment", stopped: true, wantErr: ErrNotRunning},
		{
			name: "refuses a name that a running capture writes",
			req:  StartRequest{Name: "mine"},
			prepare: func(c *fakeCluster) {
				c.capture(map[string]string{"id": "2", "state": "running", "path": "/phenix/images/exp1/files/mine.pcapng"})
			},
			wantErr: ErrPathInUse,
		},
		{
			name: "starts a name that a stopped capture wrote",
			req:  StartRequest{Name: "mine"},
			prepare: func(c *fakeCluster) {
				c.capture(map[string]string{"id": "2", "state": "stopped", "path": "/phenix/images/exp1/files/mine.pcapng"})
			},
			wantStart: `^capture pcap namespace exp1/files/mine\.pcapng `,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.stopped)

			cluster := &fakeCluster{vms: vmsOn(), startID: "4"}
			received := useCluster(t, cluster)

			if test.prepare != nil {
				test.prepare(cluster)
			}

			got, err := Start(t.Context(), testExp, test.req, 10240)
			if !errMatches(err, test.wantErr) {
				t.Fatalf("Start(%+v) error = %v, want %v", test.req, err, test.wantErr)
			}

			var start string

			for _, cmd := range received() {
				if strings.HasPrefix(cmd, listed+" ") {
					start = cmd
				}
			}

			if test.wantStart == "" {
				if start != "" {
					t.Errorf("Start(%+v) sent %q, want no start", test.req, start)
				}

				return
			}

			if !regexp.MustCompile(test.wantStart).MatchString(start) {
				t.Errorf("Start(%+v) sent %q, want it to match %s", test.req, start, test.wantStart)
			}

			if got.Capture.ID != "4" {
				t.Errorf("Start(%+v).Capture.ID = %q, want 4", test.req, got.Capture.ID)
			}

			if diff := cmp.Diff(test.wantWarnings, got.Warnings); diff != "" {
				t.Errorf("Start(%+v).Warnings mismatch (-want +got):\n%s", test.req, diff)
			}
		})
	}
}

// Start answers the capture as minimega lists it after the start.
func TestStartAnswersListedCapture(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	cluster := &fakeCluster{vms: vmsOn(), startID: "4", started: map[string]string{
		"id": "4", "state": "starting", "path": "/phenix/images/exp1/files/x.pcapng", "start": "2026-11-05T14:22:33Z",
	}}
	useCluster(t, cluster)

	got, err := Start(t.Context(), testExp, StartRequest{Name: "x"}, 0)
	if err != nil {
		t.Fatalf("Start(name x) error = %v", err)
	}

	want := mm.NamespaceCapture{
		ID: "4", State: "starting", Path: "/phenix/images/exp1/files/x.pcapng", Format: mm.CaptureFormatPcapng,
		Start: "2026-11-05T14:22:33Z",
	}
	if diff := cmp.Diff(want, got.Capture); diff != "" {
		t.Errorf("Start(name x).Capture mismatch (-want +got):\n%s", diff)
	}

	if got, want := File(testExp, got.Capture), "x.pcapng"; got != want {
		t.Errorf("File(exp1, capture 4) = %q, want %q", got, want)
	}
}

// Without minimega's support, nothing is sent and the error says why.
func TestUnsupported(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "version" {
			return []*minicli.Response{mmtest.Text("compute1", "minimega 3.3.0 abc 2026-01-01")}
		}

		return []*minicli.Response{mmtest.Error("compute1", "unexpected command "+cmd.Base)}
	})

	calls := map[string]func() error{
		"Start":   func() error { _, err := Start(t.Context(), testExp, StartRequest{}, 0); return err },
		"Stop":    func() error { _, err := Stop(t.Context(), testExp, "1"); return err },
		"Get":     func() error { _, err := Get(t.Context(), testExp, "1"); return err },
		"List":    func() error { _, err := List(t.Context(), testExp); return err },
		"Budgets": func() error { _, err := Budgets(t.Context(), testExp); return err },
	}

	for name, call := range calls {
		if err := call(); !errors.Is(err, mm.ErrCaptureUnsupported) {
			t.Errorf("%s() error = %v, want %v", name, err, mm.ErrCaptureUnsupported)
		}
	}

	for _, cmd := range received() {
		if cmd.Base != "version" {
			t.Errorf("minimega received %q, want only the version command", cmd.Raw)
		}
	}
}

// For an experiment that does not exist, each call fails as not found before it checks minimega's support.
func TestUnsupportedMissingExperiment(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	mm.ForgetCluster()
	t.Cleanup(mm.ForgetCluster)

	received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == "version" {
			return []*minicli.Response{mmtest.Text("compute1", "minimega 3.3.0 abc 2026-01-01")}
		}

		return []*minicli.Response{mmtest.Error("compute1", "unexpected command "+cmd.Base)}
	})

	const missing = "no-such-exp"

	calls := map[string]func() error{
		"Start": func() error { _, err := Start(t.Context(), missing, StartRequest{}, 0); return err },
		"Stop":  func() error { _, err := Stop(t.Context(), missing, "1"); return err },
		"Get":   func() error { _, err := Get(t.Context(), missing, "1"); return err },
		"List":  func() error { _, err := List(t.Context(), missing); return err },
	}

	for name, call := range calls {
		if err := call(); !errors.Is(err, store.ErrNotExist) {
			t.Errorf("%s(%s) error = %v, want %v", name, missing, err, store.ErrNotExist)
		}
	}

	if sent := received(); len(sent) != 0 {
		t.Errorf("minimega received %d commands, want none", len(sent))
	}
}

func TestStop(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	tests := []struct {
		name     string
		id       string
		stopped  bool
		prepare  func(c *fakeCluster)
		want     mm.NamespaceCapture
		wantSent []string
		wantErr  error
	}{
		{
			name:     "stops the capture and answers its final rows",
			id:       "2",
			want:     mm.NamespaceCapture{ID: "2", State: "stopped", Path: "exp1/files/b.pcapng", Format: "pcapng"},
			wantSent: []string{listed, "capture pcap delete namespace 2"},
		},
		{
			name:     "answers the listed capture when the stop gives no final rows",
			id:       "2",
			prepare:  func(c *fakeCluster) { c.noFinal = true },
			want:     mm.NamespaceCapture{ID: "2", State: "running", Path: "exp1/files/b.pcapng", Format: "pcapng"},
			wantSent: []string{listed, "capture pcap delete namespace 2"},
		},
		{
			name:     "answers the listed capture when minimega fails the stop",
			id:       "2",
			prepare:  func(c *fakeCluster) { c.failing = "capture pcap delete" },
			want:     mm.NamespaceCapture{ID: "2", State: "running", Path: "exp1/files/b.pcapng", Format: "pcapng"},
			wantSent: []string{listed, "capture pcap delete namespace 2"},
			wantErr:  errAny,
		},
		{name: "does not stop an unknown capture", id: "9", wantSent: []string{listed}, wantErr: ErrNotFound},
		{name: "does not ask about a stopped experiment", id: "2", stopped: true, wantErr: ErrNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, test.stopped)

			cluster := &fakeCluster{}
			received := useCluster(t, cluster)
			cluster.capture(map[string]string{"id": "1", "state": "running", "path": "exp1/files/a.pcapng"})
			cluster.capture(map[string]string{"id": "2", "state": "running", "path": "exp1/files/b.pcapng"})

			if test.prepare != nil {
				test.prepare(cluster)
			}

			got, err := Stop(t.Context(), testExp, test.id)
			if !errMatches(err, test.wantErr) {
				t.Fatalf("Stop(%s) error = %v, want %v", test.id, err, test.wantErr)
			}

			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("Stop(%s) mismatch (-want +got):\n%s", test.id, diff)
			}

			if diff := cmp.Diff(test.wantSent, received(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Stop(%s) commands mismatch (-want +got):\n%s", test.id, diff)
			}
		})
	}
}

func TestListAndGet(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	cluster := &fakeCluster{}
	useCluster(t, cluster)
	cluster.capture(map[string]string{"id": "10", "state": "stopped", "path": "exp1/files/b.pcap"})
	cluster.capture(map[string]string{"id": "9", "state": "running", "path": "exp1/files/a.pcapng", "vlans": "EXP_1"})

	captures, err := List(t.Context(), testExp)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	ids := make([]string, 0, len(captures))
	for _, c := range captures {
		ids = append(ids, c.ID)
	}

	if diff := cmp.Diff([]string{"9", "10"}, ids); diff != "" {
		t.Errorf("List() IDs mismatch (-want +got):\n%s", diff)
	}

	got, err := Get(t.Context(), testExp, "9")
	if err != nil {
		t.Fatalf("Get(9) error = %v", err)
	}

	want := mm.NamespaceCapture{
		ID: "9", State: "running", Path: "exp1/files/a.pcapng", Format: "pcapng", VLANs: []string{"EXP_1"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Get(9) mismatch (-want +got):\n%s", diff)
	}

	if _, err := Get(t.Context(), testExp, "11"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(11) error = %v, want %v", err, ErrNotFound)
	}
}

// A stopped experiment has no captures, and minimega is not asked about it.
func TestListStoppedExperiment(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, true)

	received := useCluster(t, &fakeCluster{})

	captures, err := List(t.Context(), testExp)
	if err != nil || len(captures) != 0 {
		t.Errorf("List() of a stopped experiment = %v, %v, want none", captures, err)
	}

	if _, err := Budgets(t.Context(), testExp); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Budgets() of a stopped experiment error = %v, want %v", err, ErrNotRunning)
	}

	if sent := received(); len(sent) != 0 {
		t.Errorf("minimega received %q, want nothing", sent)
	}
}

func TestBudgets(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	vms := vmsOn()
	vms = append(vms, mm.VM{Name: "c", Host: "", Networks: []string{"EXP_1 (101)"}}, mm.VM{
		Name: "outside", Host: "compute3", Networks: []string{"EXP_3 (103)", "disconnected"},
	})

	cluster := &fakeCluster{vms: vms}
	received := useCluster(t, cluster)
	cluster.mirrorsOn("compute1", "phenix", testExp, 17)
	cluster.mirrorsOn("compute1", "mega_bridge_exp1", "", 28)
	cluster.mirrorsOn("compute2", "mega_bridge_exp1", "other", 32)
	cluster.mirrorsOn("compute2", "phenix", testExp, 3)

	got, err := Budgets(t.Context(), testExp)
	if err != nil {
		t.Fatalf("Budgets() error = %v", err)
	}

	want := []Budget{
		{Host: "compute1", Bridge: "mega_bridge_exp1", Total: 28, Experiment: 0, Limit: 32, Level: LevelNear},
		{Host: "compute1", Bridge: "phenix", Total: 17, Experiment: 17, Limit: 32, Level: LevelHalf},
		{Host: "compute2", Bridge: "mega_bridge_exp1", Total: 32, Experiment: 0, Limit: 32, Level: LevelFull},
		{Host: "compute3", Bridge: "mega_bridge_exp1", Total: 0, Experiment: 0, Limit: 32, Level: LevelOK},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Budgets() mismatch (-want +got):\n%s", diff)
	}

	wantSent := []string{"bridge mirrors", "mesh send compute2,compute3 bridge mirrors"}
	if diff := cmp.Diff(wantSent, received()); diff != "" {
		t.Errorf("Budgets() commands mismatch (-want +got):\n%s", diff)
	}
}

// A failed read of the VMs is an error, never an empty budget.
func TestBudgetsVMInfoError(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	boom := errors.New("boom")
	useCluster(t, &fakeCluster{vms: vmsOn(), vmErr: boom})

	if got, err := Budgets(t.Context(), testExp); !errors.Is(err, boom) {
		t.Errorf("Budgets() = %v, %v, want error %v", got, err, boom)
	}
}

func TestLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		total, mine int
		want        string
	}{
		{total: 0, mine: 0, want: LevelOK},
		{total: 16, mine: 16, want: LevelOK},
		{total: 17, mine: 17, want: LevelHalf},
		{total: 27, mine: 0, want: LevelOK},
		{total: 28, mine: 0, want: LevelNear},
		{total: 31, mine: 20, want: LevelNear},
		{total: 32, mine: 0, want: LevelFull},
		{total: 40, mine: 40, want: LevelFull},
	}

	for _, test := range tests {
		if got := level(test.total, test.mine); got != test.want {
			t.Errorf("level(%d, %d) = %q, want %q", test.total, test.mine, got, test.want)
		}
	}
}

func TestBudgetMessages(t *testing.T) {
	t.Parallel()

	b := func(total, mine int) Budget {
		return Budget{Host: "compute1", Bridge: "mega_bridge_exp1", Total: total, Experiment: mine, Limit: 32, Level: ""}
	}

	if got, want := refused([]Budget{b(4, 0), b(32, 21)}), "compute1 / mega_bridge_exp1 has 32 of 32 Open vSwitch mirrors"; got != want {
		t.Errorf("refused(a full bridge) = %q, want %q", got, want)
	}

	if got := refused([]Budget{b(31, 21)}); got != "" {
		t.Errorf("refused(a bridge with one mirror left) = %q, want none", got)
	}

	tests := []struct {
		budget Budget
		want   []string
	}{
		{budget: b(4, 2)},
		{budget: b(16, 16), want: []string{"exp1 uses 17 of 32 mirrors on compute1 / mega_bridge_exp1"}},
		{budget: b(27, 5), want: []string{"compute1 / mega_bridge_exp1 has 28 of 32 mirrors; 4 are left (6 by exp1, 22 by others)"}},
		{budget: b(30, 5), want: []string{"compute1 / mega_bridge_exp1 has 31 of 32 mirrors; 1 is left (6 by exp1, 25 by others)"}},
		{
			budget: b(28, 17),
			want: []string{
				"exp1 uses 18 of 32 mirrors on compute1 / mega_bridge_exp1",
				"compute1 / mega_bridge_exp1 has 29 of 32 mirrors; 3 are left (18 by exp1, 11 by others)",
			},
		},
	}

	for _, test := range tests {
		got := budgetWarnings(testExp, []Budget{test.budget})
		if diff := cmp.Diff(test.want, got); diff != "" {
			t.Errorf("budgetWarnings(%+v) mismatch (-want +got):\n%s", test.budget, diff)
		}
	}
}

func TestDefaultName(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 5, 14, 22, 33, 0, time.UTC)

	tests := []struct {
		vlans []string
		want  string
	}{
		{want: "exp1-20261105-142233"},
		{vlans: []string{"EXP_1"}, want: "exp1-EXP_1-20261105-142233"},
		{vlans: []string{"EXP_1", "EXP_2"}, want: "exp1-20261105-142233"},
	}

	for _, test := range tests {
		if got := defaultName(testExp, test.vlans, now); got != test.want {
			t.Errorf("defaultName(exp1, %q) = %q, want %q", test.vlans, got, test.want)
		}
	}
}

func TestFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path, want string
	}{
		{path: "/phenix/images/exp1/files/a.pcapng", want: "a.pcapng"},
		{path: "exp1/files/sub/a.pcap", want: "sub/a.pcap"},
		{path: "/elsewhere/a.pcap"},
		{path: ""},
	}

	for _, test := range tests {
		if got := File(testExp, mm.NamespaceCapture{Path: test.path}); got != test.want {
			t.Errorf("File(exp1, path %q) = %q, want %q", test.path, got, test.want)
		}
	}
}

// A start waits for the start before it, also of another experiment, and gives up when its context ends.
func TestLockStarts(t *testing.T) {
	t.Parallel()

	unlock, err := lockStarts(t.Context(), "lock-test")
	if err != nil {
		t.Fatalf("lockStarts() error = %v", err)
	}

	for _, exp := range []string{"lock-test", "lock-test-other"} {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)

		release, err := lockStarts(ctx, exp)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("lockStarts(%s) while another start runs error = %v, want %v", exp, err, context.DeadlineExceeded)
		}

		if release != nil {
			release()
		}

		cancel()
	}

	unlock()

	other, err := lockStarts(t.Context(), "lock-test-other")
	if err != nil {
		t.Fatalf("lockStarts() of another experiment after the start before error = %v", err)
	}

	other()
}

// A start of an experiment that stops while the start waits for the lock fails with ErrNotRunning and asks minimega
// nothing about the namespace.
func TestStartExperimentStopsWhileWaiting(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	received := useCluster(t, &fakeCluster{})

	stopping := &stoppingStore{fakeStore: store.DefaultStore.(*fakeStore)} //nolint:forcetypeassert // set by useExperiment
	store.DefaultStore = stopping                                          //nolint:reassign // useExperiment restores it

	unlock, err := lockStarts(t.Context(), "another-experiment")
	if err != nil {
		t.Fatalf("lockStarts() error = %v", err)
	}

	result := make(chan error, 1)

	go func() {
		_, err := Start(t.Context(), testExp, StartRequest{}, 0)
		result <- err
	}()

	polltest.Until(t, "the start reads the running experiment", func() bool { return stopping.count() >= 1 })
	unlock()

	if err := <-result; !errors.Is(err, ErrNotRunning) {
		t.Errorf("Start() of an experiment that stopped while it waited error = %v, want %v", err, ErrNotRunning)
	}

	if sent := received(); len(sent) != 0 {
		t.Errorf("minimega received %q, want nothing", sent)
	}
}

// A default file name that a running capture writes, or that the experiment's files directory has, gets the first
// free suffix; a name that the start names is never changed.
func TestFreeFile(t *testing.T) { //nolint:paralleltest // replaces the phenix base
	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	files := filepath.Join(base, "images", testExp, "files")
	if err := os.MkdirAll(files, 0o750); err != nil {
		t.Fatal(err)
	}

	const stem = "exp1/files/exp1-20261105-142233"

	if err := os.WriteFile(filepath.Join(files, "exp1-20261105-142233.pcapng"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(base, "missing"), filepath.Join(files, "link.pcapng")); err != nil {
		t.Fatal(err)
	}

	running := func(paths ...string) []mm.NamespaceCapture {
		captures := make([]mm.NamespaceCapture, 0, len(paths))
		for i, p := range paths {
			captures = append(captures, mm.NamespaceCapture{ID: strconv.Itoa(i), State: "running", Path: p})
		}

		return captures
	}

	every := make([]string, 0, maxNameSuffix)
	for n := 2; n <= maxNameSuffix; n++ {
		every = append(every, fmt.Sprintf("%s-%d.pcapng", stem, n))
	}

	tests := map[string]struct {
		file      string
		defaulted bool
		captures  []mm.NamespaceCapture
		want      string
		wantErr   error
	}{
		"a default name that a file has": {file: stem + ".pcapng", defaulted: true, want: stem + "-2.pcapng"},
		"a default name that a file and a capture have": {
			file: stem + ".pcapng", defaulted: true, captures: running(stem + "-2.pcapng"), want: stem + "-3.pcapng",
		},
		"a free default name": {file: "exp1/files/exp1-EXP_1-20261105-142233.pcap", defaulted: true,
			want: "exp1/files/exp1-EXP_1-20261105-142233.pcap"},
		"a default name with no free suffix": {
			file: stem + ".pcapng", defaulted: true, captures: running(every...), wantErr: ErrPathInUse,
		},
		"a given name that a file has":    {file: stem + ".pcapng", wantErr: ErrPathInUse},
		"a given name that a symlink has": {file: "exp1/files/link.pcapng", wantErr: ErrPathInUse},
		"a given name that no file has":   {file: "exp1/files/mine.pcap", want: "exp1/files/mine.pcap"},
		"a given name that a capture writes": {
			file: "exp1/files/mine.pcap", captures: running("/phenix/images/exp1/files/mine.pcap"), wantErr: ErrPathInUse,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			format := strings.TrimPrefix(filepath.Ext(tc.file), ".")
			valid := request{vlans: nil, filter: "", snaplen: 0, format: format, file: tc.file, defaulted: tc.defaulted}

			got, err := freeFile(testExp, valid, tc.captures)
			if !errors.Is(err, tc.wantErr) || got != tc.want {
				t.Errorf("freeFile(%s, defaulted %t) = %q, %v; want %q, %v", tc.file, tc.defaulted, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

// A node with two interfaces on one VLAN on two bridges needs a mirror on each bridge.
func TestBudgetsOfInterfacesOnOneVLAN(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperimentNodes(t, false,
		testNode("a",
			map[string]any{"name": "IF0", "vlan": "EXP_1", "bridge": "phenix"},
			map[string]any{"name": "IF1", "vlan": "EXP_1", "bridge": "other"},
			map[string]any{"name": "IF2", "vlan": "EXP_1"},
		),
	)

	cluster := &fakeCluster{vms: mm.VMs{{Name: "a", Host: "compute1", Networks: []string{"EXP_1 (101)", "EXP_1 (101)"}}}}
	useCluster(t, cluster)

	got, err := Budgets(t.Context(), testExp)
	if err != nil {
		t.Fatalf("Budgets() error = %v", err)
	}

	want := []Budget{
		{Host: "compute1", Bridge: "mega_bridge_exp1", Total: 0, Experiment: 0, Limit: 32, Level: LevelOK},
		{Host: "compute1", Bridge: "other", Total: 0, Experiment: 0, Limit: 32, Level: LevelOK},
		{Host: "compute1", Bridge: "phenix", Total: 0, Experiment: 0, Limit: 32, Level: LevelOK},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Budgets() mismatch (-want +got):\n%s", diff)
	}
}

// A failed listing of the captures is an error, never an empty list.
func TestListFails(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	useCluster(t, &fakeCluster{failing: listed})

	if got, err := List(t.Context(), testExp); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("List() with a failing minimega = %v, %v, want an error that is not %v", got, err, ErrNotFound)
	}

	if _, err := Get(t.Context(), testExp, "1"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Get(1) with a failing minimega error = %v, want an error that is not %v", err, ErrNotFound)
	}
}

// A start that minimega fails is an error that is not ErrInvalid, as the request was valid; one that fails the list
// before it sends no start.
func TestStartMinimegaFails(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	tests := []struct {
		name, failing string
		wantStart     bool
	}{
		{name: "the list of the captures", failing: listed},
		{name: "the start", failing: listed + " exp1/", wantStart: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, false)

			received := useCluster(t, &fakeCluster{vms: vmsOn(), failing: test.failing})

			got, err := Start(t.Context(), testExp, StartRequest{}, 0)
			if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrBudget) {
				t.Errorf("Start() when minimega fails %s = %+v, %v, want an error that is not %v or %v",
					test.name, got, err, ErrInvalid, ErrBudget)
			}

			sent := slices.ContainsFunc(received(), func(cmd string) bool { return strings.HasPrefix(cmd, listed+" ") })
			if sent != test.wantStart {
				t.Errorf("Start() when minimega fails %s sent a start: %t, want %t", test.name, sent, test.wantStart)
			}
		})
	}
}

// A start of a VLAN whose alias minimega does not take as one argument fails with ErrInvalid and sends no start.
func TestStartRefusesUnsafeVLAN(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperimentNodes(t, false, testNode("a", map[string]any{"name": "IF0", "vlan": "A,B"}))

	received := useCluster(t, &fakeCluster{vms: vmsOn()})

	_, err := Start(t.Context(), testExp, StartRequest{VLANs: []string{"a,b"}}, 0)
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, mm.ErrUnsafeArg) {
		t.Errorf("Start(VLAN a,b) error = %v, want %v and %v", err, ErrInvalid, mm.ErrUnsafeArg)
	}

	for _, cmd := range received() {
		if strings.HasPrefix(cmd, listed+" ") {
			t.Errorf("Start(VLAN a,b) sent %q, want no start", cmd)
		}
	}
}

// A start may name a VLAN that only the experiment spec's aliases have, and a topology node without a network adds
// no VLAN.
func TestStartVLANOfSpecAliases(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperimentSpec(t, false, map[string]any{
		"experimentName": testExp,
		"vlans":          map[string]any{"aliases": map[string]any{"SPEC_1": 200}},
		"topology": map[string]any{"nodes": []map[string]any{
			{
				"type":     "VirtualMachine",
				"general":  map[string]any{"hostname": "plain"},
				"hardware": map[string]any{"os_type": "linux", "drives": []map[string]any{{"image": "a.qc2"}}},
			},
		}},
	})

	received := useCluster(t, &fakeCluster{vms: vmsOn()})

	if _, err := Start(t.Context(), testExp, StartRequest{VLANs: []string{"spec_1"}, Name: "x"}, 0); err != nil {
		t.Fatalf("Start(VLAN spec_1) error = %v", err)
	}

	want := listed + " exp1/files/x.pcapng vlans=SPEC_1 snaplen=1600 format=pcapng"
	if got := received(); !slices.Contains(got, want) {
		t.Errorf("Start(VLAN spec_1) sent %q, want %q", got, want)
	}

	exp, err := experiment.Get(testExp)
	if err != nil {
		t.Fatalf("experiment.Get(%s) error = %v", testExp, err)
	}

	if diff := cmp.Diff([]string{"EXP_1", "EXP_2", "SPEC_1"}, vlanAliases(exp)); diff != "" {
		t.Errorf("vlanAliases(exp1) mismatch (-want +got):\n%s", diff)
	}
}

// A start that waits for another start gives up when its context ends, and sends nothing.
func TestStartGivesUpWaiting(t *testing.T) { //nolint:paralleltest // shares the fake minimega and the start lock
	useExperiment(t, false)

	received := useCluster(t, &fakeCluster{vms: vmsOn()})

	unlock, err := lockStarts(t.Context(), "another-experiment")
	if err != nil {
		t.Fatalf("lockStarts() error = %v", err)
	}

	defer unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	if _, err := Start(ctx, testExp, StartRequest{}, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Start() while another start runs error = %v, want %v", err, context.DeadlineExceeded)
	}

	if sent := received(); len(sent) != 0 {
		t.Errorf("minimega received %q, want nothing", sent)
	}
}

// A start of an experiment that is deleted while the start waits fails as not found and sends nothing.
func TestStartExperimentDeletedWhileWaiting(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	received := useCluster(t, &fakeCluster{vms: vmsOn()})

	stored := store.DefaultStore.(*fakeStore)               //nolint:forcetypeassert // set by useExperiment
	store.DefaultStore = &vanishingStore{fakeStore: stored} //nolint:reassign // useExperiment restores it

	if _, err := Start(t.Context(), testExp, StartRequest{}, 0); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("Start() of an experiment deleted while it waited error = %v, want %v", err, store.ErrNotExist)
	}

	if sent := received(); len(sent) != 0 {
		t.Errorf("minimega received %q, want nothing", sent)
	}
}

// A start whose context ends while it counts the mirrors fails with the context's error and sends no start.
func TestStartCanceledWhileCounting(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	received := useCluster(t, &fakeCluster{vms: vmsOn(), onVMInfo: cancel})

	if _, err := Start(ctx, testExp, StartRequest{}, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("Start() canceled while it counts the mirrors error = %v, want %v", err, context.Canceled)
	}

	for _, cmd := range received() {
		if strings.HasPrefix(cmd, listed+" ") {
			t.Errorf("Start() canceled while it counts the mirrors sent %q, want no start", cmd)
		}
	}
}

// An experiment name that minimega does not take in a file name fails validation with ErrInvalid.
func TestValidateRefusesUnsafeExperimentName(t *testing.T) {
	t.Parallel()

	exp := &types.Experiment{Metadata: store.ConfigMetadata{Name: "my exp"}}

	_, err := validate(exp, StartRequest{Name: "x"}, time.Now())
	if !errors.Is(err, ErrInvalid) || !errors.Is(err, mm.ErrUnsafeArg) {
		t.Errorf("validate(experiment %q) error = %v, want %v and %v", exp.Metadata.Name, err, ErrInvalid, mm.ErrUnsafeArg)
	}

	if aliases := vlanAliases(exp); len(aliases) != 0 {
		t.Errorf("vlanAliases(an experiment with no spec or status) = %q, want none", aliases)
	}
}

// Budgets of an experiment that does not exist fails as not found, and asks minimega nothing.
func TestBudgetsMissingExperiment(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	received := useCluster(t, &fakeCluster{vms: vmsOn()})

	if _, err := Budgets(t.Context(), "no-such-exp"); !errors.Is(err, store.ErrNotExist) {
		t.Errorf("Budgets(no-such-exp) error = %v, want %v", err, store.ErrNotExist)
	}

	if sent := received(); len(sent) != 0 {
		t.Errorf("minimega received %q, want nothing", sent)
	}
}

// An experiment with no VM on a host has an empty budget, not a null one, and minimega is not asked for mirrors.
func TestBudgetsWithoutVMs(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperiment(t, false)

	received := useCluster(t, &fakeCluster{vms: mm.VMs{{Name: "a", Host: "", Networks: []string{"EXP_1 (101)"}}}})

	got, err := Budgets(t.Context(), testExp)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("Budgets() with no VM on a host = %#v, %v, want an empty budget", got, err)
	}

	if sent := received(); len(sent) != 0 {
		t.Errorf("minimega received %q, want nothing", sent)
	}
}

// An experiment with no default bridge uses phenix's, and a VLAN that a node's interfaces do not name is on it.
func TestBudgetsFallbackBridge(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	useExperimentSpec(t, false, map[string]any{
		"experimentName": testExp,
		"topology": map[string]any{"nodes": []map[string]any{
			testNode("a", map[string]any{"name": "IF0", "vlan": "EXP_1", "bridge": "br0"}),
		}},
	})

	useCluster(t, &fakeCluster{vms: mm.VMs{{Name: "a", Host: "compute1", Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}}}})

	got, err := Budgets(t.Context(), testExp)
	if err != nil {
		t.Fatalf("Budgets() error = %v", err)
	}

	want := []Budget{
		{Host: "compute1", Bridge: "br0", Total: 0, Experiment: 0, Limit: 32, Level: LevelOK},
		{Host: "compute1", Bridge: defaultBridge, Total: 0, Experiment: 0, Limit: 32, Level: LevelOK},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Budgets() mismatch (-want +got):\n%s", diff)
	}
}
