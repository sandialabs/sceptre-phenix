package soh

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"phenix/types"
	"phenix/util/plog"
)

const (
	captureHost = "plc"
	noBootHost  = "ignition"
	noImageHost = "controlthings-elk"
	missingHost = "historian"

	skipCaptureMsg   = "skipping packet capture for host"
	reasonNoBoot     = "node will not boot"
	reasonNotInTopo  = "host is not in the topology"
	captureMonitorVM = captureHost + "-soh-monitor"
)

// captureExperiment builds a topology where captureHost boots, noBootHost is
// set to not boot, and noImageHost is marked to not boot the way the startup
// app marks a node whose disk image is missing. captureHosts lists hosts, each
// with interface IF0.
func captureExperiment(t *testing.T, hosts ...string) (*types.Experiment, *fakeMM) {
	t.Helper()

	noBoot := linuxNode(noBootHost, staticIface("IF0", "10.0.0.2", ""))
	noBoot["general"] = map[string]any{"hostname": noBootHost, "do_not_boot": true}

	nodes := []map[string]any{
		linuxNode(captureHost, staticIface("IF0", "10.0.0.1", "")),
		noBoot,
		linuxNode(noImageHost, staticIface("IF0", "10.0.0.3", "")),
	}

	captureHosts := make(map[string]any, len(hosts))

	for _, host := range hosts {
		captureHosts[host] = []string{"IF0"}
	}

	md := map[string]any{
		"packetCapture": map[string]any{
			"elasticImage":    "elasticsearch.qc2",
			"packetBeatImage": "packetbeat.qc2",
			"elasticServer": map[string]any{
				"hostname":  "soh-elasticsearch-server",
				"ipAddress": "172.16.200.1/16",
				"vlan":      "MGMT",
			},
			"captureHosts": captureHosts,
		},
	}

	exp := newTestExperiment(t, nodes, md)
	exp.Spec.SetBaseDir(t.TempDir())
	exp.Spec.Topology().FindNodeByName(noImageHost).General().SetDoNotBoot(true)

	f := new(fakeMM)
	useFakes(t, f)

	return exp, f
}

// captureLogs returns a context that writes soh's logs to the returned buffer
// as JSON lines.
func captureLogs(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()

	logs := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	return plog.ContextWithLogger(t.Context(), logger, plog.TypeSoh), logs
}

// skippedCaptureHosts returns the reason soh logged for each capture host it
// skipped.
func skippedCaptureHosts(t *testing.T, logs *bytes.Buffer) map[string]string {
	t.Helper()

	skipped := make(map[string]string)
	dec := json.NewDecoder(logs)

	for dec.More() {
		var record map[string]any

		if err := dec.Decode(&record); err != nil {
			t.Fatalf("decoding soh log record: %v", err)
		}

		if record[slog.LevelKey] != slog.LevelWarn.String() || record[slog.MessageKey] != skipCaptureMsg {
			continue
		}

		host, _ := record[hostKey].(string)
		reason, _ := record["reason"].(string)
		skipped[host] = reason
	}

	return skipped
}

// launchedMonitors returns the VMs that the one packet capture script read
// into minimega launches, and the tap mirrors it sets up.
func launchedMonitors(t *testing.T, f *fakeMM) ([]string, []string) {
	t.Helper()

	scripts := f.readScripts()
	if len(scripts) != 1 {
		t.Fatalf("minimega read %d packet capture scripts, want 1", len(scripts))
	}

	var vms, mirrors []string

	for line := range strings.Lines(scripts[0]) {
		line = strings.TrimSpace(line)

		if vm, ok := strings.CutPrefix(line, "vm launch kvm "); ok {
			vms = append(vms, vm)
		}

		if mirror, ok := strings.CutPrefix(line, "tap mirror "); ok {
			mirrors = append(mirrors, mirror)
		}
	}

	return vms, mirrors
}

// TestDeployCaptureSkipsHostsThatWillNotBoot checks that a capture host gets a
// Packetbeat monitor only if its node is in the topology and will boot, and
// that soh warns about each host it skips.
func TestDeployCaptureSkipsHostsThatWillNotBoot(t *testing.T) {
	tests := []struct {
		name        string
		hosts       []string
		wantSkipped map[string]string
	}{
		{
			name:        "do not boot",
			hosts:       []string{captureHost, noBootHost, noImageHost},
			wantSkipped: map[string]string{noBootHost: reasonNoBoot, noImageHost: reasonNoBoot},
		},
		{
			name:        "not in topology",
			hosts:       []string{captureHost, missingHost},
			wantSkipped: map[string]string{missingHost: reasonNotInTopo},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, f := captureExperiment(t, tt.hosts...)
			ctx, logs := captureLogs(t)

			if err := newSOH().deployCapture(ctx, exp, false); err != nil {
				t.Fatalf("deployCapture() = %v, want nil", err)
			}

			vms, mirrors := launchedMonitors(t, f)

			if want := []string{captureMonitorVM}; !slices.Equal(vms, want) {
				t.Errorf("launched VMs = %q, want %q", vms, want)
			}

			if want := []string{captureHost + " 0 " + captureMonitorVM + " 1"}; !slices.Equal(mirrors, want) {
				t.Errorf("tap mirrors = %q, want %q", mirrors, want)
			}

			if got := skippedCaptureHosts(t, logs); !maps.Equal(got, tt.wantSkipped) {
				t.Errorf("skipped capture hosts = %v, want %v", got, tt.wantSkipped)
			}
		})
	}
}

// TestDeployCaptureWithNoHostThatBoots checks that soh has minimega read no
// packet capture script when none of the capture hosts gets a monitor.
func TestDeployCaptureWithNoHostThatBoots(t *testing.T) {
	exp, f := captureExperiment(t, noBootHost, noImageHost, missingHost)
	ctx, logs := captureLogs(t)

	if err := newSOH().deployCapture(ctx, exp, false); err != nil {
		t.Fatalf("deployCapture() = %v, want nil", err)
	}

	if scripts := f.readScripts(); len(scripts) != 0 {
		t.Errorf("minimega read packet capture scripts %q, want none", scripts)
	}

	want := map[string]string{noBootHost: reasonNoBoot, noImageHost: reasonNoBoot, missingHost: reasonNotInTopo}
	if got := skippedCaptureHosts(t, logs); !maps.Equal(got, want) {
		t.Errorf("skipped capture hosts = %v, want %v", got, want)
	}
}
