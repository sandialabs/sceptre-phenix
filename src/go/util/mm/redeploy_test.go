package mm

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/mm/mmtest"
)

func TestRedeployVMKeepsItsType(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		vmType, want string
	}{
		"container": {vmType: "container", want: "vm launch container a"},
		"kvm":       {vmType: "kvm", want: "vm launch kvm a"},
		"unknown":   {vmType: "", want: "vm launch kvm a"},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == vmInfoCmd {
					return []*minicli.Response{mmtest.Tabular("head", []string{"name", "type", "disks"}, []string{"a", tc.vmType, ""})}
				}

				return nil
			})

			if err := (Minimega{}).RedeployVM(NS("exp"), VMName("a")); err != nil {
				t.Fatalf("RedeployVM(a) error = %v", err)
			}

			var launches []string

			for _, base := range mmtest.Bases(received()) {
				if strings.HasPrefix(base, "vm launch ") {
					launches = append(launches, base)
				}
			}

			if diff := cmp.Diff([]string{tc.want}, launches); diff != "" {
				t.Errorf("RedeployVM(a) launches mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// redeployInjects is a RedeployVM of VM a, deployed with a disk snapshot, with
// new VCPUs, memory and disk and files to inject into the disk.
func redeployInjects() []Option {
	return []Option{
		NS("exp"), VMName("a"), CPU(2), Mem(1024), Disk("/phenix/images/new.qcow2"),
		Injects("/src/a:/dst/a", "/src/b:/dst/b"), InjectPartition(2),
	}
}

// redeployInjectsSent is what redeployInjects sends, in order.
//
//nolint:gochecknoglobals // a constant list
var redeployInjectsSent = []mmtest.Command{
	{Namespace: "exp", Base: vmInfoCmd},
	{Namespace: "exp", Base: "vm config clone a"},
	{Namespace: "exp", Base: "clear vm config state"},
	{Namespace: "exp", Base: "vm kill a"},
	{Namespace: "exp", Base: "vm flush a"},
	{Namespace: "exp", Base: "vm config vcpus 2"},
	{Namespace: "exp", Base: "vm config mem 1024"},
	{Namespace: "", Base: "mesh send all file delete exp_a_snapshot"},
	{Namespace: "", Base: "file delete exp_a_snapshot"},
	{Namespace: "exp", Base: "disk snapshot /phenix/images/new.qcow2 exp_a_snapshot"},
	{Namespace: "", Base: "disk inject exp_a_snapshot:2 files /src/a:/dst/a /src/b:/dst/b"},
	{Namespace: "exp", Base: "vm config disk /phenix/images/exp_a_snapshot,writeback"},
	{Namespace: "exp", Base: "vm launch kvm a"},
	{Namespace: "exp", Base: vmLaunchCmd},
	{Namespace: "exp", Base: "vm start a"},
}

// snapshotVM answers `vm info` with VM a, deployed with disks.
func snapshotVM(disks string) mmtest.Handler {
	return func(cmd mmtest.Command) []*minicli.Response {
		if cmd.Base == vmInfoCmd {
			return []*minicli.Response{mmtest.Tabular("head", []string{"name", "type", "disks"}, []string{"a", "kvm", disks})}
		}

		return nil
	}
}

func TestRedeployVM(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		disks string
		opts  []Option
		want  []mmtest.Command
	}{
		"injects files into a new snapshot of the new disk": {
			disks: "/phenix/images/exp_a_snapshot,writeback",
			opts:  redeployInjects(),
			want:  redeployInjectsSent,
		},
		"takes the new disk's cache mode": {
			disks: "/phenix/images/exp_a_snapshot,writeback",
			opts:  []Option{NS("exp"), VMName("a"), Disk("/phenix/images/new.qcow2,none"), Injects("/x:/y")},
			want: []mmtest.Command{
				{Namespace: "exp", Base: vmInfoCmd},
				{Namespace: "exp", Base: "vm config clone a"},
				{Namespace: "exp", Base: "clear vm config state"},
				{Namespace: "exp", Base: "vm kill a"},
				{Namespace: "exp", Base: "vm flush a"},
				{Namespace: "", Base: "mesh send all file delete exp_a_snapshot"},
				{Namespace: "", Base: "file delete exp_a_snapshot"},
				{Namespace: "exp", Base: "disk snapshot /phenix/images/new.qcow2 exp_a_snapshot"},
				{Namespace: "", Base: "disk inject exp_a_snapshot:1 files /x:/y"},
				{Namespace: "exp", Base: "vm config disk /phenix/images/exp_a_snapshot,none"},
				{Namespace: "exp", Base: "vm launch kvm a"},
				{Namespace: "exp", Base: vmLaunchCmd},
				{Namespace: "exp", Base: "vm start a"},
			},
		},
		"keeps the interface and the other disks of a snapshot VM": {
			disks: "/phenix/images/exp_a_snapshot,virtio,writeback /phenix/images/data.qcow2,ide",
			opts:  []Option{NS("exp"), VMName("a"), Disk("/phenix/images/new.qcow2,none"), Injects("/x:/y")},
			want: []mmtest.Command{
				{Namespace: "exp", Base: vmInfoCmd},
				{Namespace: "exp", Base: "vm config clone a"},
				{Namespace: "exp", Base: "clear vm config state"},
				{Namespace: "exp", Base: "vm kill a"},
				{Namespace: "exp", Base: "vm flush a"},
				{Namespace: "", Base: "mesh send all file delete exp_a_snapshot"},
				{Namespace: "", Base: "file delete exp_a_snapshot"},
				{Namespace: "exp", Base: "disk snapshot /phenix/images/new.qcow2 exp_a_snapshot"},
				{Namespace: "", Base: "disk inject exp_a_snapshot:1 files /x:/y"},
				{Namespace: "exp", Base: "vm config disk /phenix/images/exp_a_snapshot,virtio,none /phenix/images/data.qcow2,ide"},
				{Namespace: "exp", Base: "vm launch kvm a"},
				{Namespace: "exp", Base: vmLaunchCmd},
				{Namespace: "exp", Base: "vm start a"},
			},
		},
		"replaces only the first of several disks": {
			disks: "/phenix/images/a.qcow2,virtio /phenix/images/data.qcow2,ide,none",
			opts:  []Option{NS("exp"), VMName("a"), Disk("/phenix/images/new.qcow2")},
			want: []mmtest.Command{
				{Namespace: "exp", Base: vmInfoCmd},
				{Namespace: "exp", Base: "vm config clone a"},
				{Namespace: "exp", Base: "clear vm config state"},
				{Namespace: "exp", Base: "vm kill a"},
				{Namespace: "exp", Base: "vm flush a"},
				{Namespace: "exp", Base: "vm config disk /phenix/images/new.qcow2,virtio /phenix/images/data.qcow2,ide,none"},
				{Namespace: "exp", Base: "vm launch kvm a"},
				{Namespace: "exp", Base: vmLaunchCmd},
				{Namespace: "exp", Base: "vm start a"},
			},
		},
		"sends no VCPUs or memory for zero": {
			disks: "/phenix/images/a.qcow2",
			opts:  []Option{NS("exp"), VMName("a"), CPU(0), Mem(0), Disk("/phenix/images/new.qcow2")},
			want: []mmtest.Command{
				{Namespace: "exp", Base: vmInfoCmd},
				{Namespace: "exp", Base: "vm config clone a"},
				{Namespace: "exp", Base: "clear vm config state"},
				{Namespace: "exp", Base: "vm kill a"},
				{Namespace: "exp", Base: "vm flush a"},
				{Namespace: "exp", Base: "vm config disk /phenix/images/new.qcow2"},
				{Namespace: "exp", Base: "vm launch kvm a"},
				{Namespace: "exp", Base: vmLaunchCmd},
				{Namespace: "exp", Base: "vm start a"},
			},
		},
		"uses the new disk as is for a VM without a snapshot": {
			disks: "/phenix/images/a.qcow2",
			opts:  []Option{NS("exp"), VMName("a"), Disk("/phenix/images/new.qcow2"), Injects("/x:/y")},
			want: []mmtest.Command{
				{Namespace: "exp", Base: vmInfoCmd},
				{Namespace: "exp", Base: "vm config clone a"},
				{Namespace: "exp", Base: "clear vm config state"},
				{Namespace: "exp", Base: "vm kill a"},
				{Namespace: "exp", Base: "vm flush a"},
				{Namespace: "exp", Base: "vm config disk /phenix/images/new.qcow2"},
				{Namespace: "exp", Base: "vm launch kvm a"},
				{Namespace: "exp", Base: vmLaunchCmd},
				{Namespace: "exp", Base: "vm start a"},
			},
		},
		"keeps the interface and cache mode without injects": {
			disks: "/phenix/images/a.qcow2,virtio,writeback",
			opts:  []Option{NS("exp"), VMName("a"), Disk("/phenix/images/new.qcow2")},
			want: []mmtest.Command{
				{Namespace: "exp", Base: vmInfoCmd},
				{Namespace: "exp", Base: "vm config clone a"},
				{Namespace: "exp", Base: "clear vm config state"},
				{Namespace: "exp", Base: "vm kill a"},
				{Namespace: "exp", Base: "vm flush a"},
				{Namespace: "exp", Base: "vm config disk /phenix/images/new.qcow2,virtio,writeback"},
				{Namespace: "exp", Base: "vm launch kvm a"},
				{Namespace: "exp", Base: vmLaunchCmd},
				{Namespace: "exp", Base: "vm start a"},
			},
		},
		"uses the new disk as is without injects": {
			disks: "/phenix/images/exp_a_snapshot",
			opts:  []Option{NS("exp"), VMName("a"), Disk("/phenix/images/new.qcow2")},
			want: []mmtest.Command{
				{Namespace: "exp", Base: vmInfoCmd},
				{Namespace: "exp", Base: "vm config clone a"},
				{Namespace: "exp", Base: "clear vm config state"},
				{Namespace: "exp", Base: "vm kill a"},
				{Namespace: "exp", Base: "vm flush a"},
				{Namespace: "exp", Base: "vm config disk /phenix/images/new.qcow2"},
				{Namespace: "exp", Base: "vm launch kvm a"},
				{Namespace: "exp", Base: vmLaunchCmd},
				{Namespace: "exp", Base: "vm start a"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, snapshotVM(tc.disks))

			if err := (Minimega{}).RedeployVM(tc.opts...); err != nil {
				t.Fatalf("RedeployVM(a) error = %v", err)
			}

			if diff := cmp.Diff(tc.want, received(), ignoreRaw); diff != "" {
				t.Errorf("RedeployVM(a) sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A redeploy stops at the first command minimega reports an error for, and
// reports that error.
func TestRedeployVMStopsAtTheFirstError(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for i, failing := range redeployInjectsSent[1:] {
		t.Run(failing.Base, func(t *testing.T) {
			snapshot := snapshotVM("/phenix/images/exp_a_snapshot,writeback")

			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == failing.Base {
					return []*minicli.Response{mmtest.Error("head", "boom")}
				}

				return snapshot(cmd)
			})

			err := (Minimega{}).RedeployVM(redeployInjects()...)
			if err == nil || !strings.Contains(err.Error(), "boom") {
				t.Errorf("RedeployVM(a) with %q failing: error = %v, want minimega's boom", failing.Base, err)
			}

			if diff := cmp.Diff(redeployInjectsSent[:i+2], received(), ignoreRaw); diff != "" {
				t.Errorf("RedeployVM(a) with %q failing sent commands mismatch (-want +got):\n%s", failing.Base, diff)
			}
		})
	}
}

// A redeploy changes nothing for a VM minimega does not know, whose type
// minimega would not read back as one argument, or with a malformed disk.
func TestRedeployVMChangesNothing(t *testing.T) { //nolint:paralleltest // shares the fake minimega
	for name, tc := range map[string]struct {
		answer []*minicli.Response
		disk   string
		wantIs error
	}{
		"a missing VM": {answer: nil},
		"an unsafe type": {
			answer: []*minicli.Response{mmtest.Tabular("head", []string{"name", "type"}, []string{"a", "kvm #"})},
			wantIs: ErrUnsafeArg,
		},
		"a malformed new disk": {
			answer: []*minicli.Response{mmtest.Tabular("head", []string{"name", "type", "disks"}, []string{"a", "kvm", "a.qcow2"})},
			disk:   "new.qcow2,bogus",
			wantIs: ErrMalformedDiskSpec,
		},
		"a malformed current disk": {
			answer: []*minicli.Response{mmtest.Tabular("head", []string{"name", "type", "disks"}, []string{"a", "kvm", "a.qcow2,,"})},
			disk:   "new.qcow2",
			wantIs: ErrMalformedDiskSpec,
		},
	} {
		t.Run(name, func(t *testing.T) {
			received := useFakeMinimega(t, func(cmd mmtest.Command) []*minicli.Response {
				if cmd.Base == vmInfoCmd {
					return tc.answer
				}

				return nil
			})

			err := RedeployVM(NS("exp"), VMName("a"), Disk(tc.disk))

			switch {
			case err == nil:
				t.Error("RedeployVM(a) error = nil, want an error")
			case tc.wantIs != nil && !errors.Is(err, tc.wantIs):
				t.Errorf("RedeployVM(a) error = %v, want %v", err, tc.wantIs)
			}

			if diff := cmp.Diff([]string{vmInfoCmd}, mmtest.Bases(received())); diff != "" {
				t.Errorf("RedeployVM(a) sent commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
