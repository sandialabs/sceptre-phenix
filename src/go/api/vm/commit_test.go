package vm_test

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/disk"
	"phenix/api/vm"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm/mmtest"
)

// commitFiles records the cluster file copies of CommitToDisk. Any other
// file.ClusterFiles method panics through the nil embedded interface.
type commitFiles struct {
	file.ClusterFiles

	mu     sync.Mutex
	copies []string // "path -> dest"
	syncs  []string
}

func (f *commitFiles) CopyFile(path, dest string, _ file.CopyStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.copies = append(f.copies, path+" -> "+dest)

	return nil
}

func (f *commitFiles) SyncFile(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.syncs = append(f.syncs, path)

	return nil
}

// useQemuImg puts a fake qemu-img first on PATH until the test ends. The fake
// fails when its first argument is failOn. It returns a function that lists
// the arguments of each run.
func useQemuImg(t *testing.T, failOn string) func() []string {
	t.Helper()

	dir := t.TempDir()
	log := filepath.Join(dir, "runs")

	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"if [ \"$1\" = '" + failOn + "' ]; then echo 'qemu-img failed' >&2; exit 1; fi\n"

	if err := os.WriteFile(filepath.Join(dir, "qemu-img"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing fake qemu-img: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return func() []string {
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}

		return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	}
}

// commitTree makes a phenix base directory with vm1's image (a snapshot) and
// the image it is based on, until the test ends. It returns the base.
func commitTree(t *testing.T) string {
	t.Helper()

	base := t.TempDir()

	original := common.PhenixBase
	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore test configuration

	common.PhenixBase = base //nolint:reassign // images go to a temporary tree

	for name, data := range map[string]string{"vm1.qc2": "vm1 snapshot", "base.qc2": "base image"} {
		if err := os.MkdirAll(base+"/images", 0o750); err != nil {
			t.Fatalf("creating images directory: %v", err)
		}

		if err := os.WriteFile(base+"/images/"+name, []byte(data), 0o600); err != nil {
			t.Fatalf("writing image %s: %v", name, err)
		}
	}

	return base
}

// commitMinimega answers as minimega does for CommitToDisk: chain for the
// image's backing chain and rows for the VM's info. The VM runs until it gets
// the powerdown. A command with one of the prefixes in fail fails.
func commitMinimega(chain string, fail []string, rows [][]string) mmtest.Handler {
	var powerdown atomic.Bool

	return func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case slices.ContainsFunc(fail, func(prefix string) bool { return strings.HasPrefix(cmd.Base, prefix) }):
			return []*minicli.Response{mmtest.Error("compute1", "minimega failed")}
		case strings.HasPrefix(cmd.Base, "shell qemu-img info"):
			return []*minicli.Response{mmtest.Text("head", chain)}
		case cmd.Base == "vm info":
			return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "name", "id", "state"}, rows...)}
		case cmd.Base == "vm info summary" && powerdown.Load():
			return stateRow("QUIT")
		case cmd.Base == "vm info summary":
			return stateRow("RUNNING")
		case cmd.Base == systemPowerdown:
			powerdown.Store(true)
		}

		return nil
	}
}

// filesIn lists the names of the files in dir, none for a dir that does not
// exist.
func filesIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reading %s: %v", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

// readFile is the content of path, or the error reading it.
func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}

	return string(data)
}

// CommitToDisk copies the VM's base image to the new image, pulls the VM's
// snapshot from its node, rebases the snapshots onto the copy, commits them
// into it and starts the VM again. It removes its copies of the snapshots, and
// after a failure it starts a VM that it stopped.
//
//nolint:paralleltest // replaces the store, minimega, cluster files, PATH and the phenix base directory
func TestCommitToDisk(t *testing.T) {
	stamped := regexp.MustCompile(`^vm1_\d{14}\.qc2$`)

	tests := []struct {
		name    string
		vm, out string
		rows    [][]string // minimega's vm info rows
		failOn  string     // the qemu-img subcommand that fails
		mmFail  []string   // the prefixes of the minimega commands that fail
		wantErr bool
		// what CommitToDisk ran: the minimega commands, then qemu-img's runs,
		// for the new image's path
		wantSent, wantRuns func(base, out string) []string
	}{
		{
			name: "commits a VM on another node into a copy of its base image",
			vm:   "vm1", out: "new.qc2",
			rows: [][]string{{"compute1", "vm1", "5", "QUIT"}},
		},
		{
			name: "names the new image after the topology's image",
			vm:   "vm1",
			rows: [][]string{{"compute1", "vm1", "5", "QUIT"}},
		},
		{
			name: "reports a VM minimega does not have",
			vm:   "vm1", out: "new.qc2", wantErr: true,
			wantSent: func(base, _ string) []string { return chainQueries(base)[:3] },
			wantRuns: func(string, string) []string { return nil },
		},
		{
			name: "does not sync or start a VM it did not stop after a failed commit",
			vm:   "vm1", out: "new.qc2", failOn: "commit", wantErr: true,
			rows: [][]string{{"compute1", "vm1", "5", "QUIT"}},
			wantSent: func(base, _ string) []string {
				return append(chainQueries(base), removeCopy(base))
			},
		},
		{
			name: "stops a running VM and starts it again after the commit",
			vm:   "vm1", out: "new.qc2",
			rows: [][]string{{"compute1", "vm1", "5", "RUNNING"}},
			wantSent: func(base, _ string) []string {
				return slices.Concat(stopQueries(base), copyQueries(base), []string{"vm start vm1", removeCopy(base)})
			},
		},
		{
			name: "starts the VM it stopped again after a failed commit",
			vm:   "vm1", out: "new.qc2", failOn: "commit", wantErr: true,
			rows: [][]string{{"compute1", "vm1", "5", "RUNNING"}},
			wantSent: func(base, _ string) []string {
				return slices.Concat(stopQueries(base), copyQueries(base), []string{"vm start vm1", removeCopy(base)})
			},
		},
		{
			name: "starts the VM it stopped again after a failed rebase",
			vm:   "vm1", out: "new.qc2", failOn: "rebase", wantErr: true,
			rows: [][]string{{"compute1", "vm1", "5", "RUNNING"}},
			wantSent: func(base, _ string) []string {
				return slices.Concat(stopQueries(base), copyQueries(base), []string{"vm start vm1", removeCopy(base)})
			},
		},
		{
			name: "starts the VM it stopped again after a failed snapshot copy",
			vm:   "vm1", out: "new.qc2", mmFail: []string{"mesh send compute1 shell cp"}, wantErr: true,
			rows: [][]string{{"compute1", "vm1", "5", "RUNNING"}},
			wantSent: func(base, _ string) []string {
				return slices.Concat(stopQueries(base), copyQueries(base), []string{"vm start vm1", removeCopy(base)})
			},
			wantRuns: func(string, string) []string { return nil },
		},
		{
			name: "reports a failed start of the VM it stopped after a failed commit",
			vm:   "vm1", out: "new.qc2", failOn: "commit", mmFail: []string{"vm start"}, wantErr: true,
			rows: [][]string{{"compute1", "vm1", "5", "RUNNING"}},
			wantSent: func(base, _ string) []string {
				return slices.Concat(stopQueries(base), copyQueries(base), []string{"vm start vm1", removeCopy(base)})
			},
		},
		{
			name: "starts the VM again after a failed shutdown",
			vm:   "vm1", out: "new.qc2", mmFail: []string{"vm qmp", "vm kill"}, wantErr: true,
			rows: [][]string{{"compute1", "vm1", "5", "RUNNING"}},
			wantSent: func(base, _ string) []string {
				return append(stopQueries(base)[:6], "vm kill vm1", "vm start vm1")
			},
			wantRuns: func(string, string) []string { return nil },
		},
		{
			name: "rejects a VM the topology does not have", vm: "vm2", wantErr: true,
			wantSent: func(string, string) []string { return nil },
			wantRuns: func(string, string) []string { return nil },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := commitTree(t)

			node := vmNode("vm1")
			hardware := node["hardware"].(map[string]any) //nolint:forcetypeassert // vmNode's hardware
			hardware["drives"] = []map[string]any{{"image": "vm1.qc2"}}
			useExperiment(t, startTime, node)

			files := &commitFiles{}

			originalFiles := file.DefaultClusterFiles
			t.Cleanup(func() { file.DefaultClusterFiles = originalFiles }) //nolint:reassign // restore test double

			file.DefaultClusterFiles = files //nolint:reassign // install test double

			runs := useQemuImg(t, test.failOn)
			chain := fmt.Sprintf(`[{"filename":%q,"backing-filename":%q},{"filename":%q}]`,
				base+"/images/vm1.qc2", base+"/images/base.qc2", base+"/images/base.qc2")

			received := useMinimega(t, commitMinimega(chain, test.mmFail, test.rows))

			got, err := vm.CommitToDisk(t.Context(), testExp, test.vm, test.out, nil, vm.WithCommitShutdownPoll(time.Millisecond))

			if gotErr := err != nil; gotErr != test.wantErr {
				t.Fatalf("CommitToDisk(%q, %q) error = %v, want error %t", test.vm, test.out, err, test.wantErr)
			}

			out := test.out
			if !test.wantErr {
				out = got

				if test.out == "" && !stamped.MatchString(got) {
					t.Errorf("CommitToDisk(%q, %q) = %q, want vm1_<time>.qc2", test.vm, test.out, got)
				} else if test.out != "" && got != test.out {
					t.Errorf("CommitToDisk(%q, %q) = %q, want %q", test.vm, test.out, got, test.out)
				}
			}

			wantSent, wantRuns := test.wantSent, test.wantRuns
			if wantSent == nil {
				wantSent = func(base, _ string) []string {
					return append(chainQueries(base), "vm start vm1", removeCopy(base))
				}
			}

			if wantRuns == nil {
				wantRuns = func(base, out string) []string { return commitRuns(base, out, test.failOn) }
			}

			if diff := cmp.Diff(wantSent(base, out), mmtest.Bases(received()), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("CommitToDisk(%q, %q) minimega commands mismatch (-want +got):\n%s", test.vm, test.out, diff)
			}

			if diff := cmp.Diff(wantRuns(base, out), runs(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("CommitToDisk(%q, %q) qemu-img runs mismatch (-want +got):\n%s", test.vm, test.out, diff)
			}

			if left := filesIn(t, base+"/images/"+testExp+"/tmp"); len(left) != 0 {
				t.Errorf("CommitToDisk(%q, %q) left copies %q, want none", test.vm, test.out, left)
			}

			if test.wantErr {
				return
			}

			files.mu.Lock()
			copies, syncs := files.copies, files.syncs
			files.mu.Unlock()

			headnode, _ := os.Hostname()
			if diff := cmp.Diff([]string{testExp + "/tmp/vm1.qc2 -> " + headnode}, copies); diff != "" {
				t.Errorf("CommitToDisk(%q, %q) file copies mismatch (-want +got):\n%s", test.vm, test.out, diff)
			}

			if diff := cmp.Diff([]string{out}, syncs); diff != "" {
				t.Errorf("CommitToDisk(%q, %q) file syncs mismatch (-want +got):\n%s", test.vm, test.out, diff)
			}

			if got := readFile(base + "/images/" + out); got != "base image" {
				t.Errorf("CommitToDisk(%q, %q) left %s = %q, want %q", test.vm, test.out, out, got, "base image")
			}
		})
	}
}

// CommitToDisk reports the size of the new image as a part of its size plus
// the size of the headnode's copy of the snapshot, from 0.8 to 1. It sends no
// report after it returns.
//
//nolint:paralleltest // replaces the store, minimega, cluster files, PATH and the phenix base directory
func TestCommitToDiskProgress(t *testing.T) {
	base := commitTree(t)
	tmp := base + "/images/" + testExp + "/tmp"

	node := vmNode("vm1")
	hardware := node["hardware"].(map[string]any) //nolint:forcetypeassert // vmNode's hardware
	hardware["drives"] = []map[string]any{{"image": "vm1.qc2"}}
	useExperiment(t, startTime, node)

	originalFiles := file.DefaultClusterFiles
	t.Cleanup(func() { file.DefaultClusterFiles = originalFiles }) //nolint:reassign // restore test double

	file.DefaultClusterFiles = &commitFiles{} //nolint:reassign // install test double

	// The fake commit grows the new image (10 bytes, a copy of base.qc2) by 10
	// bytes, then waits for a report of the grown image, for at most 5 s.
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	script := "#!/bin/sh\n[ \"$1\" = commit ] || exit 0\nprintf '0123456789' >> \"$3\"\n" +
		"i=0\nwhile [ ! -e '" + release + "' ] && [ $i -lt 500 ]; do sleep 0.01; i=$((i+1)); done\n"

	if err := os.WriteFile(filepath.Join(dir, "qemu-img"), []byte(script), 0o700); err != nil {
		t.Fatalf("writing fake qemu-img: %v", err)
	}

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	chain := fmt.Sprintf(`[{"filename":%q,"backing-filename":%q},{"filename":%q}]`,
		base+"/images/vm1.qc2", base+"/images/base.qc2", base+"/images/base.qc2")

	useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case strings.HasPrefix(cmd.Base, "shell qemu-img info"):
			return []*minicli.Response{mmtest.Text("head", chain)}
		case cmd.Base == "vm info":
			return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "name", "id", "state"},
				[]string{"compute1", "vm1", "5", "QUIT"})}
		case strings.HasPrefix(cmd.Base, "mesh send compute1 shell cp "):
			// the headnode's copy of the snapshot (30 bytes), which the commit adds to the new image
			if err := os.MkdirAll(tmp, 0o750); err != nil {
				return []*minicli.Response{mmtest.Error("compute1", err.Error())}
			}

			if err := os.WriteFile(tmp+"/vm1.qc2", []byte(strings.Repeat("s", 30)), 0o600); err != nil {
				return []*minicli.Response{mmtest.Error("compute1", err.Error())}
			}
		}

		return nil
	})

	var (
		mu             sync.Mutex
		progress       []float64
		returned, late atomic.Bool
		releaseOnce    sync.Once
		releaseErr     error
	)

	_, err := vm.CommitToDisk(t.Context(), testExp, "vm1", "new.qc2", func(p float64) {
		if returned.Load() {
			late.Store(true)
		}

		mu.Lock()
		progress = append(progress, p)
		mu.Unlock()

		if p > 0.875 {
			releaseOnce.Do(func() { releaseErr = os.WriteFile(release, nil, 0o600) })
		}
	}, vm.WithCommitWait(time.Millisecond))

	returned.Store(true)

	if err != nil {
		t.Fatalf("CommitToDisk(vm1, new.qc2) error = %v, want nil", err)
	}

	if releaseErr != nil {
		t.Fatalf("releasing fake qemu-img commit: %v", releaseErr)
	}

	// Without the stop, the reports would go on every millisecond.
	time.Sleep(20 * time.Millisecond)

	if late.Load() {
		t.Errorf("CommitToDisk(vm1, new.qc2) reported progress after it returned")
	}

	mu.Lock()
	defer mu.Unlock()

	// The copy of base.qc2 reports 0 or 0.8 first, a number of times that is
	// not fixed. The commit reports repeat until the next tick.
	var commit []float64

	for _, p := range progress {
		if len(commit) == 0 && p <= 0.8 {
			continue
		}

		if len(commit) == 0 || commit[len(commit)-1] != p {
			commit = append(commit, p)
		}
	}

	// 10 then 20 of 40 bytes: a quarter, then half, of the commit's 0.2 after
	// the copy's 0.8. A first tick after the growth sees only half.
	want := []float64{0.85, 0.9}
	if len(commit) > 0 && math.Abs(commit[0]-0.9) < 1e-9 {
		want = want[1:]
	}

	if diff := cmp.Diff(want, commit, cmpopts.EquateApprox(0, 1e-9)); diff != "" {
		t.Errorf("CommitToDisk(vm1, new.qc2) progress mismatch (-want +got):\n%s", diff)
	}
}

// When CommitToDisk fails before the commit, here as it stops the VM, it
// stops the copy of the base image, waits for it and removes the new image,
// so no copy and no progress report goes on after it returns.
//
//nolint:paralleltest // replaces the store, minimega, cluster files, PATH and the phenix base directory
func TestCommitToDiskStopsItsCopiesOnAnEarlyReturn(t *testing.T) {
	base := commitTree(t)
	out := base + "/images/new.qc2"

	// The base image is a FIFO, so its copy goes on until the test closes it.
	fifo := base + "/images/base.qc2"
	if err := os.Remove(fifo); err != nil {
		t.Fatalf("removing base image: %v", err)
	}

	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("making base image FIFO: %v", err)
	}

	node := vmNode("vm1")
	hardware := node["hardware"].(map[string]any) //nolint:forcetypeassert // vmNode's hardware
	hardware["drives"] = []map[string]any{{"image": "vm1.qc2"}}
	useExperiment(t, startTime, node)

	originalFiles := file.DefaultClusterFiles
	t.Cleanup(func() { file.DefaultClusterFiles = originalFiles }) //nolint:reassign // restore test double

	file.DefaultClusterFiles = &commitFiles{} //nolint:reassign // install test double

	runs := useQemuImg(t, "")

	var (
		reported   = make(chan struct{}) // the copy reported its progress
		failed     = make(chan struct{}) // the VM's shutdown failed
		reportOnce sync.Once
		writerDone = make(chan struct{})
	)

	go func() {
		defer close(writerDone)

		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}

		_, _ = w.WriteString("base")
		<-failed
		_ = w.Close()
	}()

	t.Cleanup(func() {
		// a reader that opens the FIFO lets a writer still waiting for one go on
		if r, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = r.Close()
		}

		<-writerDone
	})

	chain := fmt.Sprintf(`[{"filename":%q,"backing-filename":%q},{"filename":%q}]`,
		base+"/images/vm1.qc2", fifo, fifo)

	sent := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case strings.HasPrefix(cmd.Base, "shell qemu-img info"):
			return []*minicli.Response{mmtest.Text("head", chain)}
		case cmd.Base == "vm info":
			return []*minicli.Response{mmtest.Tabular("compute1", []string{"host", "name", "id", "state"},
				[]string{"compute1", "vm1", "5", "RUNNING"})}
		case cmd.Base == "vm info summary":
			select {
			case <-reported:
			case <-time.After(5 * time.Second):
			}

			close(failed)

			return []*minicli.Response{mmtest.Error("compute1", "minimega failed")}
		}

		return nil
	})

	var reporting atomic.Int32

	_, err := vm.CommitToDisk(t.Context(), testExp, "vm1", "new.qc2", func(float64) {
		reporting.Add(1)
		defer reporting.Add(-1)

		reportOnce.Do(func() { close(reported) })

		// a report still running when the shutdown fails ends after a return that does not wait for it
		<-failed
		time.Sleep(20 * time.Millisecond)
	})

	inReport := reporting.Load()

	if err == nil {
		t.Fatalf("CommitToDisk(vm1, new.qc2) with a failed shutdown error = nil, want an error")
	}

	if inReport != 0 {
		t.Errorf("CommitToDisk(vm1, new.qc2) returned while %d progress reports were running, want 0", inReport)
	}

	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("CommitToDisk(vm1, new.qc2) left %s (stat error %v), want it removed", out, err)
	}

	if got := runs(); len(got) != 0 {
		t.Errorf("CommitToDisk(vm1, new.qc2) ran qemu-img %q, want no runs", got)
	}

	// the copy of the VM's snapshot, which ignores ctx, starts only after the shutdown
	for _, base := range mmtest.Bases(sent()) {
		if strings.Contains(base, "shell mkdir") || strings.Contains(base, "shell cp") {
			t.Errorf("CommitToDisk(vm1, new.qc2) with a failed shutdown sent %q, want no snapshot copy", base)
		}
	}
}

// chainQueries are the minimega commands CommitToDisk sends for vm1 (ID 5,
// stopped on compute1) under the phenix base directory base, up to the copy of
// its snapshot.
func chainQueries(base string) []string {
	return append(stopQueries(base)[:3], copyQueries(base)...)
}

// stopQueries are the minimega commands CommitToDisk sends for vm1 (ID 5,
// running on compute1) up to the end of its shutdown.
func stopQueries(base string) []string {
	info := "shell qemu-img info --backing-chain " + base + "/images/vm1.qc2 --output json"

	return []string{info, info, "vm info", "vm info summary", "capture", systemPowerdown, "vm info summary"}
}

// copyQueries are the minimega commands that copy vm1's snapshot (ID 5) on
// compute1 into the experiment's tmp folder.
func copyQueries(base string) []string {
	tmp := base + "/images/" + testExp + "/tmp"

	return []string{
		"mesh send compute1 shell mkdir -p " + tmp,
		"mesh send compute1 shell cp " + common.MinimegaBase + "/5/disk-0.qcow2 " + tmp + "/vm1.qc2",
	}
}

// removeCopy is the minimega command that removes the copy of vm1's snapshot
// on compute1.
func removeCopy(base string) string {
	return "mesh send compute1 shell rm -f " + base + "/images/" + testExp + "/tmp/vm1.qc2"
}

// commitRuns are the qemu-img runs that commit vm1's snapshots into the new
// image out, up to the run of failOn.
func commitRuns(base, out, failOn string) []string {
	tmp := base + "/images/" + testExp + "/tmp"

	runs := []string{
		"rebase -f qcow2 -b " + base + "/images/" + out + " -F qcow2 " + tmp + "/snapshot-0.qc2",
		"rebase -f qcow2 -b " + tmp + "/snapshot-0.qc2 -F qcow2 " + tmp + "/vm1.qc2",
		"commit -b " + base + "/images/" + out + " " + tmp + "/vm1.qc2",
	}

	for i, run := range runs {
		if strings.HasPrefix(run, failOn+" ") {
			return runs[:i+1]
		}
	}

	return runs
}

// dumpStatus is QEMU's answer to a query-dump command.
func dumpStatus(status string, completed, total int) []*minicli.Response {
	return []*minicli.Response{mmtest.Text("compute1",
		fmt.Sprintf(`{"return":{"status":%q,"completed":%d,"total":%d}}`, status, completed, total))}
}

// MemorySnapshot has QEMU dump the VM's memory into the experiment files,
// reports its progress until it ends, and pulls the dump from the VM's node.
//
//nolint:paralleltest // replaces the store and minimega
func TestMemorySnapshot(t *testing.T) {
	const (
		queryDump = `vm qmp vm1 '{ "execute": "query-dump" }'`
		files     = "/phenix/images/" + testExp + "/files"
	)

	dump := func(path string) string {
		return `vm qmp vm1 '{ "execute": "dump-guest-memory", "arguments": { "protocol": "file:` + path +
			`", "paging": false, "format": "elf" , "detach": true} }'`
	}

	tests := []struct {
		name     string
		out      string
		host     string
		statuses [][]*minicli.Response // the answers to successive query-dump commands; the last repeats
		noCB     bool
		wantOut  string
		wantErr  error
		// the progress reports, and the minimega commands sent
		wantProgress, want []string
	}{
		{
			name:         "dumps a VM on another node and pulls the dump to the headnode",
			out:          "mem.raw",
			host:         "compute1",
			statuses:     [][]*minicli.Response{dumpStatus("active", 0, 0), dumpStatus("active", 1, 4), dumpStatus("completed", 4, 4)},
			wantOut:      files + "/mem.elf",
			wantProgress: []string{"0", "0.25", "1", "completed"},
			want: []string{
				"vm info", "mesh send compute1 shell mkdir -p " + files, dump(files + "/mem.elf"),
				queryDump, queryDump, queryDump, "file get " + testExp + "/files/mem.elf",
			},
		},
		{
			name:         "dumps a VM on the headnode to an absolute path",
			out:          "/dumps/mem",
			host:         "head",
			statuses:     [][]*minicli.Response{dumpStatus("completed", 4, 4)},
			wantOut:      "/dumps/mem.elf",
			wantProgress: []string{"1", "completed"},
			want:         []string{"vm info", " shell mkdir -p /dumps", dump("/dumps/mem.elf"), queryDump},
		},
		{
			name:     "reports progress to no callback",
			out:      "mem.elf",
			host:     "head",
			statuses: [][]*minicli.Response{dumpStatus("active", 1, 4), dumpStatus("completed", 4, 4)},
			noCB:     true,
			wantOut:  files + "/mem.elf",
			want:     []string{"vm info", " shell mkdir -p " + files, dump(files + "/mem.elf"), queryDump, queryDump},
		},
		{
			name:         "reports a failed dump",
			out:          "mem.elf",
			host:         "head",
			statuses:     [][]*minicli.Response{dumpStatus("failed", 0, 4)},
			wantOut:      "failed",
			wantErr:      errAny,
			wantProgress: []string{"failed"},
			want:         []string{"vm info", " shell mkdir -p " + files, dump(files + "/mem.elf"), queryDump},
		},
		{
			name:         "reports a failed status query",
			out:          "mem.elf",
			host:         "head",
			statuses:     [][]*minicli.Response{{mmtest.Error("head", "qmp busy")}},
			wantErr:      errAny,
			wantProgress: []string{"failed"},
			want:         []string{"vm info", " shell mkdir -p " + files, dump(files + "/mem.elf"), queryDump},
		},
		{
			name:         "reports a dump without a status",
			out:          "mem.elf",
			host:         "head",
			statuses:     [][]*minicli.Response{{mmtest.Text("head", `{"return":{}}`)}},
			wantErr:      errAny,
			wantProgress: []string{"failed"},
			want:         []string{"vm info", " shell mkdir -p " + files, dump(files + "/mem.elf"), queryDump},
		},
		{
			name:         "pulls a dump in a folder of the experiment files of a VM on another node",
			out:          "sub/mem.elf",
			host:         "compute1",
			statuses:     [][]*minicli.Response{dumpStatus("completed", 4, 4)},
			wantOut:      files + "/sub/mem.elf",
			wantProgress: []string{"1", "completed"},
			want: []string{
				"vm info", "mesh send compute1 shell mkdir -p " + files + "/sub", dump(files + "/sub/mem.elf"),
				queryDump, "file get " + testExp + "/files/sub/mem.elf",
			},
		},
		{
			name:         "pulls a dump at an absolute path in the experiment files of a VM on another node",
			out:          files + "/mem.elf",
			host:         "compute1",
			statuses:     [][]*minicli.Response{dumpStatus("completed", 4, 4)},
			wantOut:      files + "/mem.elf",
			wantProgress: []string{"1", "completed"},
			want: []string{
				"vm info", "mesh send compute1 shell mkdir -p " + files, dump(files + "/mem.elf"),
				queryDump, "file get " + testExp + "/files/mem.elf",
			},
		},
		{
			name:    "rejects an absolute path outside the experiment files for a VM on another node",
			out:     "/dumps/mem",
			host:    "compute1",
			wantErr: vm.ErrRemoteDumpPath,
			want:    []string{"vm info"},
		},
		{
			name:    "rejects a relative path out of the experiment files for a VM on another node",
			out:     "../mem.elf",
			host:    "compute1",
			wantErr: vm.ErrRemoteDumpPath,
			want:    []string{"vm info"},
		},
		{
			name:    "rejects a path minimega would split",
			out:     "my mem.elf",
			host:    "head",
			wantErr: disk.ErrUnsafeName,
		},
		{
			name:    "rejects a path that would end the QMP command",
			out:     `mem'.elf`,
			host:    "head",
			wantErr: disk.ErrUnsafeName,
		},
		{
			name:    "rejects a path that is not UTF-8",
			out:     "mem\xff.elf",
			host:    "head",
			wantErr: disk.ErrUnsafeName,
		},
		{
			name:    "reports a VM minimega does not have",
			out:     "mem.elf",
			wantErr: errAny,
			want:    []string{"vm info"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, "", vmNode("vm1"))

			var queries atomic.Int32

			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch cmd.Base {
				case "vm info":
					var rows [][]string
					if test.host != "" {
						rows = append(rows, []string{test.host, "vm1", "5", "RUNNING"})
					}

					return []*minicli.Response{mmtest.Tabular(test.host, []string{"host", "name", "id", "state"}, rows...)}
				case queryDump:
					n := min(int(queries.Add(1)), len(test.statuses)) - 1

					return test.statuses[n]
				}

				return nil
			})

			var (
				progress []string
				cb       = func(p string) { progress = append(progress, p) }
			)

			if test.noCB {
				cb = nil
			}

			got, err := vm.MemorySnapshot(t.Context(), testExp, "vm1", test.out, cb, vm.WithDumpTiming(time.Millisecond, time.Minute))
			if !errMatches(err, test.wantErr) {
				t.Errorf("MemorySnapshot(%q) error = %v, want %v", test.out, err, test.wantErr)
			}

			if got != test.wantOut {
				t.Errorf("MemorySnapshot(%q) = %q, want %q", test.out, got, test.wantOut)
			}

			if diff := cmp.Diff(test.wantProgress, progress, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("MemorySnapshot(%q) progress mismatch (-want +got):\n%s", test.out, diff)
			}

			if err := checkSent(received(), testExp, test.want); err != nil {
				t.Errorf("MemorySnapshot(%q) %v", test.out, err)
			}
		})
	}
}

// MemorySnapshot gives up on a dump that QEMU reports as active for longer
// than its time limit, and resumes the VM that QEMU paused for the dump.
//
//nolint:paralleltest // replaces the store and minimega
func TestMemorySnapshotGivesUpOnADumpThatDoesNotEnd(t *testing.T) {
	const resume = `vm qmp vm1 '{ "execute": "cont" }'`

	tests := []struct {
		name  string
		state string // the VM's state before the dump
		cont  []*minicli.Response
		// whether it sends QMP cont, and a part of the error
		wantResume bool
		wantErr    string
	}{
		{name: "resumes a running VM", state: "RUNNING", wantResume: true, wantErr: "did not complete within"},
		{name: "leaves a paused VM paused", state: "PAUSED", wantErr: "did not complete within"},
		{
			name: "reports a failed resume", state: "RUNNING", cont: []*minicli.Response{mmtest.Error("head", "qmp busy")},
			wantResume: true, wantErr: "resuming VM vm1 after memory snapshot",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useExperiment(t, "", vmNode("vm1"))

			received := useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				switch {
				case cmd.Base == "vm info":
					return []*minicli.Response{mmtest.Tabular("head", []string{"host", "name", "id", "state"},
						[]string{"head", "vm1", "5", test.state})}
				case strings.Contains(cmd.Base, "query-dump"):
					return dumpStatus("active", 1, 4)
				case cmd.Base == resume:
					return test.cont
				}

				return nil
			})

			var progress []string

			got, err := vm.MemorySnapshot(t.Context(), testExp, "vm1", "mem.elf", func(p string) { progress = append(progress, p) },
				vm.WithDumpTiming(time.Millisecond, 20*time.Millisecond))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Errorf("MemorySnapshot(mem.elf) error = %v, want one containing %q", err, test.wantErr)
			}

			if got != "" {
				t.Errorf("MemorySnapshot(mem.elf) = %q, want \"\"", got)
			}

			if len(progress) == 0 || progress[len(progress)-1] != "failed" {
				t.Errorf("MemorySnapshot(mem.elf) progress = %q, want it to end with failed", progress)
			}

			want := 0
			if test.wantResume {
				want = 1
			}

			if n := mmtest.Count(received(), resume); n != want {
				t.Errorf("MemorySnapshot(mem.elf) sent QMP cont %d times, want %d", n, want)
			}
		})
	}
}

// MemorySnapshot names a dump without a name after the VM and the time.
//
//nolint:paralleltest // replaces the store and minimega
func TestMemorySnapshotNamesTheDump(t *testing.T) {
	useExperiment(t, "", vmNode("vm1"))

	useMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
		switch {
		case cmd.Base == "vm info":
			return []*minicli.Response{mmtest.Tabular("head", []string{"host", "name", "id", "state"},
				[]string{"head", "vm1", "5", "RUNNING"})}
		case strings.Contains(cmd.Base, "query-dump"):
			return dumpStatus("completed", 1, 1)
		}

		return nil
	})

	got, err := vm.MemorySnapshot(t.Context(), testExp, "vm1", "", nil, vm.WithDumpTiming(time.Millisecond, time.Minute))
	if err != nil {
		t.Fatalf("MemorySnapshot(%q) error = %v, want nil", "", err)
	}

	want := regexp.MustCompile(`^/phenix/images/` + testExp + `/files/vm1_\d{8}_\d{4}\.elf$`)
	if !want.MatchString(got) {
		t.Errorf("MemorySnapshot(%q) = %q, want %s", "", got, want)
	}
}
