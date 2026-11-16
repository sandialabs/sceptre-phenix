package soh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"phenix/types"
	ifaces "phenix/types/interfaces"
	v1 "phenix/types/version/v1"
	"phenix/util/plog"
)

const skipCaptureMsg = "skipping packet capture for host"

// captureExperiment is experiment exp with packet capture for each of hosts,
// interface eth0. web boots, spare is set not to boot, noimage is marked not to
// boot as the startup app marks a node whose disk image is missing, and ext is
// external.
func captureExperiment(t *testing.T, hosts ...string) *types.Experiment {
	t.Helper()

	dnb, ext := true, true

	spare := sohNode("spare", "linux", sohIface("eth0", "A", "static", "10.0.0.2", ""))
	spare.GeneralF.DoNotBootF = &dnb

	external := sohNode("ext", "linux", sohIface("eth0", "A", "static", "10.0.0.4", ""))
	external.ExternalF = &ext

	captureHosts := make(map[string]any, len(hosts))
	for _, host := range hosts {
		captureHosts[host] = []any{"eth0"}
	}

	md := captureMetadata("10.1.0.1/24")
	md["packetCapture"].(map[string]any)["captureHosts"] = captureHosts //nolint:forcetypeassert // set by captureMetadata

	exp := sohExperiment(t, md, []*v1.Node{
		sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", "")),
		spare,
		sohNode("noimage", "linux", sohIface("eth0", "A", "static", "10.0.0.3", "")),
		external,
	})

	exp.Spec.Topology().FindNodeByName("noimage").General().SetDoNotBoot(true)

	return exp
}

// jsonLogs returns a context whose soh logs go to the returned buffer as JSON
// lines.
func jsonLogs(t *testing.T) (context.Context, *bytes.Buffer) {
	t.Helper()

	logs := new(bytes.Buffer)
	logger := slog.New(slog.NewJSONHandler(logs, nil))

	return plog.ContextWithLogger(t.Context(), logger, plog.TypeSoh), logs
}

// skippedCaptureHosts returns the reason of each warning that soh logged for a
// capture host it skipped, by host.
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

// captureScript returns the path of the packet capture script of exp.
func captureScript(exp *types.Experiment) string {
	return filepath.Join(exp.Spec.BaseDir(), "mm_files", "exp-monitor.mm")
}

// scriptLines returns the arguments of each line of script that starts with
// prefix.
func scriptLines(t *testing.T, script, prefix string) []string {
	t.Helper()

	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", script, err)
	}

	var found []string

	for line := range strings.Lines(string(body)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			found = append(found, rest)
		}
	}

	return found
}

// deployCapture gives a Packetbeat monitor only to a capture host that is in the
// topology, runs in minimega and boots. It warns about each host it skips, and
// gives minimega no script when no monitor remains.
func TestDeployCaptureSkipsHostsThatWillNotBoot(t *testing.T) { //nolint:paralleltest // replaces package globals
	const (
		noBoot   = "node will not boot"
		notInTop = "host is not in the topology"
		external = "node is external and not running in minimega"
	)

	tests := map[string]struct {
		hosts    []string
		launched []string // monitor VMs that the script launches; no script when nil
		mirrors  []string
		skipped  map[string]string
	}{
		"host set not to boot": {
			hosts: []string{"web", "spare"}, launched: []string{"web-soh-monitor"},
			mirrors: []string{"web 0 web-soh-monitor 1"}, skipped: map[string]string{"spare": noBoot},
		},
		"host without its disk image": {
			hosts: []string{"web", "noimage"}, launched: []string{"web-soh-monitor"},
			mirrors: []string{"web 0 web-soh-monitor 1"}, skipped: map[string]string{"noimage": noBoot},
		},
		"host not in the topology": {
			hosts: []string{"web", "nosuch"}, launched: []string{"web-soh-monitor"},
			mirrors: []string{"web 0 web-soh-monitor 1"}, skipped: map[string]string{"nosuch": notInTop},
		},
		"external host": {
			hosts: []string{"web", "ext"}, launched: []string{"web-soh-monitor"},
			mirrors: []string{"web 0 web-soh-monitor 1"}, skipped: map[string]string{"ext": external},
		},
		"no host that boots": {
			hosts:   []string{"spare", "noimage", "nosuch", "ext"},
			skipped: map[string]string{"spare": noBoot, "noimage": noBoot, "nosuch": notInTop, "ext": external},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			exp := captureExperiment(t, tt.hosts...)
			fake := useSOHMM(t, nil)
			ctx, logs := jsonLogs(t)

			if err := newSOH().deployCapture(ctx, exp, false); err != nil {
				t.Fatalf("deployCapture(%q) error = %v", tt.hosts, err)
			}

			if diff := cmp.Diff(tt.skipped, skippedCaptureHosts(t, logs)); diff != "" {
				t.Errorf("deployCapture(%q) skipped hosts mismatch (-want +got):\n%s", tt.hosts, diff)
			}

			script := captureScript(exp)

			if tt.launched == nil {
				if len(fake.scripts) != 0 {
					t.Errorf("deployCapture(%q) scripts read = %q, want none", tt.hosts, fake.scripts)
				}

				if _, err := os.Stat(script); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("deployCapture(%q) os.Stat(capture script) error = %v, want %v", tt.hosts, err, fs.ErrNotExist)
				}

				return
			}

			if diff := cmp.Diff([]string{"exp " + script}, fake.scripts); diff != "" {
				t.Errorf("deployCapture(%q) scripts read mismatch (-want +got):\n%s", tt.hosts, diff)
			}

			if diff := cmp.Diff(tt.launched, scriptLines(t, script, "vm launch kvm ")); diff != "" {
				t.Errorf("deployCapture(%q) launched VMs mismatch (-want +got):\n%s", tt.hosts, diff)
			}

			if diff := cmp.Diff(tt.mirrors, scriptLines(t, script, "tap mirror ")); diff != "" {
				t.Errorf("deployCapture(%q) tap mirrors mismatch (-want +got):\n%s", tt.hosts, diff)
			}
		})
	}
}

// deployCapture gives each capture host that boots its own monitor, and sends
// no script to minimega for a dry run.
func TestDeployCaptureMonitorsEachHost(t *testing.T) { //nolint:paralleltest // replaces package globals
	exp := captureExperiment(t, "web", "db")
	exp.Spec.Topology().AddNode("VirtualMachine", "db").AddNetworkInterface("ethernet", "eth0", "B")

	fake := useSOHMM(t, nil)

	if err := newSOH().deployCapture(t.Context(), exp, true); err != nil {
		t.Fatalf("deployCapture(web, db, dry run) error = %v", err)
	}

	if len(fake.scripts) != 0 {
		t.Errorf("deployCapture(web, db, dry run) scripts read = %q, want none", fake.scripts)
	}

	// The order of the capture hosts is random.
	launched := scriptLines(t, captureScript(exp), "vm launch kvm ")
	slices.Sort(launched)

	if diff := cmp.Diff([]string{"db-soh-monitor", "web-soh-monitor"}, launched); diff != "" {
		t.Errorf("deployCapture(web, db, dry run) launched VMs mismatch (-want +got):\n%s", diff)
	}
}

// deployCapture returns the error of each file it cannot write and of the
// script minimega cannot read.
func TestDeployCaptureErrors(t *testing.T) { //nolint:paralleltest // replaces package globals
	errRead := errors.New("read failed")

	tests := map[string]struct {
		blocked string // a folder at this path, relative to the base folder, stops its file
		readErr error
	}{
		"hostname script":   {blocked: "startup/web-soh-monitor-hostname.sh"},
		"timezone script":   {blocked: "startup/web-soh-monitor-timezone.sh"},
		"interfaces script": {blocked: "startup/web-soh-monitor-interfaces.sh"},
		"Packetbeat config": {blocked: "startup/web-soh-monitor-packetbeat.yml"},
		"capture script":    {blocked: "mm_files/exp-monitor.mm"},
		"minimega read":     {readErr: errRead},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			exp := captureExperiment(t, "web")
			fake := useSOHMM(t, nil)
			fake.scriptErr = tt.readErr

			if tt.blocked != "" {
				if err := os.MkdirAll(filepath.Join(exp.Spec.BaseDir(), tt.blocked), 0o750); err != nil {
					t.Fatalf("os.MkdirAll(%s) error = %v", tt.blocked, err)
				}
			}

			err := newSOH().deployCapture(t.Context(), exp, false)
			if tt.readErr != nil {
				if !errors.Is(err, tt.readErr) {
					t.Errorf("deployCapture(web) error = %v, want %v", err, tt.readErr)
				}

				return
			}

			var linkErr *os.LinkError
			if !errors.As(err, &linkErr) || linkErr.New != filepath.Join(exp.Spec.BaseDir(), tt.blocked) {
				t.Errorf("deployCapture(web with %s blocked) error = %v, want an error that replaces it", tt.blocked, err)
			}
		})
	}
}

// deployCapture fails for an experiment without soh metadata, and does
// nothing without capture hosts.
func TestDeployCaptureMetadata(t *testing.T) { //nolint:paralleltest // replaces package globals
	nodes := []*v1.Node{sohNode("web", "linux", sohIface("eth0", "A", "static", "10.0.0.1", ""))}

	if err := newSOH().deployCapture(t.Context(), sohExperiment(t, nil, nodes), false); err == nil {
		t.Error("deployCapture(no soh metadata) error = nil, want an error")
	}

	fake := useSOHMM(t, nil)
	exp := sohExperiment(t, map[string]any{}, nodes)

	if err := newSOH().deployCapture(t.Context(), exp, false); err != nil {
		t.Errorf("deployCapture(no capture hosts) error = %v", err)
	}

	if _, err := os.Stat(captureScript(exp)); !errors.Is(err, fs.ErrNotExist) || len(fake.scripts) != 0 {
		t.Errorf("deployCapture(no capture hosts) wrote or read a script: stat error %v, read %q", err, fake.scripts)
	}
}

// captureSkipReason gives a reason for each node that gets no monitor.
func TestCaptureSkipReason(t *testing.T) {
	t.Parallel()

	dnb, ext := true, true

	tests := map[string]struct {
		node ifaces.NodeSpec
		want string
	}{
		"boots":               {node: sohNode("web", "linux"), want: ""},
		"not in the topology": {node: nil, want: "host is not in the topology"},
		"external":            {node: &v1.Node{ExternalF: &ext}, want: "node is external and not running in minimega"},
		"set not to boot":     {node: &v1.Node{GeneralF: &v1.General{DoNotBootF: &dnb}}, want: "node will not boot"},
		"external, not to boot": {
			node: &v1.Node{GeneralF: &v1.General{DoNotBootF: &dnb}, ExternalF: &ext},
			want: "node is external and not running in minimega",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := captureSkipReason(tt.node); got != tt.want {
				t.Errorf("captureSkipReason(%s) = %q, want %q", name, got, tt.want)
			}
		})
	}
}

// Configure returns the error of each Elastic server file it cannot write.
func TestConfigureElasticServerErrors(t *testing.T) { //nolint:paralleltest // replaces package globals
	for _, blocked := range []string{"startup/elastic-elasticsearch.yml", "startup/elastic-kibana.yml"} {
		t.Run(blocked, func(t *testing.T) {
			exp := captureExperiment(t, "web")

			path := filepath.Join(exp.Spec.BaseDir(), blocked)
			if err := os.MkdirAll(path, 0o750); err != nil {
				t.Fatalf("os.MkdirAll(%s) error = %v", blocked, err)
			}

			var linkErr *os.LinkError
			if err := newSOH().Configure(t.Context(), exp); !errors.As(err, &linkErr) || linkErr.New != path {
				t.Errorf("Configure(exp with %s blocked) error = %v, want an error that replaces it", blocked, err)
			}
		})
	}
}
