package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"
	"github.com/spf13/cobra"

	"phenix/api/disk"
	"phenix/api/vm"
	"phenix/store"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// errSnapshotListing is the error of a snapshot listing that vmSnapshotCmdFiles fails.
var errSnapshotListing = errors.New("file list failed")

// vmSnapshotCmdFiles lists the cluster's snapshots, or fails with err, and records the copies and syncs of a commit.
// Any other file.ClusterFiles method panics through the nil embedded interface.
type vmSnapshotCmdFiles struct {
	file.ClusterFiles

	snapshots []string
	err       error

	mu     sync.Mutex
	copies []string // "path -> dest"
	syncs  []string
}

func (f *vmSnapshotCmdFiles) GetExperimentSnapshots(string) ([]string, error) {
	return f.snapshots, f.err
}

func (f *vmSnapshotCmdFiles) CopyFile(path, dest string, _ file.CopyStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.copies = append(f.copies, path+" -> "+dest)

	return nil
}

func (f *vmSnapshotCmdFiles) SyncFile(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.syncs = append(f.syncs, path)

	return nil
}

// vmSnapshotCmdMinimega answers as a minimega that runs VM name on compute1 with ID 5 in the state, with the disks
// and the snapshot flag given. It answers qemu-img's backing chain with chain, and a command whose base starts
// with a key of fail gets that error message.
type vmSnapshotCmdMinimega struct {
	name, state, disks, snapshot string

	chain string
	fail  map[string]string
}

// handle is the mmtest.Handler of f.
func (f vmSnapshotCmdMinimega) handle(cmd mmtest.Command) []*minicli.Response {
	for prefix, msg := range f.fail {
		if strings.HasPrefix(cmd.Base, prefix) {
			return []*minicli.Response{mmtest.Error("compute1", msg)}
		}
	}

	switch {
	case cmd.Base == vmCmdInfoBase:
		header := []string{
			"uuid", "host", "name", "id", "state", "uptime", "vlan", "tap", "ip",
			"memory", "vcpus", "disks", "snapshot", "cdrom", "tags",
		}
		row := []string{
			"uuid-" + f.name, "compute1", f.name, "5", f.state, "60", "[EXP_1]", "[]", "[10.0.0.5]",
			"512", "1", f.disks, f.snapshot, "", "{}",
		}

		return []*minicli.Response{mmtest.Tabular("compute1", header, row)}
	case cmd.Base == "vm save":
		return []*minicli.Response{mmtest.Tabular("compute1", []string{"name", "status", "complete (%)"},
			[]string{f.name, "completed", "100"})}
	case strings.HasPrefix(cmd.Base, "shell qemu-img info"):
		return []*minicli.Response{mmtest.Text("head", f.chain)}
	}

	return nil
}

// vmSnapshotCmdRunningVM is a vmSnapshotCmdMinimega for VM web, running with one disk and its snapshot flag set.
func vmSnapshotCmdRunningVM() vmSnapshotCmdMinimega {
	return vmSnapshotCmdMinimega{name: "web", state: "RUNNING", disks: "/tmp/minimega/5/disk-0.qcow2,virtio", snapshot: "true"}
}

// vmSnapshotCmdUse makes the phenix store hold the vmCmdExperiment named exp, running if running is set, with a
// second disk for the db VM, and makes files the cluster's files, until the test ends.
func vmSnapshotCmdUse(t *testing.T, running bool, files *vmSnapshotCmdFiles) {
	t.Helper()

	cfg := vmCmdExperiment(t, "exp", running)
	topology := cfg.Spec["topology"].(map[string]any) //nolint:forcetypeassert // vmCmdExperiment's topology
	nodes := topology["nodes"].([]map[string]any)     //nolint:forcetypeassert // vmCmdExperiment's nodes
	hardware := nodes[1]["hardware"].(map[string]any) //nolint:forcetypeassert // vmCmdNode's hardware
	hardware["drives"] = []map[string]any{{"image": "db.qc2"}, {"image": "data.qc2"}}

	vmCmdUseStore(t, cfg)

	original := file.DefaultClusterFiles
	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore test double

	file.DefaultClusterFiles = files //nolint:reassign // install test double
}

// vmSnapshotCmdOutput runs phenix vm with args and returns what it writes to its output and error writers.
func vmSnapshotCmdOutput(args ...string) (string, string, error) {
	root := &cobra.Command{Use: "phenix", SilenceUsage: true, SilenceErrors: true}
	vmCmd := newVMCmd()
	vmCmd.AddCommand(newVMSnapshotCmd(), newVMCommitCmd())
	root.AddCommand(vmCmd)
	root.SetArgs(append([]string{"vm"}, args...))

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)

	_, err := root.ExecuteC()

	return stdout.String(), stderr.String(), err
}

// vmSnapshotCmdChanges returns the bases of the commands in cmds that change minimega state or the cluster's files.
func vmSnapshotCmdChanges(cmds []mmtest.Command) []string {
	reads := regexp.MustCompile(`^(vm info|vm info summary|cc client|capture|host|args|shell qemu-img info .*|mesh send \S+ disk info .*)$`)

	var changes []string

	for _, base := range mmtest.Bases(cmds) {
		if !reads.MatchString(base) {
			changes = append(changes, base)
		}
	}

	return changes
}

// TestVMCmdSnapshotArguments checks the errors of the snapshot and commit
// commands for arguments that they refuse before they read anything.
func TestVMCmdSnapshotArguments(t *testing.T) { //nolint:paralleltest // replaces minimega
	tests := []struct {
		name    string
		args    []string
		wantErr string // the whole error, for a refused argument count
		wantIs  error
	}{
		{name: "list without a VM", args: []string{"snapshot", "list", "exp"}, wantErr: "must provide an experiment name and VM name"},
		{
			name: "create without a name", args: []string{"snapshot", "create", "exp", "web"},
			wantErr: "must provide an experiment name, VM name, and snapshot name",
		},
		{name: "create with a folder", args: []string{"snapshot", "create", "exp", "web", "a/b"}, wantIs: disk.ErrUnsafeName},
		{name: "create with only an extension", args: []string{"snapshot", "create", "exp", "web", ".SNAP"}, wantIs: disk.ErrUnsafeName},
		{name: "create with a space", args: []string{"snapshot", "create", "exp", "web", "a b"}, wantIs: disk.ErrUnsafeName},
		{
			name: "restore without a name", args: []string{"snapshot", "restore", "exp", "web"},
			wantErr: "must provide an experiment name, VM name, and snapshot name",
		},
		{
			name: "commit without a VM", args: []string{"commit", "exp"},
			wantErr: "must provide an experiment name and VM name, optionally followed by an image name",
		},
		{
			name: "commit with too many arguments", args: []string{"commit", "exp", "web", "a.qc2", "b.qc2"},
			wantErr: "must provide an experiment name and VM name, optionally followed by an image name",
		},
		{name: "commit to a folder", args: []string{"commit", "exp", "web", "../web.qc2"}, wantIs: disk.ErrUnsafeName},
		{name: "commit to a name minimega splits", args: []string{"commit", "exp", "web", "a,b.qc2"}, wantIs: disk.ErrUnsafeName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := mmtest.Use(t, nil)

			_, _, err := vmSnapshotCmdOutput(tt.args...)

			switch {
			case tt.wantIs != nil && !errors.Is(err, tt.wantIs):
				t.Errorf("phenix vm %q error = %v, want %v", tt.args, err, tt.wantIs)
			case tt.wantIs == nil && (err == nil || err.Error() != tt.wantErr):
				t.Errorf("phenix vm %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			if got := mmtest.Bases(received()); len(got) != 0 {
				t.Errorf("phenix vm %q sent %q to minimega, want nothing", tt.args, got)
			}
		})
	}
}

// TestVMCmdSnapshotState checks that the snapshot and commit commands refuse
// a VM or experiment in a state that they cannot use, before they change
// anything.
func TestVMCmdSnapshotState(t *testing.T) { //nolint:paralleltest // replaces the phenix store, minimega and cluster files
	twoDisks := vmSnapshotCmdRunningVM()
	twoDisks.disks = "/tmp/minimega/5/disk-0.qcow2,virtio /phenix/images/data.qc2,virtio"

	inPlace := vmSnapshotCmdRunningVM()
	inPlace.snapshot = falseFlagDefault

	tests := []struct {
		name     string
		args     []string
		stopped  bool
		mm       vmSnapshotCmdMinimega
		wantIs   error
		wantHint string // text the error adds to say what to do
	}{
		{name: "list of a missing experiment", args: []string{"snapshot", "list", "nope", "web"}, wantIs: store.ErrNotExist},
		{name: "list of a missing VM", args: []string{"snapshot", "list", "exp", "nope"}, wantIs: mm.ErrVMNotFound},
		{
			name: "create in a stopped experiment", args: []string{"snapshot", "create", "exp", "web", "s1"},
			stopped: true, wantIs: vm.ErrExperimentNotRunning,
		},
		{
			name: "create of a missing VM", args: []string{"snapshot", "create", "exp", "nope", "s1"},
			wantIs: mm.ErrVMNotFound,
		},
		{
			name: "create without the snapshot flag", args: []string{"snapshot", "create", "exp", "web", "s1"},
			mm: inPlace, wantIs: vm.ErrSnapshotDisabled, wantHint: "phenix vm set <experiment name> web --snapshot",
		},
		{
			name: "create with two disks", args: []string{"snapshot", "create", "exp", "web", "s1"},
			mm: twoDisks, wantIs: vm.ErrMultipleDisks, wantHint: "sandialabs/sceptre-phenix#145",
		},
		{
			name: "restore in a stopped experiment", args: []string{"snapshot", "restore", "exp", "web", "web__s1"},
			stopped: true, wantIs: vm.ErrExperimentNotRunning,
		},
		{
			name: "restore of a missing snapshot", args: []string{"snapshot", "restore", "exp", "web", "web__s9"},
			mm: vmSnapshotCmdRunningVM(), wantIs: vm.ErrSnapshotNotFound,
		},
		{
			name: "restore with two disks", args: []string{"snapshot", "restore", "exp", "web", "web__s1"},
			mm: twoDisks, wantIs: vm.ErrMultipleDisks,
		},
		{
			name: "commit in a stopped experiment", args: []string{"commit", "exp", "web", "new.qc2"},
			stopped: true, wantIs: vm.ErrExperimentNotRunning,
		},
		{
			name: "commit without the snapshot flag", args: []string{"commit", "exp", "web", "new.qc2"},
			mm: inPlace, wantIs: vm.ErrSnapshotDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmSnapshotCmdUse(t, !tt.stopped, &vmSnapshotCmdFiles{snapshots: []string{"web__s1"}})
			vmSnapshotCmdUseImages(t)

			fake := tt.mm
			fake.chain = `[{"filename":"` + common.PhenixBase + `/images/web.qc2"}]`
			received := mmtest.Use(t, fake.handle)

			_, _, err := vmSnapshotCmdOutput(tt.args...)
			if !errors.Is(err, tt.wantIs) {
				t.Errorf("phenix vm %q error = %v, want %v", tt.args, err, tt.wantIs)
			}

			if err != nil && !strings.Contains(err.Error(), tt.wantHint) {
				t.Errorf("phenix vm %q error = %v, want it to say %q", tt.args, err, tt.wantHint)
			}

			if got := vmSnapshotCmdChanges(received()); len(got) != 0 {
				t.Errorf("phenix vm %q sent %q to minimega, want no change", tt.args, got)
			}
		})
	}
}

// TestVMCmdSnapshotList checks that snapshot list prints the VM's snapshots
// in sort order, and returns the error of a listing that fails.
func TestVMCmdSnapshotList(t *testing.T) { //nolint:paralleltest // replaces the phenix store and cluster files
	tests := []struct {
		name      string
		snapshots []string
		listErr   error
		want      string
		wantErr   error
	}{
		{name: "sorted", snapshots: []string{"web__b", "db__a", "web__a"}, want: "web__a\nweb__b\n"},
		{name: "none", snapshots: []string{"db__a"}},
		{name: "failed listing", listErr: errSnapshotListing, wantErr: errSnapshotListing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmSnapshotCmdUse(t, false, &vmSnapshotCmdFiles{snapshots: tt.snapshots, err: tt.listErr})

			got, _, err := vmSnapshotCmdOutput("snapshot", "list", "exp", "web")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("phenix vm snapshot list exp web error = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("phenix vm snapshot list exp web printed %q, want %q", got, tt.want)
			}
		})
	}
}

// TestVMCmdSnapshotCreate checks that snapshot create saves the VM on its
// node, moves the files into the experiment's files on that node, and prints
// the snapshot's name, or returns minimega's error.
func TestVMCmdSnapshotCreate(t *testing.T) { //nolint:paralleltest // replaces the phenix store, minimega and cluster files
	files := common.PhenixBase + "/images/exp/files"
	saved := common.PhenixBase + "/images/exp_web__before-update"

	tests := []struct {
		name    string
		fail    map[string]string
		want    string
		wantErr []string // the parts of the error, none for no error
		changes []string
	}{
		{
			name: "created",
			want: "Created snapshot web__before-update of VM web in experiment exp\n",
			changes: []string{
				"vm save web exp_web__before-update", "vm save", "vm start web",
				"mesh send compute1 shell mkdir -p " + files,
				"mesh send compute1 shell mv " + saved + ".state " + files + "/web__before-update.state",
				"mesh send compute1 shell mv " + saved + ".hdd " + files + "/web__before-update.hdd",
			},
		},
		{
			name: "save refused",
			fail: map[string]string{"vm save web": "migration failed: no space left on device"},
			wantErr: []string{
				"unable to create a snapshot of the web VM: starting memory snapshot for VM web: ",
				"migration failed: no space left on device",
			},
			changes: []string{"vm save web exp_web__before-update"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmSnapshotCmdUse(t, true, &vmSnapshotCmdFiles{})

			fake := vmSnapshotCmdRunningVM()
			fake.fail = tt.fail
			received := mmtest.Use(t, fake.handle)

			got, _, err := vmSnapshotCmdOutput("snapshot", "create", "exp", "web", "before-update.SNAP")

			if err := errorHas(err, tt.wantErr); err != nil {
				t.Fatalf("phenix vm snapshot create: %v", err)
			}

			if got != tt.want {
				t.Errorf("phenix vm snapshot create printed %q, want %q", got, tt.want)
			}

			if diff := cmp.Diff(tt.changes, vmSnapshotCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm snapshot create minimega commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestVMCmdSnapshotRestore checks that snapshot restore relaunches the VM
// from the snapshot's files, or returns minimega's error.
func TestVMCmdSnapshotRestore(t *testing.T) { //nolint:paralleltest // replaces the phenix store, minimega and cluster files
	relaunch := []string{
		"vm config clone web", "vm config uuid uuid-web", "vm config state exp/files/web__s1.state",
		"vm config disk exp/files/web__s1.hdd,writeback", "vm kill web", "vm flush web", "vm launch kvm web",
		"vm launch", "vm start web",
	}

	tests := []struct {
		name    string
		fail    map[string]string
		want    string
		wantErr []string // the parts of the error, none for no error
		changes []string
	}{
		{name: "restored", want: "Restored VM web in experiment exp to snapshot web__s1\n", changes: relaunch},
		{
			name:    "relaunch refused",
			fail:    map[string]string{"vm launch kvm": "file not found: exp/files/web__s1.hdd"},
			wantErr: []string{"unable to restore the web VM: relaunching VM web: ", "file not found: exp/files/web__s1.hdd"},
			changes: relaunch[:7],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vmSnapshotCmdUse(t, true, &vmSnapshotCmdFiles{snapshots: []string{"web__s1", "db__s1"}})

			fake := vmSnapshotCmdRunningVM()
			fake.fail = tt.fail
			received := mmtest.Use(t, fake.handle)

			got, _, err := vmSnapshotCmdOutput("snapshot", "restore", "exp", "web", "web__s1")

			if err := errorHas(err, tt.wantErr); err != nil {
				t.Fatalf("phenix vm snapshot restore: %v", err)
			}

			if got != tt.want {
				t.Errorf("phenix vm snapshot restore printed %q, want %q", got, tt.want)
			}

			if diff := cmp.Diff(tt.changes, vmSnapshotCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm snapshot restore minimega commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// vmSnapshotCmdUseImages makes a temporary phenix base directory with the images web.qc2 and db.qc2 until the test
// ends.
func vmSnapshotCmdUseImages(t *testing.T) {
	t.Helper()

	original := common.PhenixBase
	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore test configuration

	common.PhenixBase = t.TempDir() //nolint:reassign // images go to a temporary tree

	if err := os.MkdirAll(common.PhenixBase+"/images", 0o750); err != nil {
		t.Fatalf("creating images directory: %v", err)
	}

	for _, name := range []string{"web", "db"} {
		if err := os.WriteFile(common.PhenixBase+"/images/"+name+".qc2", []byte(name+" image"), 0o600); err != nil {
			t.Fatalf("writing image %s: %v", name, err)
		}
	}
}

// vmSnapshotCmdUseQemuImg puts a fake qemu-img first on PATH until the test ends. The fake fails when its first
// argument is failOn. It returns a function that lists the arguments of each run.
func vmSnapshotCmdUseQemuImg(t *testing.T, failOn string) func() []string {
	t.Helper()

	dir := t.TempDir()
	log := filepath.Join(dir, "runs")

	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" +
		"if [ \"$1\" = '" + failOn + "' ]; then echo 'qemu-img: Could not open the disk' >&2; exit 1; fi\n"

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

// TestVMCmdCommit checks that commit writes the first disk of a VM on another
// node to a new image, copies the image to the cluster, warns about the other
// disks, and prints the image's name, or returns qemu-img's error.
func TestVMCmdCommit(t *testing.T) { //nolint:paralleltest // replaces the store, minimega, cluster files, PATH and base dir
	stamped := regexp.MustCompile(`^Created image (web_\d{14}\.qc2) from the first disk of VM web in experiment exp\n$`)

	tests := []struct {
		name      string
		args      []string
		vm        string
		failOn    string
		want      string
		wantWarn  string
		wantErr   string
		wantImage string // the image and its content, "" for none
	}{
		{
			name: "named image", args: []string{"commit", "exp", "web", "web-new.qc2"}, vm: "web",
			want: "Created image web-new.qc2 from the first disk of VM web in experiment exp\n", wantImage: "web-new.qc2",
		},
		{name: "generated name", args: []string{"commit", "exp", "web"}, vm: "web"},
		{
			name: "VM with two disks", args: []string{"commit", "exp", "db", "db-new.qc2"}, vm: "db",
			want:     "Created image db-new.qc2 from the first disk of VM db in experiment exp\n",
			wantWarn: "warning: VM db has 2 disks; the new image holds only the first disk\n", wantImage: "db-new.qc2",
		},
		{
			name: "failed commit", args: []string{"commit", "exp", "web", "web-new.qc2"}, vm: "web", failOn: "commit",
			wantErr: "unable to commit the disk of the web VM: committing snapshot (qemu-img: Could not open the disk\n): exit status 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := &vmSnapshotCmdFiles{}
			vmSnapshotCmdUse(t, true, files)
			vmSnapshotCmdUseImages(t)

			runs := vmSnapshotCmdUseQemuImg(t, tt.failOn)
			fake := vmSnapshotCmdMinimega{
				name: tt.vm, state: "QUIT", disks: "/tmp/minimega/5/disk-0.qcow2", snapshot: "true",
				chain: `[{"filename":"` + common.PhenixBase + `/images/` + tt.vm + `.qc2"}]`,
			}
			received := mmtest.Use(t, fake.handle)

			got, warned, err := vmSnapshotCmdOutput(tt.args...)

			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("phenix vm %q error = %v", tt.args, err)
			case tt.wantErr != "" && (err == nil || err.Error() != tt.wantErr):
				t.Fatalf("phenix vm %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			image := tt.wantImage
			if tt.want == "" && tt.wantErr == "" {
				match := stamped.FindStringSubmatch(got)
				if match == nil {
					t.Fatalf("phenix vm %q printed %q, want Created image web_<time>.qc2 ...", tt.args, got)
				}

				image = match[1]
			} else if got != tt.want {
				t.Errorf("phenix vm %q printed %q, want %q", tt.args, got, tt.want)
			}

			if warned != tt.wantWarn {
				t.Errorf("phenix vm %q warned %q, want %q", tt.args, warned, tt.wantWarn)
			}

			tmp := common.PhenixBase + "/images/exp/tmp/" + tt.vm + ".qc2"
			wantChanges := []string{
				"mesh send compute1 shell mkdir -p " + common.PhenixBase + "/images/exp/tmp",
				"mesh send compute1 shell cp " + common.MinimegaBase + "/5/disk-0.qcow2 " + tmp,
				"mesh send compute1 shell rm -f " + tmp,
			}

			if tt.wantErr == "" {
				wantChanges = slices.Insert(wantChanges, 2, "vm start "+tt.vm)
			}

			if diff := cmp.Diff(wantChanges, vmSnapshotCmdChanges(received())); diff != "" {
				t.Errorf("phenix vm %q minimega commands mismatch (-want +got):\n%s", tt.args, diff)
			}

			if image == "" {
				return
			}

			out := common.PhenixBase + "/images/" + image
			wantRuns := []string{"rebase -f qcow2 -b " + out + " -F qcow2 " + tmp, "commit -b " + out + " " + tmp}

			if diff := cmp.Diff(wantRuns, runs()); diff != "" {
				t.Errorf("phenix vm %q qemu-img runs mismatch (-want +got):\n%s", tt.args, diff)
			}

			if data, err := os.ReadFile(out); err != nil || string(data) != tt.vm+" image" {
				t.Errorf("phenix vm %q wrote %s = %q, %v; want a copy of %s.qc2", tt.args, image, data, err, tt.vm)
			}

			headnode, _ := os.Hostname()

			files.mu.Lock()
			defer files.mu.Unlock()

			if diff := cmp.Diff([]string{"exp/tmp/" + tt.vm + ".qc2 -> " + headnode}, files.copies); diff != "" {
				t.Errorf("phenix vm %q cluster copies mismatch (-want +got):\n%s", tt.args, diff)
			}

			if diff := cmp.Diff([]string{image}, files.syncs, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("phenix vm %q cluster syncs mismatch (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

// errorHas returns an error unless err contains each of parts, or err is nil when parts is empty.
func errorHas(err error, parts []string) error {
	switch {
	case len(parts) == 0 && err != nil:
		return fmt.Errorf("error = %w, want nil", err)
	case len(parts) != 0 && err == nil:
		return fmt.Errorf("error = nil, want one containing %q", parts)
	}

	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			return fmt.Errorf("error = %w, want one containing %q", err, part)
		}
	}

	return nil
}

// TestVMCmdSnapshotCompletion checks that snapshot restore completes the
// experiment and VM names, then the VM's snapshots in sort order.
func TestVMCmdSnapshotCompletion(t *testing.T) { //nolint:paralleltest // replaces the phenix store and cluster files
	files := &vmSnapshotCmdFiles{snapshots: []string{"web__b", "db__a", "web__a", "web__x"}}
	vmSnapshotCmdUse(t, false, files)

	tests := []struct {
		name       string
		args       []string
		toComplete string
		listErr    error
		want       []string
		directive  cobra.ShellCompDirective
	}{
		{name: "VMs", args: []string{"exp"}, toComplete: "w", want: []string{"web"}, directive: cobra.ShellCompDirectiveNoFileComp},
		{
			name: "snapshots", args: []string{"exp", "web"}, toComplete: "web__",
			want: []string{"web__a", "web__b", "web__x"}, directive: cobra.ShellCompDirectiveNoFileComp,
		},
		{
			name: "snapshots with a prefix", args: []string{"exp", "web"}, toComplete: "web__x",
			want: []string{"web__x"}, directive: cobra.ShellCompDirectiveNoFileComp,
		},
		{name: "failed listing", args: []string{"exp", "web"}, listErr: errSnapshotListing, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "fourth argument", args: []string{"exp", "web", "web__a"}, directive: cobra.ShellCompDirectiveNoFileComp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files.err = tt.listErr

			got, directive := vmSnapshotCompletion(nil, tt.args, tt.toComplete)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("vmSnapshotCompletion(%q, %q) mismatch (-want +got):\n%s", tt.args, tt.toComplete, diff)
			}

			if directive != tt.directive {
				t.Errorf("vmSnapshotCompletion(%q, %q) directive = %v, want %v", tt.args, tt.toComplete, directive, tt.directive)
			}
		})
	}
}

// TestVMCmdSnapshotHelp checks that phenix vm snapshot without a subcommand
// prints its help, with the list, create and restore subcommands, and sends
// nothing to minimega.
func TestVMCmdSnapshotHelp(t *testing.T) { //nolint:paralleltest // replaces minimega
	received := mmtest.Use(t, nil)

	got, _, err := vmSnapshotCmdOutput("snapshot")
	if err != nil {
		t.Fatalf("phenix vm snapshot error = %v", err)
	}

	for _, want := range []string{"phenix vm snapshot [command]", "\n  create ", "\n  list ", "\n  restore "} {
		if !strings.Contains(got, want) {
			t.Errorf("phenix vm snapshot printed %q, want it to contain %q", got, want)
		}
	}

	if got := mmtest.Bases(received()); len(got) != 0 {
		t.Errorf("phenix vm snapshot sent %q to minimega, want nothing", got)
	}
}

// TestVMCmdCommitNeedsABaseImage checks that phenix vm commit without an image
// name refuses a VM whose topology has no disk to name the image after, before
// it changes anything.
func TestVMCmdCommitNeedsABaseImage(t *testing.T) { //nolint:paralleltest // replaces the phenix store and minimega
	cfg := vmCmdExperiment(t, "exp", true)
	topology := cfg.Spec["topology"].(map[string]any) //nolint:forcetypeassert // vmCmdExperiment's topology
	nodes := topology["nodes"].([]map[string]any)     //nolint:forcetypeassert // vmCmdExperiment's nodes
	hardware := nodes[0]["hardware"].(map[string]any) //nolint:forcetypeassert // vmCmdNode's hardware
	hardware["drives"] = []map[string]any{}

	vmCmdUseStore(t, cfg)

	received := mmtest.Use(t, vmSnapshotCmdRunningVM().handle)

	_, _, err := vmSnapshotCmdOutput("commit", "exp", "web")

	want := "unable to commit the disk of the web VM: getting base disk image: vm web for experiment exp has no drives"
	if err == nil || err.Error() != want {
		t.Errorf("phenix vm commit exp web error = %v, want %q", err, want)
	}

	if got := vmSnapshotCmdChanges(received()); len(got) != 0 {
		t.Errorf("phenix vm commit exp web sent %q to minimega, want no changes", got)
	}
}
