package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"pgregory.net/rapid"

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

			if got := StartupUsesC2(node); got != tt.wantCommands {
				t.Errorf("StartupUsesC2() = %v, want %v as PreStart sends commands", got, tt.wantCommands)
			}

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

// StartupUsesC2 is false for the nodes whose startup scripts PreStart skips: routers, nodes with the default apps
// off, and operating systems it makes no scripts for.
func TestStartupUsesC2Skips(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		modify func(*v1.Node)
		want   bool
	}{
		"windows":           {modify: func(n *v1.Node) { n.HardwareF.OSTypeF = osWindows }, want: true},
		"router":            {modify: func(n *v1.Node) { n.TypeF = "Router" }},
		"default apps off":  {modify: func(n *v1.Node) { n.AnnotationsF[defaultAppsAnnotation] = false }},
		"other os":          {modify: func(n *v1.Node) { n.HardwareF.OSTypeF = "plan9" }},
		"linux, C2 enabled": {modify: func(*v1.Node) {}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			node := newStartupTestNode(t, "n1", osLinux, true, 1)
			node.AnnotationsF = map[string]any{startupViaCCAnnotation: true}
			tc.modify(node)

			if got := StartupUsesC2(node); got != tc.want {
				t.Errorf("StartupUsesC2() = %v, want %v", got, tc.want)
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

// startupCommand is a node command and whether the startup app generated it for that node.
type startupCommand struct {
	cmd   string
	stale bool
}

// genStartupCommand draws a command the startup app generated for host in exp1, or one of another host or
// experiment, or a user's command.
func genStartupCommand(host string) *rapid.Generator[startupCommand] {
	exp1 := &types.Experiment{Spec: &v1.ExperimentSpec{ExperimentNameF: "exp1"}}
	exp2 := &types.Experiment{Spec: &v1.ExperimentSpec{ExperimentNameF: "exp2"}}

	return rapid.Custom(func(t *rapid.T) startupCommand {
		if rapid.Bool().Draw(t, "user") {
			return startupCommand{cmd: rapid.SampledFrom([]string{
				"exec df -h", "send exp1/notes.txt", "exec-once bash /tmp/miniccc/files/exp1/" + host + "-custom.sh",
			}).Draw(t, "cmd")}
		}

		owner := rapid.SampledFrom([]string{host, host + "-x", "other"}).Draw(t, "owner")
		exp := rapid.SampledFrom([]*types.Experiment{exp1, exp2}).Draw(t, "exp")
		script := rapid.SampledFrom(startupC2ScriptNames(owner)).Draw(t, "script")
		executor := rapid.SampledFrom([]startupC2Executor{startupC2Linux, startupC2Windows}).Draw(t, "executor")

		send, exec, _ := startupC2Commands(exp, filepath.Join("/startup", script), executor)
		cmd := rapid.SampledFrom([]string{send, exec}).Draw(t, "cmd")

		return startupCommand{cmd: cmd, stale: owner == host && exp == exp1}
	})
}

// removeStartupC2Commands drops exactly the commands generated for the node, keeps the rest in order, and
// changes nothing when run again.
func TestRemoveStartupC2CommandsProperties(t *testing.T) {
	t.Parallel()

	exp := &types.Experiment{Spec: &v1.ExperimentSpec{ExperimentNameF: "exp1"}}

	rapid.Check(t, func(t *rapid.T) {
		host := rapid.SampledFrom([]string{"linux1", "win1", "a-b"}).Draw(t, "host")
		commands := rapid.SliceOfN(genStartupCommand(host), 0, 8).Draw(t, "commands")

		node := &v1.Node{GeneralF: &v1.General{HostnameF: host}}

		var want []string

		for _, c := range commands {
			node.CommandsF = append(node.CommandsF, c.cmd)

			if !c.stale {
				want = append(want, c.cmd)
			}
		}

		removeStartupC2Commands(exp, node)

		if diff := cmp.Diff(want, node.CommandsF, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("removeStartupC2Commands(%s) commands mismatch (-want +got):\n%s", host, diff)
		}

		removeStartupC2Commands(exp, node)

		if diff := cmp.Diff(want, node.CommandsF, cmpopts.EquateEmpty()); diff != "" {
			t.Fatalf("removeStartupC2Commands(%s) run again mismatch (-want +got):\n%s", host, diff)
		}
	})
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
