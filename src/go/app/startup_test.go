package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
	v2 "phenix/types/version/v2"
)

func TestStartupPostStartAutoMount(t *testing.T) {
	mountErr := errors.New("mount failed")

	tests := []struct {
		name        string
		annotation  any
		setAnnot    bool
		nodeType    string
		doNotBoot   bool
		userDelay   bool
		osType      string
		dryRun      bool
		mountErr    error
		wantMounts  int
		wantErrText string
	}{
		{
			name:       "enabled bool",
			annotation: true,
			setAnnot:   true,
			wantMounts: 1,
		},
		{
			name:       "disabled bool",
			annotation: false,
			setAnnot:   true,
		},
		{
			name: "omitted",
		},
		{
			name:       "dry run",
			annotation: true,
			setAnnot:   true,
			dryRun:     true,
		},
		{
			name:        "invalid string value",
			annotation:  "true",
			setAnnot:    true,
			wantErrText: "phenix/auto-mount annotation for node linux1 must be a boolean",
		},
		{
			name:        "invalid int value",
			annotation:  1,
			setAnnot:    true,
			wantErrText: "phenix/auto-mount annotation for node linux1 must be a boolean",
		},
		{
			name:       "VM does not boot skipped",
			annotation: true,
			setAnnot:   true,
			doNotBoot:  true,
			wantMounts: 0,
		},
		{
			name:       "non-VM node skipped",
			annotation: true,
			setAnnot:   true,
			nodeType:   "Switch",
			wantMounts: 0,
		},
		{
			name:       "user-delayed VM skipped",
			annotation: true,
			setAnnot:   true,
			userDelay:  true,
			wantMounts: 0,
		},
		{
			name:       "minirouter skipped",
			annotation: true,
			setAnnot:   true,
			osType:     "minirouter",
			wantMounts: 0,
		},
		{
			name:        "mount failure",
			annotation:  true,
			setAnnot:    true,
			mountErr:    mountErr,
			wantMounts:  1,
			wantErrText: "auto-mounting VM linux1: mount failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osType := osLinux
			if tt.osType != "" {
				osType = tt.osType
			}

			node := newStartupTestNode(t, "linux1", osType, true, 1)
			if tt.nodeType != "" {
				node.TypeF = tt.nodeType
			}
			if tt.setAnnot {
				node.AnnotationsF = map[string]any{autoMountAnnotation: tt.annotation}
			}
			node.GeneralF.DoNotBootF = boolPtr(tt.doNotBoot)
			if tt.userDelay {
				node.DelayF = &v1.Delay{UserF: true}
			}

			exp, _ := newStartupTestExperiment(t, node, nil)

			var mounts [][2]string
			oldStartupMountFilesystem := startupMountFilesystem
			startupMountFilesystem = func(_ context.Context, expName, vmName string) error {
				mounts = append(mounts, [2]string{expName, vmName})

				return tt.mountErr
			}
			t.Cleanup(func() {
				startupMountFilesystem = oldStartupMountFilesystem
			})

			err := (&Startup{dryRun: tt.dryRun}).PostStart(t.Context(), exp)
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("PostStart() error = %v", err)
				}
			} else if err == nil || err.Error() != tt.wantErrText {
				t.Fatalf("PostStart() error = %v, want %q", err, tt.wantErrText)
			}

			if len(mounts) != tt.wantMounts {
				t.Fatalf("PostStart() mount calls = %d, want %d", len(mounts), tt.wantMounts)
			}

			if tt.wantMounts > 0 && mounts[0] != [2]string{"exp1", "linux1"} {
				t.Fatalf("PostStart() mount target = %v, want [exp1 linux1]", mounts[0])
			}
		})
	}
}

// TestTriggerAutoMount verifies the exported entry point used to mount a
// node's filesystem once a user-delayed node has been manually started. It
// must NOT skip user-delayed nodes (unlike the automatic post-start pass),
// since it is specifically invoked to handle that case, but should still
// respect the other skip conditions (do-not-boot, minirouter) and the
// boolean-only annotation requirement.
func TestTriggerAutoMount(t *testing.T) {
	mountErr := errors.New("mount failed")

	tests := []struct {
		name        string
		annotation  any
		setAnnot    bool
		nodeType    string
		doNotBoot   bool
		osType      string
		mountErr    error
		wantMounts  int
		wantErrText string
	}{
		{
			name:       "user-delayed node mounted once triggered",
			annotation: true,
			setAnnot:   true,
			wantMounts: 1,
		},
		{
			name:     "disabled node not mounted",
			setAnnot: false,
		},
		{
			name:        "invalid annotation value errors",
			annotation:  "true",
			setAnnot:    true,
			wantErrText: "phenix/auto-mount annotation for node linux1 must be a boolean",
		},
		{
			name:       "do-not-boot node skipped",
			annotation: true,
			setAnnot:   true,
			doNotBoot:  true,
			wantMounts: 0,
		},
		{
			name:       "non-VM node skipped",
			annotation: true,
			setAnnot:   true,
			nodeType:   "Firewall",
			wantMounts: 0,
		},
		{
			name:       "minirouter node skipped",
			annotation: true,
			setAnnot:   true,
			osType:     "minirouter",
			wantMounts: 0,
		},
		{
			name:        "mount failure propagated",
			annotation:  true,
			setAnnot:    true,
			mountErr:    mountErr,
			wantMounts:  1,
			wantErrText: "auto-mounting VM linux1: mount failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			osType := osLinux
			if tt.osType != "" {
				osType = tt.osType
			}

			node := newStartupTestNode(t, "linux1", osType, true, 1)
			if tt.nodeType != "" {
				node.TypeF = tt.nodeType
			}
			if tt.setAnnot {
				node.AnnotationsF = map[string]any{autoMountAnnotation: tt.annotation}
			}
			node.GeneralF.DoNotBootF = boolPtr(tt.doNotBoot)
			node.DelayF = &v1.Delay{UserF: true}

			var mounts [][2]string
			oldStartupMountFilesystem := startupMountFilesystem
			startupMountFilesystem = func(_ context.Context, expName, vmName string) error {
				mounts = append(mounts, [2]string{expName, vmName})

				return tt.mountErr
			}
			t.Cleanup(func() {
				startupMountFilesystem = oldStartupMountFilesystem
			})

			err := TriggerAutoMount(t.Context(), "exp1", node)
			if tt.wantErrText == "" {
				if err != nil {
					t.Fatalf("TriggerAutoMount() error = %v", err)
				}
			} else if err == nil || err.Error() != tt.wantErrText {
				t.Fatalf("TriggerAutoMount() error = %v, want %q", err, tt.wantErrText)
			}

			if len(mounts) != tt.wantMounts {
				t.Fatalf("TriggerAutoMount() mount calls = %d, want %d", len(mounts), tt.wantMounts)
			}
		})
	}
}

func TestStartupInitDryRun(t *testing.T) {
	startup := new(Startup)
	if err := startup.Init(DryRun(true)); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	if !startup.dryRun {
		t.Fatal("Init() did not retain dry-run setting")
	}
}

func TestStartupCleanupAutoMounts(t *testing.T) {
	unmountErr := errors.New("unmount failed")

	enabled1 := newStartupTestNode(t, "linux1", osLinux, true, 1)
	enabled1.AnnotationsF = map[string]any{autoMountAnnotation: true}
	enabled2 := newStartupTestNode(t, "linux2", osLinux, true, 1)
	enabled2.AnnotationsF = map[string]any{autoMountAnnotation: true}
	disabled := newStartupTestNode(t, "linux3", osLinux, true, 1)
	disabled.AnnotationsF = map[string]any{autoMountAnnotation: false}
	doNotBoot := newStartupTestNode(t, "linux4", osLinux, true, 1)
	doNotBoot.AnnotationsF = map[string]any{autoMountAnnotation: true}
	doNotBoot.GeneralF.DoNotBootF = boolPtr(true)
	nonVM := newStartupTestNode(t, "switch1", osLinux, true, 1)
	nonVM.AnnotationsF = map[string]any{autoMountAnnotation: true}
	nonVM.TypeF = "Switch"
	minirouter := newStartupTestNode(t, "router1", "minirouter", true, 1)
	minirouter.AnnotationsF = map[string]any{autoMountAnnotation: true}

	exp, _ := newStartupTestExperiment(t, enabled1, nil)
	exp.Spec.(*v1.ExperimentSpec).TopologyF.NodesF = []*v1.Node{ //nolint:forcetypeassert // test fixture
		enabled1,
		enabled2,
		disabled,
		doNotBoot,
		nonVM,
		minirouter,
	}

	var unmounts [][2]string
	oldStartupUnmountFilesystem := startupUnmountFilesystem
	startupUnmountFilesystem = func(_ context.Context, expName, vmName string) error {
		unmounts = append(unmounts, [2]string{expName, vmName})
		if vmName == "linux1" {
			return unmountErr
		}

		return nil
	}
	t.Cleanup(func() {
		startupUnmountFilesystem = oldStartupUnmountFilesystem
	})

	err := (&Startup{}).Cleanup(t.Context(), exp)
	if !errors.Is(err, unmountErr) {
		t.Fatalf("Cleanup() error = %v, want wrapped %v", err, unmountErr)
	}

	want := [][2]string{{"exp1", "linux1"}, {"exp1", "linux2"}}
	if !reflect.DeepEqual(unmounts, want) {
		t.Fatalf("Cleanup() unmounts = %v, want %v", unmounts, want)
	}
}

func TestStartupCleanupDryRun(t *testing.T) {
	node := newStartupTestNode(t, "linux1", osLinux, true, 1)
	node.AnnotationsF = map[string]any{autoMountAnnotation: true}
	exp, _ := newStartupTestExperiment(t, node, nil)

	oldStartupUnmountFilesystem := startupUnmountFilesystem
	startupUnmountFilesystem = func(context.Context, string, string) error {
		t.Fatal("Cleanup() unmounted during dry run")

		return nil
	}
	t.Cleanup(func() {
		startupUnmountFilesystem = oldStartupUnmountFilesystem
	})

	if err := (&Startup{dryRun: true}).Cleanup(t.Context(), exp); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
}

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
	if hasInjection(node, windowsStartupWrapperDst) {
		t.Fatalf("unexpected Windows startup wrapper injection with C2 delivery")
	}
	if hasInjection(node, windowsSchedulerDst) || hasInjection(node, "/"+windowsSchedulerDst) {
		t.Fatalf("unexpected Windows Start Menu scheduler injection with C2 delivery")
	}

	staged := filepath.Join(mmDir, "exp1", "win1-startup.ps1")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("expected staged Windows script %s: %v", staged, err)
	}
}

func TestStartupPreStartWindowsWrapperInjections(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]any
		wantWrapper bool
	}{
		{
			name:        "disk injection",
			wantWrapper: true,
		},
		{
			name:        "startup via cc suppresses wrapper",
			annotations: map[string]any{startupViaCCAnnotation: true},
			wantWrapper: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := newStartupTestNode(t, "win1", osWindows, true, 1)
			node.AnnotationsF = tt.annotations

			exp, mmDir := newStartupTestExperiment(t, node, nil)
			runStartupPreStart(t, exp, mmDir)

			for _, dst := range []string{windowsStartupWrapperDst, windowsSchedulerDst} {
				if got := hasInjection(node, dst); got != tt.wantWrapper {
					t.Fatalf("injection %q present = %v, want %v", dst, got, tt.wantWrapper)
				}
			}

			if !tt.wantWrapper {
				return
			}

			startupDir := filepath.Join(exp.Spec.BaseDir(), "startup")

			for _, asset := range []string{windowsStartupWrapperAsset, windowsSchedulerAsset} {
				if _, err := os.Stat(filepath.Join(startupDir, asset)); err != nil {
					t.Fatalf("expected restored asset %s: %v", asset, err)
				}
			}
		})
	}
}

func TestStartupPreStartAnnotationPreservesUnrelatedInjections(t *testing.T) {
	node := newStartupTestNode(t, "linux1", osLinux, true, 1)
	node.AnnotationsF = map[string]any{startupViaCCAnnotation: true}
	node.InjectionsF = []*v1.Injection{
		{SrcF: "user-file", DstF: "/etc/user-file"},
		{SrcF: "old-hostname", DstF: linuxHostnameInjectDst},
		{SrcF: "old-wrapper", DstF: windowsStartupWrapperDst},
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
	if hasInjection(node, windowsStartupWrapperDst) {
		t.Fatalf("unexpected Windows startup wrapper injection")
	}
	if hasInjection(node, windowsSchedulerDst) {
		t.Fatalf("unexpected scheduler injection")
	}
	if !hasCommand(node, "send exp1/linux1-hostname.sh") {
		t.Fatalf("expected startup C2 send command")
	}
}

func TestStartupPreStartRemovesWindowsWrapperInjectionsForC2Delivery(t *testing.T) {
	node := newStartupTestNode(t, "win1", osWindows, true, 0)
	node.InjectionsF = []*v1.Injection{
		{SrcF: "user-file", DstF: "/etc/user-file"},
		{SrcF: "old-wrapper", DstF: windowsStartupWrapperDst},
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
	if hasInjection(node, windowsStartupWrapperDst) {
		t.Fatalf("unexpected Windows startup wrapper injection with C2 delivery")
	}
	if hasInjection(node, windowsSchedulerDst) || hasInjection(node, "/"+windowsSchedulerDst) {
		t.Fatalf("unexpected Windows Start Menu scheduler injection with C2 delivery")
	}
	if !hasCommand(node, "send exp1/win1-startup.ps1") {
		t.Fatalf("expected startup C2 command")
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
	return slices.Contains(node.CommandsF, command)
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
