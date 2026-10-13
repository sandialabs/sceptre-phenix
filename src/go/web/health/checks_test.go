package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"
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

	for used, want := range tests {
		statfs := func(string) (DiskStat, error) {
			return DiskStat{Device: 1, Total: 100, Avail: 100 - used}, nil
		}

		paths := func() []DiskPath { return []DiskPath{{ID: "files-dir", Path: "/phenix/images"}} }

		result := only(t, one(t, Disk(paths, statfs)), KeyDisk)

		if result.Status != want || result.ObservedValue != float64(used) || result.ComponentID != "files-dir" {
			t.Errorf("%d%% used: %+v, want %s", used, result, want)
		}
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

	results := one(t, Disk(paths, statfs))[KeyDisk]

	if len(results) != 2 || results[0].ComponentID != "files-dir" || results[1].ComponentID != "other" {
		t.Fatalf("results = %+v, want files-dir and other", results)
	}

	if results[1].Status != Warn || results[1].ObservedValue != nil {
		t.Errorf("unmeasured path = %+v, want a warning without a value", results[1])
	}
}

func TestQemuImg(t *testing.T) {
	t.Parallel()

	missing := func(string) (string, error) { return "", errors.New("not found") }
	found := func(string) (string, error) { return "/usr/bin/qemu-img", nil }
	version := func(context.Context, string) (string, error) {
		return "qemu-img version 8.2.2 (Debian 1:8.2.2+ds-0ubuntu1)", nil
	}

	result := only(t, one(t, QemuImg(missing, version)), KeyQemuImg)

	if result.Status != Warn || result.ObservedValue != nil || len(result.AffectedEndpoints) == 0 {
		t.Errorf("missing qemu-img: %+v, want a warning without a value", result)
	}

	result = only(t, one(t, QemuImg(found, version)), KeyQemuImg)

	if result.Status != Pass || result.ObservedValue != "8.2.2" {
		t.Errorf("qemu-img: %+v, want version 8.2.2", result)
	}
}

func TestWebShark(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	always := func() bool { return true }

	if result := only(t, one(t, WebShark(dir, always)), KeyWebShark); result.Status != Warn {
		t.Errorf("without index.html: %+v, want a warning", result)
	}

	if err := os.WriteFile(filepath.Join(dir, "index.html"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if result := only(t, one(t, WebShark(dir, always)), KeyWebShark); result.Status != Pass {
		t.Errorf("with index.html: %+v, want a pass", result)
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

	results := one(t, check)

	if r := only(t, results, KeyMinimegaVersion); r.Status != Pass || r.ObservedValue != "2.9" {
		t.Errorf("version = %+v, want 2.9", r)
	}

	if r := only(t, results, KeyMinimegaNamespaces); r.Status != Pass || r.ObservedValue != 2 {
		t.Errorf("namespaces = %+v, want 2", r)
	}

	if r := only(t, results, KeyMeshNodes); r.Status != Pass || r.ObservedValue != 3 {
		t.Errorf("mesh = %+v, want 3 nodes", r)
	}

	if r := only(t, results, KeyMinimegaTime); r.Status != Pass || r.ObservedUnit != "ms" {
		t.Errorf("response time = %+v, want a pass in ms", r)
	}

	// a node leaves
	mm.size = "2"

	if r := only(t, one(t, check), KeyMeshNodes); r.Status != Warn || r.ObservedValue != 2 ||
		!strings.Contains(r.Output, "mesh had 3 nodes, now 2") {
		t.Errorf("smaller mesh = %+v, want a warning", r)
	}
}

// When minimega does not answer, every key fails and none has a value, so
// each keeps its type across runs.
func TestMinimegaUnreachable(t *testing.T) {
	t.Parallel()

	mm := &fakeMinimega{err: errors.New("unable to dial")}
	results := one(t, Minimega(mm.run))

	for _, key := range []string{KeyMinimegaTime, KeyMinimegaVersion, KeyMinimegaNamespaces, KeyMeshNodes} {
		r := only(t, results, key)

		if r.Status != Fail || r.ObservedValue != nil || len(r.AffectedEndpoints) == 0 {
			t.Errorf("%s = %+v, want a failure without a value", key, r)
		}
	}

	if len(mm.commands) != 1 {
		t.Errorf("commands = %v, want only version", mm.commands)
	}
}

func TestStore(t *testing.T) {
	t.Parallel()

	r := only(t, one(t, Store("bolt", func() error { return nil })), KeyStore)

	if r.Status != Pass || r.ComponentID != "bolt" || r.ComponentType != TypeDatastore {
		t.Errorf("readable store = %+v, want a pass", r)
	}

	if _, ok := r.ObservedValue.(float64); !ok {
		t.Errorf("readable store value = %#v, want milliseconds", r.ObservedValue)
	}

	r = only(t, one(t, Store("etcd", func() error { return errors.New("connection refused") })), KeyStore)

	if r.Status != Fail || r.ObservedValue != nil || !strings.Contains(r.Output, "connection refused") {
		t.Errorf("unreadable store = %+v, want a failure without a value", r)
	}
}

func TestBrokerQueue(t *testing.T) {
	t.Parallel()

	tests := map[int]Status{0: Pass, 74: Pass, 80: Warn}

	for queued, want := range tests {
		stats := func() BrokerStats { return BrokerStats{Clients: 3, Queued: queued, Capacity: 100} }
		results := one(t, Broker(stats))

		if r := only(t, results, KeyWebsocketQueue); r.Status != want || r.ObservedValue != float64(queued) {
			t.Errorf("%d queued: %+v, want %s", queued, r, want)
		}

		if r := only(t, results, KeyWebsocketClients); r.ObservedValue != 3 {
			t.Errorf("clients = %+v, want 3", r)
		}
	}
}

func TestUptimeAndGoRuntime(t *testing.T) {
	t.Parallel()

	if r := only(t, one(t, Uptime(time.Now().Add(-time.Minute))), KeyUptime); r.ObservedValue != int64(60) {
		t.Errorf("uptime = %+v, want 60 s", r)
	}

	results := one(t, GoRuntime())

	if r := only(t, results, KeyGoroutines); r.ObservedValue == nil || r.ObservedValue.(uint64) == 0 {
		t.Errorf("goroutines = %+v, want some", r)
	}

	for _, key := range []string{KeyHeapObjects, KeyGCPause} {
		if r := only(t, results, key); r.Status != Pass || r.ObservedValue == nil {
			t.Errorf("%s = %+v, want a value", key, r)
		}
	}
}
