package common_test

import (
	"slices"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"phenix/util/common"
)

// ParseDiskCheck takes the three modes in any case, and "" as the default error mode.
func TestParseDiskCheck(t *testing.T) {
	tests := []struct {
		mode    string
		want    common.DiskCheck
		wantErr string
	}{
		{mode: "", want: common.DiskCheckError},
		{mode: "error", want: common.DiskCheckError},
		{mode: "WARN", want: common.DiskCheckWarn},
		{mode: "Off", want: common.DiskCheckOff},
		{mode: "loose", want: common.DiskCheckError, wantErr: "unknown disk check mode provided: loose"},
	}

	for _, tt := range tests {
		got, err := common.ParseDiskCheck(tt.mode)

		var gotErr string
		if err != nil {
			gotErr = err.Error()
		}

		if got != tt.want || gotErr != tt.wantErr {
			t.Errorf("ParseDiskCheck(%q) = %q, %q, want %q, %q", tt.mode, got, gotErr, tt.want, tt.wantErr)
		}
	}
}

// SetDiskCheck keeps the current mode when it refuses one, and ParseDiskCheck refuses exactly the values that are not a
// mode in some case.
func TestSetDiskCheckProperties(t *testing.T) {
	t.Cleanup(func() { _ = common.SetDiskCheck("") })

	rapid.Check(t, func(t *rapid.T) {
		before := rapid.SampledFrom([]string{"error", "warn", "off"}).Draw(t, "before")
		mode := rapid.OneOf(rapid.SampledFrom([]string{"", "Error", "wARN", "OFF", "none"}), rapid.String()).Draw(t, "mode")

		_ = common.SetDiskCheck(before)

		valid := slices.Contains([]string{"", "error", "warn", "off"}, strings.ToLower(mode))
		err := common.SetDiskCheck(mode)

		switch {
		case valid != (err == nil):
			t.Fatalf("SetDiskCheck(%q) error = %v, want valid %t", mode, err, valid)
		case !valid && common.CurrentDiskCheck() != common.DiskCheck(before):
			t.Fatalf("SetDiskCheck(%q) changed the mode to %q, want %q kept", mode, common.CurrentDiskCheck(), before)
		}
	})
}

// keepGlobals restores the mode and base globals when the test ends.
func keepGlobals(t *testing.T) {
	t.Helper()

	bridge, deploy := common.BridgeMode, common.DeployMode
	phenixBase, mountBase, suffixes := common.PhenixBase, common.MountBase, common.HostnameSuffixes

	t.Cleanup(func() {
		_ = common.SetBridgeMode(string(bridge))
		_ = common.SetDeployMode(string(deploy))
		common.PhenixBase, common.MountBase = phenixBase, mountBase //nolint:reassign // restore
		common.HostnameSuffixes = suffixes                          //nolint:reassign // restore
	})
}

// ParseBridgeMode takes the two modes in any case, and "" as the current mode.
func TestParseBridgeMode(t *testing.T) {
	keepGlobals(t)

	_ = common.SetBridgeMode("auto")

	tests := []struct {
		mode    string
		want    common.BridgingMode
		wantErr bool
	}{
		{mode: "manual", want: common.BridgeModeManual},
		{mode: "AUTO", want: common.BridgeModeAuto},
		{mode: "Manual", want: common.BridgeModeManual},
		{mode: "", want: common.BridgeModeAuto},
		{mode: "bridged", want: common.BridgeModeUnset, wantErr: true},
	}

	for _, tt := range tests {
		got, err := common.ParseBridgeMode(tt.mode)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("ParseBridgeMode(%q) = %q, %v, want %q, error %t", tt.mode, got, err, tt.want, tt.wantErr)
		}
	}
}

// SetBridgeMode sets a mode it parses and keeps the current mode when it refuses one.
func TestSetBridgeMode(t *testing.T) {
	keepGlobals(t)

	_ = common.SetBridgeMode("manual")

	if err := common.SetBridgeMode("Auto"); err != nil || common.BridgeMode != common.BridgeModeAuto {
		t.Errorf("SetBridgeMode(Auto) = %v, mode %q, want nil, %q", err, common.BridgeMode, common.BridgeModeAuto)
	}

	if err := common.SetBridgeMode(""); err != nil || common.BridgeMode != common.BridgeModeAuto {
		t.Errorf("SetBridgeMode(\"\") = %v, mode %q, want nil, %q", err, common.BridgeMode, common.BridgeModeAuto)
	}

	if err := common.SetBridgeMode("none"); err == nil || common.BridgeMode != common.BridgeModeAuto {
		t.Errorf("SetBridgeMode(none) = %v, mode %q, want an error, %q", err, common.BridgeMode, common.BridgeModeAuto)
	}
}

// ParseDeployMode takes the three modes in any case, and "" as the current mode.
func TestParseDeployMode(t *testing.T) {
	keepGlobals(t)

	_ = common.SetDeployMode("all")

	tests := []struct {
		mode    string
		want    common.DeploymentMode
		wantErr bool
	}{
		{mode: "no-headnode", want: common.DeployModeNoHeadnode},
		{mode: "ONLY-HEADNODE", want: common.DeployModeOnlyHeadnode},
		{mode: "All", want: common.DeployModeAll},
		{mode: "", want: common.DeployModeAll},
		{mode: "headnode", want: common.DeployModeUnset, wantErr: true},
	}

	for _, tt := range tests {
		got, err := common.ParseDeployMode(tt.mode)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("ParseDeployMode(%q) = %q, %v, want %q, error %t", tt.mode, got, err, tt.want, tt.wantErr)
		}
	}
}

// SetDeployMode sets a mode it parses and keeps the current mode when it refuses one.
func TestSetDeployMode(t *testing.T) {
	keepGlobals(t)

	_ = common.SetDeployMode("no-headnode")

	if err := common.SetDeployMode("only-headnode"); err != nil || common.DeployMode != common.DeployModeOnlyHeadnode {
		t.Errorf("SetDeployMode(only-headnode) = %v, mode %q, want nil, %q", err, common.DeployMode, common.DeployModeOnlyHeadnode)
	}

	if err := common.SetDeployMode("some"); err == nil || common.DeployMode != common.DeployModeOnlyHeadnode {
		t.Errorf("SetDeployMode(some) = %v, mode %q, want an error, %q", err, common.DeployMode, common.DeployModeOnlyHeadnode)
	}
}

// MountDir is MountBase when set, else the mounts folder in PhenixBase.
func TestMountDir(t *testing.T) {
	keepGlobals(t)

	common.PhenixBase = "/srv/phenix" //nolint:reassign // test configuration

	common.MountBase = "" //nolint:reassign // test configuration
	if got := common.MountDir(); got != "/srv/phenix/mounts" {
		t.Errorf("MountDir() with no MountBase = %q, want %q", got, "/srv/phenix/mounts")
	}

	common.MountBase = "/data/mounts" //nolint:reassign // test configuration
	if got := common.MountDir(); got != "/data/mounts" {
		t.Errorf("MountDir() with MountBase %q = %q, want %q", common.MountBase, got, "/data/mounts")
	}
}

// TrimHostnameSuffixes removes each of the comma-separated suffixes once, in their order.
func TestTrimHostnameSuffixes(t *testing.T) {
	keepGlobals(t)

	tests := []struct {
		suffixes string
		name     string
		want     string
	}{
		{suffixes: "", name: "compute1", want: "compute1"},
		{suffixes: "-minimega", name: "compute1-minimega", want: "compute1"},
		{suffixes: "-minimega", name: "minimega-compute1", want: "minimega-compute1"},
		{suffixes: "-minimega,-phenix", name: "head-phenix", want: "head"},
		{suffixes: "-minimega,-phenix", name: "head-minimega-phenix", want: "head-minimega"},
		{suffixes: "-phenix,-minimega", name: "head-minimega-phenix", want: "head"},
		{suffixes: "-minimega", name: "head-minimega-minimega", want: "head-minimega"},
	}

	for _, tt := range tests {
		common.HostnameSuffixes = tt.suffixes //nolint:reassign // test configuration

		if got := common.TrimHostnameSuffixes(tt.name); got != tt.want {
			t.Errorf("TrimHostnameSuffixes(%q) with suffixes %q = %q, want %q", tt.name, tt.suffixes, got, tt.want)
		}
	}
}
