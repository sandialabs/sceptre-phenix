package cmd

import (
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/cobra"

	"phenix/store"
	"phenix/util/mm"
)

// findCaptureSubcommand looks up one of the "vm capture" subcommands (e.g.
// "start" or "stop") by the first word of its Use string, so tests can
// invoke its RunE directly without going through the full cobra command
// tree.
func findCaptureSubcommand(t *testing.T, name string) *cobra.Command {
	t.Helper()

	root := newVMCaptureCmd()

	for _, sub := range root.Commands() {
		if strings.Fields(sub.Use)[0] == name {
			return sub
		}
	}

	t.Fatalf("subcommand %q not found under vm capture", name)

	return nil
}

// captureCmdTestMM is a test double for mm.MM used by the vm capture CLI
// tests below. Embedding the (nil) mm.MM interface means any method call
// this test doesn't stub will panic, which is fine since these tests only
// ever exercise the capture-related methods.
type captureCmdTestMM struct {
	mm.MM

	vmInfo   mm.VMs
	captures []mm.Capture

	startCalls int
	stopCalls  int
}

func (m *captureCmdTestMM) GetVMInfo(...mm.Option) mm.VMs {
	return m.vmInfo
}

func (m *captureCmdTestMM) GetVMCaptures(...mm.Option) []mm.Capture {
	return m.captures
}

func (m *captureCmdTestMM) StartVMCapture(...mm.Option) error {
	m.startCalls++

	return nil
}

func (m *captureCmdTestMM) StopVMCapture(...mm.Option) error {
	m.stopCalls++

	return nil
}

// installCaptureCmdTestMM installs the given fake as mm.DefaultMM for the
// duration of the test, restoring the original afterward.
func installCaptureCmdTestMM(t *testing.T, fake *captureCmdTestMM) {
	t.Helper()

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore test double

	mm.DefaultMM = fake //nolint:reassign // install test double
}

// installCaptureCmdTestExperiment builds a store mock backing a running
// experiment named "test-experiment" with a single VM named "test-vm" that
// has two network interfaces, named "IF0" and "IF1", on VLANs "EXP_1" and
// "EXP_2" respectively. It installs the mock as store.DefaultStore for the
// duration of the test.
func installCaptureCmdTestExperiment(t *testing.T) {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	c := store.Config{
		Version: "phenix.sandia.gov/v1",
		Kind:    "Experiment",
		Metadata: store.ConfigMetadata{
			Name: "test-experiment",
		},
		Spec: map[string]any{
			"experimentName": "test-experiment",
			"baseDir":        t.TempDir(),
			"topology": map[string]any{
				"nodes": []map[string]any{
					{
						"type": "VirtualMachine",
						"general": map[string]any{
							"hostname":    "test-vm",
							"do_not_boot": false,
							"snapshot":    false,
						},
						"hardware": map[string]any{
							"vcpus":   2,
							"memory":  512,
							"os_type": "linux",
							"drives": []map[string]any{
								{
									"image":            "test.qc2",
									"inject_partition": 1,
								},
							},
						},
						"network": map[string]any{
							"interfaces": []map[string]any{
								{"name": "IF0", "vlan": "EXP_1"},
								{"name": "IF1", "vlan": "EXP_2"},
							},
						},
					},
				},
			},
		},
		Status: map[string]any{
			"startTime": "2024-01-01T00:00:00Z",
		},
	}

	m := store.NewMockStore(ctrl)
	m.EXPECT().Get(gomock.Any()).DoAndReturn(func(cfg *store.Config) error {
		*cfg = c

		return nil
	}).AnyTimes()

	store.DefaultStore = m //nolint:reassign // monkey patching for test
}

// These tests only exercise argument validation that happens before any
// call into the vm API package, so they don't require a running experiment
// or minimega instance.

func TestVMCaptureStartRequiresFourArgs(t *testing.T) {
	startCmd := findCaptureSubcommand(t, "start")

	cases := [][]string{
		nil,
		{"exp"},
		{"exp", "vm"},
		{"exp", "vm", "0"},
		{"exp", "vm", "0", "out.pcap", "extra"},
	}

	for _, args := range cases {
		if err := startCmd.RunE(startCmd, args); err == nil {
			t.Fatalf("expected error for args %v, got nil", args)
		}
	}
}

func TestVMCaptureStopRejectsWrongArgCount(t *testing.T) {
	stopCmd := findCaptureSubcommand(t, "stop")

	// Wrong number of args should fail before ever reaching the API layer.
	// Note that 2 (experiment + VM) and 3 (experiment + VM + iface) args are
	// both valid, so only counts outside that range are tested here.
	cases := [][]string{
		nil,
		{"exp"},
		{"exp", "vm", "0", "extra"},
	}

	for _, args := range cases {
		if err := stopCmd.RunE(stopCmd, args); err == nil {
			t.Fatalf("expected error for args %v, got nil", args)
		}
	}
}

// TestVMCaptureStartResolvesInterfaceByName verifies that the "start"
// subcommand accepts an interface name (as declared in the topology) in
// addition to a numeric index, and that it's correctly resolved to the
// underlying interface index before calling into minimega.
func TestVMCaptureStartResolvesInterfaceByName(t *testing.T) {
	installCaptureCmdTestExperiment(t)

	fake := &captureCmdTestMM{
		vmInfo: mm.VMs{
			{Name: "test-vm", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}},
		},
	}
	installCaptureCmdTestMM(t, fake)

	startCmd := findCaptureSubcommand(t, "start")

	args := []string{"test-experiment", "test-vm", "IF1", "out.pcap"}
	if err := startCmd.RunE(startCmd, args); err != nil {
		t.Fatalf("unexpected error starting capture by interface name: %v", err)
	}

	if fake.startCalls != 1 {
		t.Fatalf("expected StartVMCapture to be called once, got %d calls", fake.startCalls)
	}
}

// TestVMCaptureStartUnknownInterfaceName verifies that an interface name
// that doesn't match any interface declared in the topology produces an
// error without ever calling into minimega.
func TestVMCaptureStartUnknownInterfaceName(t *testing.T) {
	installCaptureCmdTestExperiment(t)

	fake := &captureCmdTestMM{
		vmInfo: mm.VMs{
			{Name: "test-vm", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}},
		},
	}
	installCaptureCmdTestMM(t, fake)

	startCmd := findCaptureSubcommand(t, "start")

	err := startCmd.RunE(startCmd, []string{"test-experiment", "test-vm", "bogus-iface", "out.pcap"})
	if err == nil {
		t.Fatal("expected error for unknown interface name")
	}

	if fake.startCalls != 0 {
		t.Fatalf("expected StartVMCapture to not be called, got %d calls", fake.startCalls)
	}
}

// TestVMCaptureStopResolvesInterfaceByName mirrors
// TestVMCaptureStartResolvesInterfaceByName for the "stop" subcommand.
func TestVMCaptureStopResolvesInterfaceByName(t *testing.T) {
	installCaptureCmdTestExperiment(t)

	fake := &captureCmdTestMM{
		vmInfo: mm.VMs{
			{Name: "test-vm", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}},
		},
		captures: []mm.Capture{
			{VM: "test-vm", Interface: 0, Filepath: "/tmp/0.pcap"},
			{VM: "test-vm", Interface: 1, Filepath: "/tmp/1.pcap"},
		},
	}
	installCaptureCmdTestMM(t, fake)

	stopCmd := findCaptureSubcommand(t, "stop")

	if err := stopCmd.RunE(stopCmd, []string{"test-experiment", "test-vm", "IF1"}); err != nil {
		t.Fatalf("unexpected error stopping capture by interface name: %v", err)
	}

	if fake.stopCalls != 1 {
		t.Fatalf("expected StopVMCapture to be called once, got %d calls", fake.stopCalls)
	}
}

func TestVMCaptureStopUnknownInterfaceName(t *testing.T) {
	installCaptureCmdTestExperiment(t)

	fake := &captureCmdTestMM{
		vmInfo: mm.VMs{
			{Name: "test-vm", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}},
		},
		captures: []mm.Capture{
			{VM: "test-vm", Interface: 0, Filepath: "/tmp/0.pcap"},
		},
	}
	installCaptureCmdTestMM(t, fake)

	stopCmd := findCaptureSubcommand(t, "stop")

	err := stopCmd.RunE(stopCmd, []string{"test-experiment", "test-vm", "bogus-iface"})
	if err == nil {
		t.Fatal("expected error for unknown interface name")
	}

	if fake.stopCalls != 0 {
		t.Fatalf("expected StopVMCapture to not be called, got %d calls", fake.stopCalls)
	}
}
