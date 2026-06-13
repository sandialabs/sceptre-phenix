package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

func TestStartupPreStartC2TriggerMatrix(t *testing.T) {
	tests := []struct {
		name             string
		annotations      map[string]any
		snapshot         bool
		injectPartition  int
		wantCommands     bool
		wantStartupInj   bool
		wantStagedScript bool
	}{
		{
			name:             "annotation enabled on injectable node",
			annotations:      map[string]any{startupViaCCAnnotation: true},
			snapshot:         true,
			injectPartition:  1,
			wantCommands:     true,
			wantStartupInj:   false,
			wantStagedScript: true,
		},
		{
			name:             "inject partition zero",
			snapshot:         true,
			injectPartition:  0,
			wantCommands:     true,
			wantStartupInj:   true,
			wantStagedScript: true,
		},
		{
			name:            "snapshot false without annotation",
			snapshot:        false,
			injectPartition: 1,
			wantCommands:    false,
			wantStartupInj:  true,
		},
		{
			name:            "normal injectable node",
			snapshot:        true,
			injectPartition: 1,
			wantCommands:    false,
			wantStartupInj:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := newStartupTestNode(t, "linux1", osLinux, tt.snapshot, tt.injectPartition)
			node.AnnotationsF = tt.annotations

			exp, mmDir := newStartupTestExperiment(t, node, nil)
			runStartupPreStart(t, exp, mmDir)

			wantCommands := []string{
				"send exp1/linux1-hostname.sh",
				"exec-once bash /tmp/miniccc/files/exp1/linux1-hostname.sh",
				"send exp1/linux1-timezone.sh",
				"exec-once bash /tmp/miniccc/files/exp1/linux1-timezone.sh",
				"send exp1/linux1-interfaces.sh",
				"exec-once bash /tmp/miniccc/files/exp1/linux1-interfaces.sh",
			}

			for _, command := range wantCommands {
				if got := hasCommand(node, command); got != tt.wantCommands {
					t.Fatalf("command %q present = %v, want %v", command, got, tt.wantCommands)
				}
			}

			for _, dst := range []string{linuxHostnameInjectDst, linuxTimezoneInjectDst, linuxIfaceInjectDst} {
				if got := hasInjection(node, dst); got != tt.wantStartupInj {
					t.Fatalf("injection %q present = %v, want %v", dst, got, tt.wantStartupInj)
				}
			}

			staged := filepath.Join(mmDir, "exp1", "linux1-hostname.sh")
			_, err := os.Stat(staged)
			if tt.wantStagedScript && err != nil {
				t.Fatalf("expected staged script %s: %v", staged, err)
			}
			if !tt.wantStagedScript && !os.IsNotExist(err) {
				t.Fatalf("unexpected staged script %s", staged)
			}
		})
	}
}

func TestStartupPreStartWindowsC2Commands(t *testing.T) {
	node := newStartupTestNode(t, "win1", osWindows, true, 0)
	exp, mmDir := newStartupTestExperiment(t, node, nil)

	runStartupPreStart(t, exp, mmDir)

	wantSend := "send exp1/win1-startup.ps1"
	wantExec := "exec-once cmd /c 'powershell.exe -noprofile -executionpolicy bypass -file /tmp/miniccc/files/exp1/win1-startup.ps1'"

	if !hasCommand(node, wantSend) {
		t.Fatalf("missing command %q", wantSend)
	}
	if !hasCommand(node, wantExec) {
		t.Fatalf("missing command %q", wantExec)
	}
	if !hasInjection(node, windowsStartupInjectDst) {
		t.Fatalf("expected Windows startup script injection to remain for inject partition 0")
	}
	if hasInjection(node, legacyWindowsStartupWrapperDst) {
		t.Fatalf("unexpected Windows startup wrapper injection")
	}
	if hasInjection(node, windowsSchedulerDst) || hasInjection(node, "/"+windowsSchedulerDst) {
		t.Fatalf("unexpected Windows Start Menu scheduler injection")
	}

	staged := filepath.Join(mmDir, "exp1", "win1-startup.ps1")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("expected staged Windows script %s: %v", staged, err)
	}
}

func TestStartupPreStartAnnotationPreservesUnrelatedInjections(t *testing.T) {
	node := newStartupTestNode(t, "linux1", osLinux, true, 1)
	node.AnnotationsF = map[string]any{startupViaCCAnnotation: true}
	node.InjectionsF = []*v1.Injection{
		{SrcF: "user-file", DstF: "/etc/user-file"},
		{SrcF: "old-hostname", DstF: linuxHostnameInjectDst},
		{SrcF: "old-wrapper", DstF: legacyWindowsStartupWrapperDst},
		{SrcF: "old-scheduler", DstF: windowsSchedulerDst},
	}

	exp, mmDir := newStartupTestExperiment(t, node, nil)
	runStartupPreStart(t, exp, mmDir)

	if !hasInjection(node, "/etc/user-file") {
		t.Fatalf("expected unrelated user injection to remain")
	}
	if hasInjection(node, linuxHostnameInjectDst) {
		t.Fatalf("unexpected startup app injection")
	}
	if hasInjection(node, legacyWindowsStartupWrapperDst) {
		t.Fatalf("unexpected stale Windows startup wrapper injection")
	}
	if hasInjection(node, windowsSchedulerDst) {
		t.Fatalf("unexpected stale scheduler injection")
	}
	if !hasCommand(node, "send exp1/linux1-hostname.sh") {
		t.Fatalf("expected startup C2 send command")
	}
}

func TestStartupPreStartRemovesLegacyWindowsStartupInjectionsWithoutAnnotation(t *testing.T) {
	node := newStartupTestNode(t, "win1", osWindows, true, 1)
	node.InjectionsF = []*v1.Injection{
		{SrcF: "user-file", DstF: "/etc/user-file"},
		{SrcF: "old-wrapper", DstF: legacyWindowsStartupWrapperDst},
		{SrcF: "old-scheduler", DstF: windowsSchedulerDst},
		{SrcF: "old-scheduler-abs", DstF: "/" + windowsSchedulerDst},
	}

	exp, mmDir := newStartupTestExperiment(t, node, nil)
	runStartupPreStart(t, exp, mmDir)

	if !hasInjection(node, "/etc/user-file") {
		t.Fatalf("expected unrelated user injection to remain")
	}
	if !hasInjection(node, windowsStartupInjectDst) {
		t.Fatalf("expected current Windows startup injection to remain")
	}
	if hasInjection(node, legacyWindowsStartupWrapperDst) {
		t.Fatalf("unexpected stale Windows startup wrapper injection")
	}
	if hasInjection(node, windowsSchedulerDst) || hasInjection(node, "/"+windowsSchedulerDst) {
		t.Fatalf("unexpected stale Windows Start Menu scheduler injection")
	}
	if hasCommand(node, "send exp1/win1-startup.ps1") {
		t.Fatalf("unexpected startup C2 command")
	}
}

func TestStartupPreStartC2CommandsAreIdempotent(t *testing.T) {
	node := newStartupTestNode(t, "linux1", osLinux, true, 0)
	exp, mmDir := newStartupTestExperiment(t, node, nil)

	runStartupPreStart(t, exp, mmDir)
	first := append([]string(nil), node.CommandsF...)

	runStartupPreStart(t, exp, mmDir)
	second := append([]string(nil), node.CommandsF...)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("commands changed after second PreStart:\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if len(second) != 6 {
		t.Fatalf("expected 6 startup C2 commands, got %d: %#v", len(second), second)
	}
}

func TestStartupPreStartRemovesStaleC2Commands(t *testing.T) {
	node := newStartupTestNode(t, "linux1", osLinux, true, 1)
	node.CommandsF = []string{
		"send exp1/linux1-hostname.sh",
		"exec-once bash /tmp/miniccc/files/exp1/linux1-hostname.sh",
		"exec df -h",
	}

	exp, mmDir := newStartupTestExperiment(t, node, nil)
	runStartupPreStart(t, exp, mmDir)

	if hasCommand(node, "send exp1/linux1-hostname.sh") {
		t.Fatalf("unexpected stale startup C2 send command")
	}
	if hasCommand(node, "exec-once bash /tmp/miniccc/files/exp1/linux1-hostname.sh") {
		t.Fatalf("unexpected stale startup C2 exec command")
	}
	if !hasCommand(node, "exec df -h") {
		t.Fatalf("expected user command to remain")
	}
}

func TestStartupViaCCAnnotationValues(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{name: "bool true", val: true, want: true},
		{name: "bool false", val: false, want: false},
		{name: "string true", val: "true", want: true},
		{name: "string false", val: "false", want: false},
		{name: "string zero", val: "0", want: false},
		{name: "int zero", val: 0, want: false},
		{name: "int one", val: 1, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := newStartupTestNode(t, "linux1", osLinux, true, 1)
			node.AnnotationsF = map[string]any{startupViaCCAnnotation: tt.val}

			if got := startupViaCCEnabled(node); got != tt.want {
				t.Fatalf("startupViaCCEnabled = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStartupPreStartLinuxDomainScriptUsesC2(t *testing.T) {
	node := newStartupTestNode(t, "linux1", osLinux, true, 0)
	scenario := &v2.ScenarioSpec{AppsF: []*v2.ScenarioApp{
		{
			NameF: "startup",
			HostsF: []*v2.ScenarioAppHost{
				{
					HostnameF: "linux1",
					MetadataF: map[string]any{
						"domain_controller": map[string]any{
							"username": "admin",
							"password": "password",
							"domain":   "example.test",
						},
					},
				},
			},
		},
	}}

	exp, mmDir := newStartupTestExperiment(t, node, scenario)
	runStartupPreStart(t, exp, mmDir)

	if !hasCommand(node, "send exp1/linux1-domain.sh") {
		t.Fatalf("expected domain script send command")
	}
	if !hasCommand(node, "exec-once bash /tmp/miniccc/files/exp1/linux1-domain.sh") {
		t.Fatalf("expected domain script exec command")
	}
	if _, err := os.Stat(filepath.Join(mmDir, "exp1", "linux1-domain.sh")); err != nil {
		t.Fatalf("expected staged domain script: %v", err)
	}
}

// newStartupTestNode builds a minimal VM node with a real test disk path.
func newStartupTestNode(t *testing.T, hostname, osType string, snapshot bool, injectPartition int) *v1.Node {
	t.Helper()

	image := filepath.Join(t.TempDir(), hostname+".qc2")
	if err := os.WriteFile(image, []byte("disk"), 0o600); err != nil {
		t.Fatalf("creating test image: %v", err)
	}

	return &v1.Node{
		TypeF: "VirtualMachine",
		GeneralF: &v1.General{
			HostnameF:  hostname,
			SnapshotF:  &snapshot,
			DoNotBootF: boolPtr(false),
		},
		HardwareF: &v1.Hardware{
			OSTypeF: osType,
			DrivesF: []*v1.Drive{
				{
					ImageF:           image,
					InjectPartitionF: &injectPartition,
				},
			},
		},
		NetworkF: &v1.Network{},
	}
}

// newStartupTestExperiment builds a minimal experiment and redirects minimega file staging.
func newStartupTestExperiment(t *testing.T, node *v1.Node, scenario *v2.ScenarioSpec) (*types.Experiment, string) {
	t.Helper()

	root := t.TempDir()
	mmDir := filepath.Join(root, "images")

	oldStartupMMFullPath := startupMMFullPath
	startupMMFullPath = func(rel string) string {
		return filepath.Join(mmDir, filepath.FromSlash(rel))
	}

	t.Cleanup(func() {
		startupMMFullPath = oldStartupMMFullPath
	})

	spec := &v1.ExperimentSpec{
		ExperimentNameF: "exp1",
		BaseDirF:        filepath.Join(root, "experiments", "exp1"),
		TopologyF:       &v1.TopologySpec{NodesF: []*v1.Node{node}},
		ScenarioF:       scenario,
	}
	if err := spec.Init(); err != nil {
		t.Fatalf("initializing test experiment spec: %v", err)
	}

	status := &v1.ExperimentStatus{}
	if err := status.Init(); err != nil {
		t.Fatalf("initializing test experiment status: %v", err)
	}

	return &types.Experiment{
		Metadata: store.ConfigMetadata{Name: "exp1"},
		Spec:     spec,
		Status:   status,
	}, mmDir
}

// runStartupPreStart creates the staged file directory and invokes startup PreStart.
func runStartupPreStart(t *testing.T, exp *types.Experiment, mmDir string) {
	t.Helper()

	if err := os.MkdirAll(mmDir, 0o750); err != nil {
		t.Fatalf("creating minimega file directory: %v", err)
	}

	if err := (Startup{}).PreStart(context.Background(), exp); err != nil {
		t.Fatalf("running startup PreStart: %v", err)
	}
}

// hasCommand reports whether the node contains command.
func hasCommand(node *v1.Node, command string) bool {
	for _, got := range node.CommandsF {
		if got == command {
			return true
		}
	}

	return false
}

// hasInjection reports whether the node contains an injection targeting dst.
func hasInjection(node *v1.Node, dst string) bool {
	for _, injection := range node.InjectionsF {
		if injection.DstF == dst {
			return true
		}
	}

	return false
}

// boolPtr returns a pointer to v.
func boolPtr(v bool) *bool {
	return &v
}
