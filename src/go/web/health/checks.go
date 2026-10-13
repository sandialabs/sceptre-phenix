package health

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/metrics"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/activeshadow/libminimega/minicli"
)

// Thresholds, budgets and cache lifetimes of the checks.
const (
	slowResponse = time.Second

	diskWarnPercent = 90
	diskFailPercent = 98

	queueWarnPercent = 75

	storeTTL, storeTimeout       = 2 * time.Second, 3 * time.Second
	minimegaTTL, minimegaTimeout = 5 * time.Second, 3 * time.Second
	qemuImgTTL, qemuImgTimeout   = time.Minute, 2 * time.Second
	websharkTTL, websharkTimeout = 30 * time.Second, time.Second
	diskTTL, diskTimeout         = 10 * time.Second, 2 * time.Second
)

// Check keys.
const (
	KeyUptime             = "uptime"
	KeyGoroutines         = "go:goroutines"
	KeyHeapObjects        = "go:heapObjectsBytes"
	KeyGCPause            = "go:gcPauseP99"
	KeyStore              = "store:responseTime"
	KeyMinimegaTime       = "minimega:responseTime"
	KeyMinimegaVersion    = "minimega:version"
	KeyMinimegaNamespaces = "minimega:namespaces"
	KeyMeshNodes          = "mesh:nodes"
	KeyQemuImg            = "qemu-img:version"
	KeyWebShark           = "webshark:files"
	KeyDisk               = "disk:utilization"
	KeyWebsocketClients   = "websocket:clients"
	KeyWebsocketQueue     = "websocket:queue"
)

// minimegaEndpoints are the routes that need minimega.
//
//nolint:gochecknoglobals // constant list
var minimegaEndpoints = []string{
	"/api/v1/experiments/{name}/start",
	"/api/v1/experiments/{exp}/vms",
	"/api/v1/vms",
	"/api/v1/hosts",
	"/api/v1/disks",
}

// millis is d in milliseconds, to the microsecond.
func millis(d time.Duration) Float {
	return Float(d.Microseconds()) / 1000 //nolint:mnd // microseconds per millisecond
}

// round1 rounds f to one decimal place.
func round1(f float64) float64 {
	return math.Round(f*10) / 10 //nolint:mnd // one decimal place
}

// Uptime reports the seconds since start.
func Uptime(start time.Time) Check {
	return Check{ //nolint:exhaustruct // runs per request
		Name: "uptime",
		Keys: []Key{{Name: KeyUptime, ComponentID: "", ComponentType: TypeSystem}},
		Run: func(context.Context) map[string][]Result {
			return map[string][]Result{KeyUptime: {{ //nolint:exhaustruct // passes
				ComponentType: TypeSystem,
				ObservedValue: int64(time.Since(start).Seconds()),
				ObservedUnit:  "s",
				Status:        Pass,
			}}}
		},
	}
}

// GoRuntime reports the number of goroutines, the bytes of live heap objects
// and the 99th percentile GC pause, from runtime/metrics, which, unlike
// [runtime.ReadMemStats], does not stop the world.
func GoRuntime() Check {
	return Check{ //nolint:exhaustruct // runs per request
		Name: "go",
		Keys: []Key{
			{Name: KeyGoroutines, ComponentID: "", ComponentType: TypeSystem},
			{Name: KeyHeapObjects, ComponentID: "", ComponentType: TypeSystem},
			{Name: KeyGCPause, ComponentID: "", ComponentType: TypeSystem},
		},
		Run: func(context.Context) map[string][]Result {
			samples := []metrics.Sample{
				{Name: "/sched/goroutines:goroutines"},       //nolint:exhaustruct // filled by Read
				{Name: "/memory/classes/heap/objects:bytes"}, //nolint:exhaustruct // filled by Read
				{Name: "/sched/pauses/total/gc:seconds"},     //nolint:exhaustruct // filled by Read
			}

			metrics.Read(samples)

			results := make(map[string][]Result, len(samples))

			report := func(key, unit string, value any) {
				results[key] = []Result{{ //nolint:exhaustruct // passes
					ComponentType: TypeSystem,
					ObservedValue: value,
					ObservedUnit:  unit,
					Status:        Pass,
				}}
			}

			if samples[0].Value.Kind() == metrics.KindUint64 {
				report(KeyGoroutines, "goroutines", samples[0].Value.Uint64())
			}

			if samples[1].Value.Kind() == metrics.KindUint64 {
				report(KeyHeapObjects, "bytes", samples[1].Value.Uint64())
			}

			if samples[2].Value.Kind() == metrics.KindFloat64Histogram {
				report(KeyGCPause, "ms", Float(percentile(samples[2].Value.Float64Histogram(), 0.99)*1000)) //nolint:mnd // ms
			}

			return results
		},
	}
}

// percentile estimates the p-th quantile of h, as the upper bound of the
// bucket holding it (or its lower bound, for the unbounded last bucket).
func percentile(h *metrics.Float64Histogram, p float64) float64 {
	var total uint64

	for _, c := range h.Counts {
		total += c
	}

	if total == 0 {
		return 0
	}

	var (
		target = uint64(math.Ceil(p * float64(total)))
		seen   uint64
	)

	for i, c := range h.Counts {
		seen += c

		if seen < target {
			continue
		}

		if upper := h.Buckets[i+1]; !math.IsInf(upper, 1) {
			return upper
		}

		return h.Buckets[i]
	}

	return h.Buckets[len(h.Buckets)-1]
}

// Store reports how long the store takes to list, through list: it fails
// when the listing fails, and warns when it takes over a second. The
// component is the store's kind ("bolt" or "etcd"), never its location.
func Store(component string, list func() error) Check {
	key := Key{Name: KeyStore, ComponentID: component, ComponentType: TypeDatastore}

	return Check{ //nolint:exhaustruct // always enabled
		Name:    "store",
		Keys:    []Key{key},
		Timeout: storeTimeout,
		TTL:     storeTTL,
		Run: func(context.Context) map[string][]Result {
			start := time.Now()
			err := list()
			took := time.Since(start)

			result := Result{ //nolint:exhaustruct // filled in below
				ComponentID:   component,
				ComponentType: TypeDatastore,
				Status:        Pass,
			}

			switch {
			case err != nil:
				result.Status = Fail
				result.Output = "listing configs: " + err.Error()
			case took > slowResponse:
				result.Status = Warn
				result.Output = fmt.Sprintf("listing configs took %s", took.Round(time.Millisecond))
			}

			if err == nil {
				result.ObservedValue, result.ObservedUnit = millis(took), "ms"
			}

			return map[string][]Result{KeyStore: {result}}
		},
	}
}

// MinimegaRunner sends one minimega command, outside any namespace, and
// returns every node's responses, or an error when the command could not be
// sent or any node answered with one.
type MinimegaRunner func(ctx context.Context, command string) ([]*minicli.Response, error)

// Minimega reports how long minimega takes to give its version, the version,
// the number of namespaces and the number of nodes in the mesh. It fails when
// minimega does not answer, and warns when it is slow, when the namespaces or
// mesh cannot be listed, or when the mesh has fewer nodes than it has had
// since the server started (a node left or cannot be reached).
//
//nolint:funlen // one check of four keys
func Minimega(run MinimegaRunner) Check {
	var (
		mu        sync.Mutex
		highWater int
	)

	keys := []Key{
		{Name: KeyMinimegaTime, ComponentID: "minimega", ComponentType: TypeComponent},
		{Name: KeyMinimegaVersion, ComponentID: "minimega", ComponentType: TypeComponent},
		{Name: KeyMinimegaNamespaces, ComponentID: "minimega", ComponentType: TypeComponent},
		{Name: KeyMeshNodes, ComponentID: "minimega-mesh", ComponentType: TypeSystem},
	}

	result := func(key Key, status Status) Result {
		r := Result{ //nolint:exhaustruct // filled in by the caller
			ComponentID:   key.ComponentID,
			ComponentType: key.ComponentType,
			Status:        status,
		}

		if status != Pass {
			r.AffectedEndpoints = minimegaEndpoints
		}

		return r
	}

	return Check{ //nolint:exhaustruct // always enabled
		Name:    "minimega",
		Keys:    keys,
		Timeout: minimegaTimeout,
		TTL:     minimegaTTL,
		Run: func(ctx context.Context) map[string][]Result {
			results := make(map[string][]Result, len(keys))

			start := time.Now()
			resps, err := run(ctx, "version")
			took := time.Since(start)

			if err != nil {
				for _, key := range keys {
					r := result(key, Fail)
					r.Output = "minimega did not answer: " + err.Error()
					results[key.Name] = []Result{r}
				}

				return results
			}

			timing := result(keys[0], Pass)
			timing.ObservedValue, timing.ObservedUnit = millis(took), "ms"

			if took > slowResponse {
				timing.Status = Warn
				timing.Output = fmt.Sprintf("minimega took %s to give its version", took.Round(time.Millisecond))
			}

			results[KeyMinimegaTime] = []Result{timing}

			ver := result(keys[1], Pass)

			if len(resps) > 0 {
				full := strings.TrimSpace(resps[0].Response)
				fields := strings.Fields(full)

				if len(fields) > 1 && fields[0] == "minimega" {
					ver.ObservedValue = fields[1]
				} else if full != "" {
					ver.ObservedValue = full
				}

				ver.Output = full
			}

			results[KeyMinimegaVersion] = []Result{ver}

			ns := result(keys[2], Pass)

			if resps, err := run(ctx, "namespace"); err != nil {
				ns = result(keys[2], Warn)
				ns.Output = "listing namespaces: " + err.Error()
			} else {
				ns.ObservedValue, ns.ObservedUnit = countColumn(resps, "namespace"), "namespaces"
			}

			results[KeyMinimegaNamespaces] = []Result{ns}

			mesh := result(keys[3], Pass)

			if resps, err := run(ctx, "mesh status"); err != nil {
				mesh = result(keys[3], Warn)
				mesh.Output = "reading mesh status: " + err.Error()
			} else if status, ok := meshStatus(resps); !ok {
				mesh = result(keys[3], Warn)
				mesh.Output = "mesh status has no size"
			} else {
				mu.Lock()
				had := highWater
				highWater = max(highWater, status.size)
				mu.Unlock()

				mesh.ObservedValue, mesh.ObservedUnit = status.size, "nodes"
				mesh.Output = fmt.Sprintf("degree %s, peers %s", status.degree, status.peers)

				if status.size < had {
					mesh = result(keys[3], Warn)
					mesh.ObservedValue, mesh.ObservedUnit = status.size, "nodes"
					mesh.Output = fmt.Sprintf(
						"mesh had %d nodes, now %d; degree %s, peers %s",
						had, status.size, status.degree, status.peers,
					)
				}
			}

			results[KeyMeshNodes] = []Result{mesh}

			return results
		},
	}
}

// countColumn counts the distinct values of a column in tabular responses.
func countColumn(resps []*minicli.Response, column string) int {
	seen := make(map[string]struct{})

	for _, resp := range resps {
		i := slices.Index(resp.Header, column)
		if i < 0 {
			continue
		}

		for _, row := range resp.Tabular {
			if i < len(row) {
				seen[row[i]] = struct{}{}
			}
		}
	}

	return len(seen)
}

type meshInfo struct {
	size          int
	degree, peers string
}

// meshStatus reads the local node's `mesh status`.
func meshStatus(resps []*minicli.Response) (meshInfo, bool) {
	for _, resp := range resps {
		if len(resp.Tabular) == 0 {
			continue
		}

		var (
			row  = resp.Tabular[0]
			info meshInfo
			ok   bool
		)

		for i, column := range resp.Header {
			if i >= len(row) {
				break
			}

			switch column {
			case "size":
				if n, err := strconv.Atoi(row[i]); err == nil {
					info.size, ok = n, true
				}
			case "degree":
				info.degree = row[i]
			case "peers":
				info.peers = row[i]
			}
		}

		return info, ok
	}

	return meshInfo{}, false
}

// QemuImg reports the version of qemu-img, which phenix inspects disk images
// with. Without it phenix falls back to asking minimega, so a missing or
// broken qemu-img only warns.
func QemuImg(lookPath func(string) (string, error), version func(ctx context.Context, path string) (string, error)) Check {
	key := Key{Name: KeyQemuImg, ComponentID: "qemu-img", ComponentType: TypeComponent}

	return Check{ //nolint:exhaustruct // always enabled
		Name:    "qemu-img",
		Keys:    []Key{key},
		Timeout: qemuImgTimeout,
		TTL:     qemuImgTTL,
		Run: func(ctx context.Context) map[string][]Result {
			result := Result{ //nolint:exhaustruct // filled in below
				ComponentID:   key.ComponentID,
				ComponentType: key.ComponentType,
				Status:        Pass,
			}

			path, err := lookPath("qemu-img")
			if err != nil {
				result.Status = Warn
				result.AffectedEndpoints = []string{"/api/v1/disks"}
				result.Output = "qemu-img not found; phenix inspects disk images through minimega instead"

				return map[string][]Result{KeyQemuImg: {result}}
			}

			out, err := version(ctx, path)
			if err != nil {
				result.Status = Warn
				result.AffectedEndpoints = []string{"/api/v1/disks"}
				result.Output = fmt.Sprintf("running %s --version: %v", path, err)

				return map[string][]Result{KeyQemuImg: {result}}
			}

			line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")

			// "qemu-img version 8.2.2 (Debian 1:8.2.2+ds-0ubuntu1)"
			if fields := strings.Fields(line); len(fields) > 2 && fields[1] == "version" {
				result.ObservedValue = fields[2]
			} else if line != "" {
				result.ObservedValue = line
			}

			result.Output = path + ": " + line

			return map[string][]Result{KeyQemuImg: {result}}
		},
	}
}

// WebShark reports whether the WebShark build the server found at startup is
// still there. WebShark is optional, so a missing build only warns.
func WebShark(dir string, enabled func() bool) Check {
	key := Key{Name: KeyWebShark, ComponentID: "webshark", ComponentType: TypeComponent}

	return Check{
		Name:    "webshark",
		Keys:    []Key{key},
		Timeout: websharkTimeout,
		TTL:     websharkTTL,
		Enabled: enabled,
		Run: func(context.Context) map[string][]Result {
			result := Result{ //nolint:exhaustruct // filled in below
				ComponentID:   key.ComponentID,
				ComponentType: key.ComponentType,
				Status:        Pass,
			}

			index := filepath.Join(dir, "index.html")

			if info, err := os.Stat(index); err != nil {
				result.Status = Warn
				result.AffectedEndpoints = []string{"/webshark/"}
				result.Output = "WebShark build missing: " + err.Error()
			} else if !info.Mode().IsRegular() {
				result.Status = Warn
				result.AffectedEndpoints = []string{"/webshark/"}
				result.Output = index + " is not a file"
			}

			return map[string][]Result{KeyWebShark: {result}}
		},
	}
}

// DiskPath is a directory whose filesystem the disk check measures, with the
// componentId it is reported under.
type DiskPath struct {
	ID   string
	Path string
}

// DiskStat is the size of the filesystem holding a path.
type DiskStat struct {
	// Device identifies the filesystem, so two paths on one are measured once.
	Device uint64
	// Total and Avail are the filesystem's size, and the space unprivileged
	// users may still write, in bytes.
	Total, Avail uint64
}

// Disk reports the percentage used of each distinct filesystem holding the
// paths (listed again on each run). It warns at 90% and fails at 98%, and
// warns about a path it cannot measure.
func Disk(paths func() []DiskPath, statfs func(string) (DiskStat, error)) Check {
	return Check{ //nolint:exhaustruct // always enabled
		Name:    "disk",
		Keys:    []Key{{Name: KeyDisk, ComponentID: "", ComponentType: TypeSystem}},
		Timeout: diskTimeout,
		TTL:     diskTTL,
		Run: func(context.Context) map[string][]Result {
			var (
				results []Result
				devices = make(map[uint64]bool)
			)

			for _, p := range paths() {
				result := Result{ //nolint:exhaustruct // filled in below
					ComponentID:   p.ID,
					ComponentType: TypeSystem,
					Status:        Pass,
				}

				st, err := statfs(p.Path)
				if err != nil {
					result.Status = Warn
					result.Output = fmt.Sprintf("measuring %s: %v", p.Path, err)
					results = append(results, result)

					continue
				}

				if devices[st.Device] {
					continue
				}

				devices[st.Device] = true

				var used float64
				if st.Total > 0 {
					used = round1(float64(st.Total-min(st.Avail, st.Total)) / float64(st.Total) * 100) //nolint:mnd // percent
				}

				result.ObservedValue, result.ObservedUnit = Float(used), "percent"
				result.Output = fmt.Sprintf("%s: %d bytes free", p.Path, st.Avail)

				switch {
				case used >= diskFailPercent:
					result.Status = Fail
				case used >= diskWarnPercent:
					result.Status = Warn
				}

				results = append(results, result)
			}

			return map[string][]Result{KeyDisk: results}
		},
	}
}

// BrokerStats are the websocket broker's clients and the broadcasts queued
// for it.
type BrokerStats struct {
	Clients  int
	Queued   int
	Capacity int
}

// Broker reports the websocket clients connected and how full the broker's
// broadcast queue is. A queue three-quarters full warns: the broker is
// falling behind, and publishers will soon block.
func Broker(stats func() BrokerStats) Check {
	return Check{ //nolint:exhaustruct // runs per request
		Name: "websocket",
		Keys: []Key{
			{Name: KeyWebsocketClients, ComponentID: "broker", ComponentType: TypeSystem},
			{Name: KeyWebsocketQueue, ComponentID: "broker", ComponentType: TypeSystem},
		},
		Run: func(context.Context) map[string][]Result {
			s := stats()

			queue := Result{ //nolint:exhaustruct // filled in below
				ComponentID:   "broker",
				ComponentType: TypeSystem,
				ObservedUnit:  "percent",
				Status:        Pass,
			}

			var percent float64
			if s.Capacity > 0 {
				percent = round1(float64(s.Queued) / float64(s.Capacity) * 100) //nolint:mnd // percent
			}

			queue.ObservedValue = Float(percent)
			queue.Output = fmt.Sprintf("%d of %d broadcasts queued", s.Queued, s.Capacity)

			if percent >= queueWarnPercent {
				queue.Status = Warn
				queue.AffectedEndpoints = []string{"/api/v1/ws"}
			}

			return map[string][]Result{
				KeyWebsocketClients: {{ //nolint:exhaustruct // passes
					ComponentID:   "broker",
					ComponentType: TypeSystem,
					ObservedValue: s.Clients,
					ObservedUnit:  "clients",
					Status:        Pass,
				}},
				KeyWebsocketQueue: {queue},
			}
		},
	}
}
