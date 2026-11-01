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

	"github.com/activeshadow/libminimega/minicli"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

	version := func(context.Context, string) (string, error) {
		return "qemu-img version 8.2.2 (Debian 1:8.2.2+ds-0ubuntu1)", nil
	}

	tests := map[string]struct {
		lookPath func(string) (string, error)
		want     Result
	}{
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
			lookPath: func(string) (string, error) { return "/usr/bin/qemu-img", nil },
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

			if diff := cmp.Diff(tc.want, only(t, one(t, QemuImg(tc.lookPath, version)), KeyQemuImg)); diff != "" {
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

func TestBrokerQueue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		queued    int
		status    Status
		endpoints []string
	}{
		{queued: 0, status: Pass},
		{queued: 74, status: Pass},
		{queued: 80, status: Warn, endpoints: []string{"/api/v1/ws"}},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d queued", tc.queued), func(t *testing.T) {
			t.Parallel()

			stats := func() BrokerStats { return BrokerStats{Clients: 3, Queued: tc.queued, Capacity: 100} }

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
					ObservedValue:     Float(tc.queued),
					ObservedUnit:      "percent",
					Status:            tc.status,
					AffectedEndpoints: tc.endpoints,
					Output:            fmt.Sprintf("%d of 100 broadcasts queued", tc.queued),
				}},
			}

			if diff := cmp.Diff(want, one(t, Broker(stats))); diff != "" {
				t.Errorf("Broker(%+v) mismatch (-want +got):\n%s", stats(), diff)
			}
		})
	}
}

func TestUptimeAndGoRuntime(t *testing.T) {
	t.Parallel()

	want := Result{ComponentType: TypeSystem, ObservedValue: int64(60), ObservedUnit: "s", Status: Pass}
	if diff := cmp.Diff(want, only(t, one(t, Uptime(time.Now().Add(-time.Minute))), KeyUptime)); diff != "" {
		t.Errorf("Uptime(a minute ago) mismatch (-want +got):\n%s", diff)
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
