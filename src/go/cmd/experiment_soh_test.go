package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"phenix/api/soh"
	"phenix/util/mm"
)

func TestExperimentSoHArguments(t *testing.T) {
	expCmdUseStore(t)

	if _, err := runExpFilesCmd(t, newExperimentSoHCmd(), "exp1", "--status", "stopped"); !errors.Is(err,
		soh.ErrInvalidStatusFilter) {
		t.Errorf("soh exp1 --status stopped error = %v, want %v", err, soh.ErrInvalidStatusFilter)
	}

	if _, err := runExpFilesCmd(t, newExperimentSoHCmd(), "--status", "running"); err == nil {
		t.Errorf("soh --status running without an experiment error = nil, want an error")
	}

	if _, err := runExpFilesCmd(t, newExperimentSoHCmd(), "nope"); err == nil {
		t.Errorf("soh of an unknown experiment error = nil, want an error")
	}
}

// The summary of stopped experiments asks minimega nothing.
func TestExperimentSoHSummary(t *testing.T) {
	expCmdUseStore(t, vmCmdExperiment(t, "b-exp", false), vmCmdExperiment(t, "a-exp", false))

	out, err := runExpFilesCmd(t, newExperimentSoHCmd())
	if err != nil {
		t.Fatalf("soh error = %v", err)
	}

	if a, b := strings.Index(out, "a-exp"), strings.Index(out, "b-exp"); a < 0 || b < a {
		t.Errorf("soh = %q, want a-exp, then b-exp", out)
	}

	if !strings.Contains(out, "not configured") {
		t.Errorf("soh = %q, want the soh app not configured", out)
	}
}

func TestPrintSoHNetwork(t *testing.T) {
	t.Parallel()

	var (
		out     bytes.Buffer
		summary = soh.Summary{
			Name: "exp1", Configured: true, Running: true, SOHInitialized: true,
			VMs:           soh.VMCounts{Known: true, Total: 2, Running: 1},
			Checks:        soh.Tally{Total: 3, Passing: 2, Failing: 1},
			FailingChecks: []soh.FailingCheck{{Host: "web", Check: "service", Target: "nginx", Error: "not running"}},
		}
		network = &soh.Network{Nodes: []soh.Node{
			{Label: "web", Status: "running", SOH: &soh.HostState{CPULoad: "0.5", Services: []soh.State{{Error: "not running"}}}},
			{Label: "LAN", Status: "ignore"},
		}}
	)

	printSoHNetwork(&out, summary, network)

	for _, want := range []string{"state of health initialized", "VMs 1/2 running", "checks 2/3", "nginx", "0.5"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printSoHNetwork() = %q, want it to contain %q", out.String(), want)
		}
	}

	if strings.Contains(out.String(), "LAN") {
		t.Errorf("printSoHNetwork() = %q, want no row for the LAN network node", out.String())
	}
}

func TestPrintHosts(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	printHosts(&out, []mm.Host{{
		Name: "head", Headnode: true, Schedulable: true, CPUs: 8, Load: []string{"0.1", "0.2", "0.3"},
		MemUsed: 1024, MemTotal: 4096, VMs: 2, Tx: 1.25, Uptime: 90.4,
	}})

	for _, want := range []string{"head", "0.1 0.2 0.3", "4096", "1.2", "1m30s"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("printHosts() = %q, want it to contain %q", out.String(), want)
		}
	}
}
