package health

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"pgregory.net/rapid"
)

// one runs a check once and returns its results.
func one(t *testing.T, c Check) map[string][]Result {
	t.Helper()

	return c.Run(t.Context())
}

// only returns the single result of a key.
func only(t *testing.T, results map[string][]Result, key string) Result {
	t.Helper()

	if len(results[key]) != 1 {
		t.Fatalf("%s = %+v, want one result", key, results[key])
	}

	return results[key][0]
}

func TestDiskThresholds(t *testing.T) {
	t.Parallel()

	tests := map[uint64]Status{89: Pass, 90: Warn, 97: Warn, 98: Fail}

	for used, status := range tests {
		t.Run(fmt.Sprintf("%d%% used", used), func(t *testing.T) {
			t.Parallel()

			statfs := func(string) (DiskStat, error) {
				return DiskStat{Device: 1, Total: 100, Avail: 100 - used}, nil
			}

			paths := func() []DiskPath { return []DiskPath{{ID: "files-dir", Path: "/phenix/images"}} }

			want := Result{
				ComponentID:   "files-dir",
				ComponentType: TypeSystem,
				ObservedValue: Float(used),
				ObservedUnit:  "percent",
				Status:        status,
				Output:        fmt.Sprintf("/phenix/images: %d bytes free", 100-used),
			}

			if diff := cmp.Diff(want, only(t, one(t, Disk(paths, statfs)), KeyDisk)); diff != "" {
				t.Errorf("Disk() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Paths on one filesystem are measured once, and a path that cannot be
// measured warns without a value.
func TestDiskFilesystems(t *testing.T) {
	t.Parallel()

	statfs := func(path string) (DiskStat, error) {
		if path == "/missing" {
			return DiskStat{}, errors.New("no such file or directory")
		}

		return DiskStat{Device: 7, Total: 1000, Avail: 500}, nil
	}

	paths := func() []DiskPath {
		return []DiskPath{
			{ID: "files-dir", Path: "/phenix/images"},
			{ID: "phenix-base", Path: "/phenix"},
			{ID: "other", Path: "/missing"},
		}
	}

	want := []Result{
		{
			ComponentID:   "files-dir",
			ComponentType: TypeSystem,
			ObservedValue: Float(50),
			ObservedUnit:  "percent",
			Status:        Pass,
			Output:        "/phenix/images: 500 bytes free",
		},
		{
			ComponentID:   "other",
			ComponentType: TypeSystem,
			Status:        Warn,
			Output:        "measuring /missing: no such file or directory",
		},
	}

	if diff := cmp.Diff(want, one(t, Disk(paths, statfs))[KeyDisk]); diff != "" {
		t.Errorf("Disk() mismatch (-want +got):\n%s", diff)
	}
}

func TestQemuImg(t *testing.T) {
	t.Parallel()

	var (
		found   = func(string) (string, error) { return "/usr/bin/qemu-img", nil }
		version = func(context.Context, string) (string, error) {
			return "qemu-img version 8.2.2 (Debian 1:8.2.2+ds-0ubuntu1)", nil
		}
		answers = func(out string, err error) func(context.Context, string) (string, error) {
			return func(context.Context, string) (string, error) { return out, err }
		}
	)

	tests := map[string]struct {
		lookPath func(string) (string, error)
		version  func(context.Context, string) (string, error)
		want     Result
	}{
		"version fails": {
			lookPath: found,
			version:  answers("", errors.New("exit status 1")),
			want: Result{
				ComponentID:       "qemu-img",
				ComponentType:     TypeComponent,
				Status:            Warn,
				AffectedEndpoints: []string{"/api/v1/disks"},
				Output:            "running /usr/bin/qemu-img --version: exit status 1",
			},
		},
		"other format": {
			lookPath: found,
			version:  answers("  qemu-img 9.0\nCopyright\n", nil),
			want: Result{
				ComponentID:   "qemu-img",
				ComponentType: TypeComponent,
				ObservedValue: "qemu-img 9.0",
				Status:        Pass,
				Output:        "/usr/bin/qemu-img: qemu-img 9.0",
			},
		},
		"empty output": {
			lookPath: found,
			version:  answers("", nil),
			want: Result{
				ComponentID:   "qemu-img",
				ComponentType: TypeComponent,
				Status:        Pass,
				Output:        "/usr/bin/qemu-img: ",
			},
		},
		"missing": {
			lookPath: func(string) (string, error) { return "", errors.New("not found") },
			want: Result{
				ComponentID:       "qemu-img",
				ComponentType:     TypeComponent,
				Status:            Warn,
				AffectedEndpoints: []string{"/api/v1/disks"},
				Output:            "qemu-img not found, so phenix inspects disk images through minimega",
			},
		},
		"found": {
			lookPath: found,
			version:  version,
			want: Result{
				ComponentID:   "qemu-img",
				ComponentType: TypeComponent,
				ObservedValue: "8.2.2",
				Status:        Pass,
				Output:        "/usr/bin/qemu-img: qemu-img version 8.2.2 (Debian 1:8.2.2+ds-0ubuntu1)",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tc.want, only(t, one(t, QemuImg(tc.lookPath, tc.version)), KeyQemuImg)); diff != "" {
				t.Errorf("QemuImg() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWebShark(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	always := func() bool { return true }

	missing := only(t, one(t, WebShark(dir, always)), KeyWebShark)

	// the output ends with the platform's stat error
	want := Result{ComponentID: "webshark", ComponentType: TypeComponent, Status: Warn, AffectedEndpoints: []string{"/webshark/"}}
	if diff := cmp.Diff(want, missing, cmpopts.IgnoreFields(Result{}, "Output")); diff != "" {
		t.Errorf("WebShark() without index.html mismatch (-want +got):\n%s", diff)
	}

	if !strings.HasPrefix(missing.Output, "WebShark build missing: ") {
		t.Errorf("WebShark() without index.html: Output = %q, want the missing build", missing.Output)
	}

	if err := os.WriteFile(filepath.Join(dir, "index.html"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	want = Result{ComponentID: "webshark", ComponentType: TypeComponent, Status: Pass}
	if diff := cmp.Diff(want, only(t, one(t, WebShark(dir, always)), KeyWebShark)); diff != "" {
		t.Errorf("WebShark() with index.html mismatch (-want +got):\n%s", diff)
	}

	// index.html that is a directory is not a build
	notFile := t.TempDir()
	if err := os.Mkdir(filepath.Join(notFile, "index.html"), 0o700); err != nil {
		t.Fatal(err)
	}

	want = Result{
		ComponentID:       "webshark",
		ComponentType:     TypeComponent,
		Status:            Warn,
		AffectedEndpoints: []string{"/webshark/"},
		Output:            filepath.Join(notFile, "index.html") + " is not a file",
	}
	if diff := cmp.Diff(want, only(t, one(t, WebShark(notFile, always)), KeyWebShark)); diff != "" {
		t.Errorf("WebShark() with an index.html directory mismatch (-want +got):\n%s", diff)
	}
}

// fakeMinimega answers version, namespace and mesh status, with the mesh size
// it is given, or fails every command.
type fakeMinimega struct {
	size     string
	err      error
	commands []string
}

func (f *fakeMinimega) run(_ context.Context, command string) ([]*minicli.Response, error) {
	f.commands = append(f.commands, command)

	if f.err != nil {
		return nil, f.err
	}

	switch command {
	case "version":
		return []*minicli.Response{{Host: "head", Response: "minimega 2.9 abc 2026-01-01"}}, nil
	case "namespace":
		return []*minicli.Response{{
			Host:    "head",
			Header:  []string{"namespace", "vlans", "active"},
			Tabular: [][]string{{"exp1", "101-200", "true"}, {"exp2", "201-300", "false"}},
		}}, nil
	case "mesh status":
		return []*minicli.Response{{
			Host:    "head",
			Header:  []string{"size", "degree", "peers", "context", "port"},
			Tabular: [][]string{{f.size, "2", "1", "phenix", "9000"}},
		}}, nil
	}

	return nil, errors.New("unexpected command " + command)
}

func TestMinimega(t *testing.T) {
	t.Parallel()

	mm := &fakeMinimega{size: "3"}
	check := Minimega(mm.run)

	got := one(t, check)

	timing := only(t, got, KeyMinimegaTime)
	if _, ok := timing.ObservedValue.(Float); !ok {
		t.Errorf("Minimega() response time = %#v, want a Float of milliseconds", timing.ObservedValue)
	}

	// the response time varies
	got[KeyMinimegaTime][0].ObservedValue = nil

	want := map[string][]Result{
		KeyMinimegaTime: {{ComponentID: "minimega", ComponentType: TypeComponent, ObservedUnit: "ms", Status: Pass}},
		KeyMinimegaVersion: {{
			ComponentID:   "minimega",
			ComponentType: TypeComponent,
			ObservedValue: "2.9",
			Status:        Pass,
			Output:        "minimega 2.9 abc 2026-01-01",
		}},
		KeyMinimegaNamespaces: {{
			ComponentID:   "minimega",
			ComponentType: TypeComponent,
			ObservedValue: 2,
			ObservedUnit:  "namespaces",
			Status:        Pass,
		}},
		KeyMeshNodes: {{
			ComponentID:   "minimega-mesh",
			ComponentType: TypeSystem,
			ObservedValue: 3,
			ObservedUnit:  "nodes",
			Status:        Pass,
			Output:        "degree 2, peers 1",
		}},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Minimega() mismatch (-want +got):\n%s", diff)
	}

	// a node leaves
	mm.size = "2"

	wantMesh := Result{
		ComponentID:       "minimega-mesh",
		ComponentType:     TypeSystem,
		ObservedValue:     2,
		ObservedUnit:      "nodes",
		Status:            Warn,
		AffectedEndpoints: minimegaEndpoints,
		Output:            "mesh had 3 nodes and now has 2 (degree 2, peers 1)",
	}

	if diff := cmp.Diff(wantMesh, only(t, one(t, check), KeyMeshNodes)); diff != "" {
		t.Errorf("Minimega() of a smaller mesh mismatch (-want +got):\n%s", diff)
	}
}

// When minimega does not answer, every key fails and none has a value, so
// each keeps its type across runs.
func TestMinimegaUnreachable(t *testing.T) {
	t.Parallel()

	mm := &fakeMinimega{err: errors.New("unable to dial")}

	failure := func(id, componentType string) []Result {
		return []Result{{
			ComponentID:       id,
			ComponentType:     componentType,
			Status:            Fail,
			AffectedEndpoints: minimegaEndpoints,
			Output:            "minimega did not answer: unable to dial",
		}}
	}

	want := map[string][]Result{
		KeyMinimegaTime:       failure("minimega", TypeComponent),
		KeyMinimegaVersion:    failure("minimega", TypeComponent),
		KeyMinimegaNamespaces: failure("minimega", TypeComponent),
		KeyMeshNodes:          failure("minimega-mesh", TypeSystem),
	}

	if diff := cmp.Diff(want, one(t, Minimega(mm.run))); diff != "" {
		t.Errorf("Minimega() of an unreachable minimega mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"version"}, mm.commands); diff != "" {
		t.Errorf("Minimega() commands mismatch (-want +got):\n%s", diff)
	}
}

func TestStore(t *testing.T) {
	t.Parallel()

	got := only(t, one(t, Store("bolt", func() error { return nil })), KeyStore)

	if _, ok := got.ObservedValue.(Float); !ok {
		t.Errorf("Store() of a readable store: ObservedValue = %#v, want a Float of milliseconds", got.ObservedValue)
	}

	// the response time varies
	got.ObservedValue = nil

	want := Result{ComponentID: "bolt", ComponentType: TypeDatastore, ObservedUnit: "ms", Status: Pass}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Store() of a readable store mismatch (-want +got):\n%s", diff)
	}

	want = Result{ComponentID: "bolt", ComponentType: TypeDatastore, Status: Fail, Output: "listing configs: connection refused"}
	got = only(t, one(t, Store("bolt", func() error { return errors.New("connection refused") })), KeyStore)

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Store() of an unreadable store mismatch (-want +got):\n%s", diff)
	}
}

// The broker queue warns from 75 percent full, and a queue of no capacity
// reports 0 percent and passes.
func TestBrokerQueue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		queued, capacity int
		percent          float64
		status           Status
		endpoints        []string
	}{
		{queued: 0, capacity: 100, percent: 0, status: Pass},
		{queued: 74, capacity: 100, percent: 74, status: Pass},
		{queued: 75, capacity: 100, percent: 75, status: Warn, endpoints: []string{"/api/v1/ws"}},
		{queued: 80, capacity: 100, percent: 80, status: Warn, endpoints: []string{"/api/v1/ws"}},
		{queued: 0, capacity: 0, percent: 0, status: Pass},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d of %d queued", tc.queued, tc.capacity), func(t *testing.T) {
			t.Parallel()

			stats := func() BrokerStats { return BrokerStats{Clients: 3, Queued: tc.queued, Capacity: tc.capacity} }

			want := map[string][]Result{
				KeyWebsocketClients: {{
					ComponentID:   "broker",
					ComponentType: TypeSystem,
					ObservedValue: 3,
					ObservedUnit:  "clients",
					Status:        Pass,
				}},
				KeyWebsocketQueue: {{
					ComponentID:       "broker",
					ComponentType:     TypeSystem,
					ObservedValue:     Float(tc.percent),
					ObservedUnit:      "percent",
					Status:            tc.status,
					AffectedEndpoints: tc.endpoints,
					Output:            fmt.Sprintf("%d of %d broadcasts queued", tc.queued, tc.capacity),
				}},
			}

			if diff := cmp.Diff(want, one(t, Broker(stats))); diff != "" {
				t.Errorf("Broker(%+v) mismatch (-want +got):\n%s", stats(), diff)
			}
		})
	}
}

// Uptime reports the whole seconds since its start, and GoRuntime reports a
// goroutine count, the bytes of live heap objects and a GC pause.
func TestUptimeAndGoRuntime(t *testing.T) {
	t.Parallel()

	uptime := only(t, one(t, Uptime(time.Now().Add(-time.Minute))), KeyUptime)

	// a slow machine can take a second or two between the clock reads
	if s, ok := uptime.ObservedValue.(int64); !ok || s < 60 || s > 62 {
		t.Errorf("Uptime(a minute ago) %s = %#v, want an int64 of 60 to 62", KeyUptime, uptime.ObservedValue)
	}

	uptime.ObservedValue = nil

	want := Result{ComponentType: TypeSystem, ObservedUnit: "s", Status: Pass}
	if diff := cmp.Diff(want, uptime); diff != "" {
		t.Errorf("Uptime(a minute ago) mismatch, value aside (-want +got):\n%s", diff)
	}

	results := one(t, GoRuntime())

	r := only(t, results, KeyGoroutines)
	if n, ok := r.ObservedValue.(uint64); !ok || n == 0 {
		t.Errorf("GoRuntime() %s = %+v, want a uint64 count of goroutines", KeyGoroutines, r)
	}

	for _, key := range []string{KeyHeapObjects, KeyGCPause} {
		if r := only(t, results, key); r.Status != Pass || r.ObservedValue == nil {
			t.Errorf("GoRuntime() %s = %+v, want a passing value", key, r)
		}
	}
}

// checksMeasured is what the fake statfs of TestDiskProperties says of a path.
type checksMeasured struct {
	stat  DiskStat
	fails bool
}

func genChecksMeasured() *rapid.Generator[checksMeasured] {
	return rapid.Custom(func(t *rapid.T) checksMeasured {
		if rapid.IntRange(0, 4).Draw(t, "fails") == 0 {
			return checksMeasured{fails: true}
		}

		total := rapid.OneOf(rapid.Uint64Range(0, 100), rapid.Uint64()).Draw(t, "total")

		return checksMeasured{stat: DiskStat{
			Device: rapid.Uint64Range(1, 3).Draw(t, "device"),
			Total:  total,
			Avail:  rapid.OneOf(rapid.Uint64Range(0, total), rapid.Uint64()).Draw(t, "avail"),
		}}
	})
}

// The disk check reports, in path order, each unmeasured path and each filesystem
// once, as a percentage whose status never improves as it fills.
func TestDiskProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		measured := rapid.SliceOfN(genChecksMeasured(), 0, 6).Draw(t, "paths")
		paths := make([]DiskPath, len(measured))

		for i := range measured {
			paths[i] = DiskPath{ID: strconv.Itoa(i), Path: "/p" + strconv.Itoa(i)}
		}

		statfs := func(path string) (DiskStat, error) {
			i, _ := strconv.Atoi(strings.TrimPrefix(path, "/p"))

			if measured[i].fails {
				return DiskStat{}, errors.New("no such file or directory")
			}

			return measured[i].stat, nil
		}

		var (
			wantIDs []string
			devices = make(map[uint64]bool)
		)

		for i, m := range measured {
			if m.fails || !devices[m.stat.Device] {
				wantIDs = append(wantIDs, strconv.Itoa(i))
			}

			devices[m.stat.Device] = devices[m.stat.Device] || !m.fails
		}

		results := Disk(func() []DiskPath { return paths }, statfs).Run(context.Background())[KeyDisk]

		gotIDs := make([]string, len(results))
		for i, r := range results {
			gotIDs[i] = r.ComponentID
		}

		if diff := cmp.Diff(wantIDs, gotIDs, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("Disk(%+v) components mismatch (-want +got):\n%s", measured, diff)
		}

		for _, a := range results {
			used, ok := a.ObservedValue.(Float)

			switch {
			case !ok && a.Status != Warn:
				t.Fatalf("Disk(%+v) unmeasured %s = %+v, want a warning", measured, a.ComponentID, a)
			case ok && (used < 0 || used > 100):
				t.Fatalf("Disk(%+v) %s = %v%%, want 0 to 100", measured, a.ComponentID, used)
			}

			for _, b := range results {
				if other, ok2 := b.ObservedValue.(Float); ok && ok2 && used <= other && Worst(a.Status, b.Status) != b.Status {
					t.Fatalf("Disk(%+v) %s at %v%% is %s, but %s at %v%% is %s", measured, a.ComponentID, used, a.Status,
						b.ComponentID, other, b.Status)
				}
			}
		}
	})
}

func genChecksHistogram() *rapid.Generator[*metrics.Float64Histogram] {
	return rapid.Custom(func(t *rapid.T) *metrics.Float64Histogram {
		counts := rapid.SliceOfN(rapid.OneOf(rapid.Just(uint64(0)), rapid.Uint64Range(0, 5)), 2, 6).Draw(t, "counts")
		buckets := make([]float64, len(counts)+1)

		for i := range buckets {
			buckets[i] = float64(i) + float64(rapid.IntRange(0, 9).Draw(t, "fraction"))/10
		}

		if rapid.Bool().Draw(t, "unbounded below") {
			buckets[0] = math.Inf(-1)
		}

		if rapid.Bool().Draw(t, "unbounded above") {
			buckets[len(buckets)-1] = math.Inf(1)
		}

		return &metrics.Float64Histogram{Counts: counts, Buckets: buckets}
	})
}

// A percentile is a finite bucket bound within the buckets that hold samples,
// and grows with p.
func TestPercentileProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		h := genChecksHistogram().Draw(t, "histogram")
		// above 0: the 0th percentile is the first bucket's bound, held or not
		p, q := rapid.Float64Range(1e-9, 1).Draw(t, "p"), rapid.Float64Range(1e-9, 1).Draw(t, "q")
		p, q = min(p, q), max(p, q)

		low, high := percentile(h, p), percentile(h, q)
		if low > high {
			t.Fatalf("percentile(%+v, %v) = %v, more than percentile at %v = %v", h, p, low, q, high)
		}

		first := slices.IndexFunc(h.Counts, func(c uint64) bool { return c > 0 })
		if first < 0 {
			if low != 0 || high != 0 {
				t.Fatalf("percentile(%+v, %v and %v) = %v and %v, want 0 without samples", h, p, q, low, high)
			}

			return
		}

		last := len(h.Counts) - 1
		for h.Counts[last] == 0 {
			last--
		}

		for _, v := range []float64{low, high} {
			if math.IsInf(v, 0) || !slices.Contains(h.Buckets, v) || v < h.Buckets[first] || v > h.Buckets[last+1] {
				t.Fatalf("percentile(%+v, %v or %v) = %v, want a finite bound of buckets %d to %d", h, p, q, v, first, last)
			}
		}
	})
}

func TestPercentile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		h    *metrics.Float64Histogram
		p    float64
		want float64
	}{
		{name: "no buckets", h: &metrics.Float64Histogram{Buckets: []float64{0}}, p: 0.99, want: 0},
		{name: "no samples", h: &metrics.Float64Histogram{Counts: []uint64{0, 0}, Buckets: []float64{0, 1, 2}}, p: 0.99, want: 0},
		{name: "first bucket", h: &metrics.Float64Histogram{Counts: []uint64{9, 1}, Buckets: []float64{0, 1, 2}}, p: 0.5, want: 1},
		{name: "last bucket", h: &metrics.Float64Histogram{Counts: []uint64{9, 1}, Buckets: []float64{0, 1, 2}}, p: 0.99, want: 2},
		{name: "zeroth", h: &metrics.Float64Histogram{Counts: []uint64{0, 1}, Buckets: []float64{0, 1, 2}}, p: 0, want: 1},
		{
			name: "unbounded last bucket",
			h:    &metrics.Float64Histogram{Counts: []uint64{1, 1}, Buckets: []float64{0, 1, math.Inf(1)}},
			p:    0.99,
			want: 1,
		},
		{name: "past the samples", h: &metrics.Float64Histogram{Counts: []uint64{1, 1}, Buckets: []float64{0, 1, 2}}, p: 1.5, want: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := percentile(test.h, test.p); got != test.want {
				t.Errorf("percentile(%+v, %v) = %v, want %v", test.h, test.p, got, test.want)
			}
		})
	}
}

// A listing slower than a second warns, with how long it took.
func TestStoreSlow(t *testing.T) {
	t.Parallel()

	got := only(t, one(t, Store("bolt", func() error {
		time.Sleep(slowResponse + 10*time.Millisecond)

		return nil
	})), KeyStore)

	if took, ok := got.ObservedValue.(Float); !ok || took < Float(slowResponse.Milliseconds()) {
		t.Errorf("Store() of a slow store: ObservedValue = %#v, want at least %d ms", got.ObservedValue, slowResponse.Milliseconds())
	}

	if got.Status != Warn || !strings.HasPrefix(got.Output, "listing configs took ") {
		t.Errorf("Store() of a slow store = %s, %q; want %s, the time it took", got.Status, got.Output, Warn)
	}
}

// checksVersion is the minimega command that gives its version.
const checksVersion = "version"

// checksRunner answers each minimega command from a table, sleeping first
// when slow is set; a command not in the table fails.
type checksRunner struct {
	answers map[string][]*minicli.Response
	errs    map[string]error
	slow    time.Duration
}

func (r checksRunner) run(_ context.Context, command string) ([]*minicli.Response, error) {
	if command == checksVersion {
		time.Sleep(r.slow)
	}

	if err := r.errs[command]; err != nil {
		return nil, err
	}

	resps, ok := r.answers[command]
	if !ok {
		return nil, errors.New("unexpected command " + command)
	}

	return resps, nil
}

// The keys after the version warn, each on its own, when their command fails
// or its answer cannot be read.
func TestMinimegaPartialAnswers(t *testing.T) {
	t.Parallel()

	var (
		version   = []*minicli.Response{{Host: "head", Response: "minimega 2.9"}}
		namespace = []*minicli.Response{{Header: []string{"namespace"}, Tabular: [][]string{{"exp"}}}}
		mesh      = []*minicli.Response{{Header: []string{"size"}, Tabular: [][]string{{"1"}}}}
	)

	warn := func(id, componentType, output string) Result {
		return Result{
			ComponentID:       id,
			ComponentType:     componentType,
			Status:            Warn,
			AffectedEndpoints: minimegaEndpoints,
			Output:            output,
		}
	}

	tests := []struct {
		name   string
		runner checksRunner
		key    string
		want   Result
	}{
		{
			name: "namespace fails",
			runner: checksRunner{
				answers: map[string][]*minicli.Response{checksVersion: version, "mesh status": mesh},
				errs:    map[string]error{"namespace": errors.New("boom")},
			},
			key:  KeyMinimegaNamespaces,
			want: warn("minimega", TypeComponent, "listing namespaces: boom"),
		},
		{
			name: "mesh status fails",
			runner: checksRunner{
				answers: map[string][]*minicli.Response{checksVersion: version, "namespace": namespace},
				errs:    map[string]error{"mesh status": errors.New("boom")},
			},
			key:  KeyMeshNodes,
			want: warn("minimega-mesh", TypeSystem, "reading mesh status: boom"),
		},
		{
			name: "mesh status without a size",
			runner: checksRunner{answers: map[string][]*minicli.Response{
				checksVersion: version,
				"namespace":   namespace,
				"mesh status": {{Header: []string{"size"}, Tabular: [][]string{{"many"}}}},
			}},
			key:  KeyMeshNodes,
			want: warn("minimega-mesh", TypeSystem, "mesh status has no size"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := one(t, Minimega(test.runner.run))

			if diff := cmp.Diff(test.want, only(t, got, test.key)); diff != "" {
				t.Errorf("Minimega() %s mismatch (-want +got):\n%s", test.key, diff)
			}

			for _, key := range []string{KeyMinimegaTime, KeyMinimegaVersion, KeyMinimegaNamespaces, KeyMeshNodes} {
				if key != test.key && only(t, got, key).Status != Pass {
					t.Errorf("Minimega() %s = %+v, want it to pass", key, only(t, got, key))
				}
			}
		})
	}
}

// The version is the second word of "minimega <version> ...", the whole
// answer otherwise, and none for an empty answer.
func TestMinimegaVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		resps  []*minicli.Response
		want   any
		output string
	}{
		{name: "minimega version", resps: []*minicli.Response{{Response: "minimega 2.9 abc"}}, want: "2.9", output: "minimega 2.9 abc"},
		{name: "other format", resps: []*minicli.Response{{Response: " v2.9-dev \n"}}, want: "v2.9-dev", output: "v2.9-dev"},
		{name: "one word", resps: []*minicli.Response{{Response: "minimega"}}, want: "minimega", output: "minimega"},
		{name: "empty", resps: []*minicli.Response{{Response: "  "}}, want: nil, output: ""},
		{name: "no responses", resps: nil, want: nil, output: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			runner := checksRunner{answers: map[string][]*minicli.Response{
				checksVersion: test.resps,
				"namespace":   nil,
				"mesh status": {{Header: []string{"size"}, Tabular: [][]string{{"1"}}}},
			}}

			want := Result{
				ComponentID:   "minimega",
				ComponentType: TypeComponent,
				ObservedValue: test.want,
				Status:        Pass,
				Output:        test.output,
			}

			if diff := cmp.Diff(want, only(t, one(t, Minimega(runner.run)), KeyMinimegaVersion)); diff != "" {
				t.Errorf("Minimega() version mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A version answer slower than a second warns, with how long it took.
func TestMinimegaSlow(t *testing.T) {
	t.Parallel()

	runner := checksRunner{
		answers: map[string][]*minicli.Response{
			checksVersion: {{Response: "minimega 2.9"}},
			"namespace":   nil,
			"mesh status": {{Header: []string{"size"}, Tabular: [][]string{{"1"}}}},
		},
		slow: slowResponse + 10*time.Millisecond,
	}

	got := only(t, one(t, Minimega(runner.run)), KeyMinimegaTime)

	if took, ok := got.ObservedValue.(Float); !ok || took < Float(slowResponse.Milliseconds()) {
		t.Errorf("Minimega() of a slow minimega %s value = %#v, want at least %d ms",
			KeyMinimegaTime, got.ObservedValue, slowResponse.Milliseconds())
	}

	if got.Status != Warn || !strings.HasPrefix(got.Output, "minimega took ") {
		t.Errorf("Minimega() of a slow minimega %s = %+v, want a warning of the time it took", KeyMinimegaTime, got)
	}
}

func TestCountColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		resps []*minicli.Response
		want  int
	}{
		{name: "no responses", resps: nil, want: 0},
		{name: "no such column", resps: []*minicli.Response{{Header: []string{"other"}, Tabular: [][]string{{"a"}}}}, want: 0},
		{
			name: "distinct across nodes",
			resps: []*minicli.Response{
				{Header: []string{"namespace"}, Tabular: [][]string{{"a"}, {"b"}}},
				{Header: []string{"vlans", "namespace"}, Tabular: [][]string{{"1", "b"}, {"2", "c"}}},
			},
			want: 3,
		},
		{
			name: "short rows and a node without the column",
			resps: []*minicli.Response{
				{Header: []string{"other"}, Tabular: [][]string{{"x"}}},
				{Header: []string{"vlans", "namespace"}, Tabular: [][]string{{"1"}, {"2", "a"}}},
			},
			want: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := countColumn(test.resps, "namespace"); got != test.want {
				t.Errorf("countColumn(%s, namespace) = %d, want %d", test.name, got, test.want)
			}
		})
	}
}

func TestMeshSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		resps  []*minicli.Response
		want   meshInfo
		wantOK bool
	}{
		{name: "no responses", resps: nil},
		{name: "no rows", resps: []*minicli.Response{{Header: []string{"size"}}}},
		{
			name: "first node with rows",
			resps: []*minicli.Response{
				{Header: []string{"size"}},
				{Header: []string{"size", "degree", "peers"}, Tabular: [][]string{{"4", "2", "3"}}},
				{Header: []string{"size"}, Tabular: [][]string{{"9"}}},
			},
			want:   meshInfo{size: 4, degree: "2", peers: "3"},
			wantOK: true,
		},
		{
			name:  "row shorter than the header",
			resps: []*minicli.Response{{Header: []string{"degree", "size"}, Tabular: [][]string{{"2"}}}},
			want:  meshInfo{degree: "2"},
		},
		{
			name:  "size not a number",
			resps: []*minicli.Response{{Header: []string{"size", "peers"}, Tabular: [][]string{{"x", "1"}}}},
			want:  meshInfo{peers: "1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			info, ok := meshStatus(test.resps)
			if diff := cmp.Diff(test.want, info, cmp.AllowUnexported(meshInfo{})); diff != "" || ok != test.wantOK {
				t.Errorf("meshStatus(%s) = %t, want %t; mismatch (-want +got):\n%s", test.name, ok, test.wantOK, diff)
			}

			if size, ok := MeshSize(test.resps); size != test.want.size || ok != test.wantOK {
				t.Errorf("MeshSize(%s) = %d, %t; want %d, %t", test.name, size, ok, test.want.size, test.wantOK)
			}
		})
	}
}
