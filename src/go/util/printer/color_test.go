package printer

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/job"
	"phenix/store"
	"phenix/types"
	"phenix/util/mm"
	"phenix/util/termcolor"
)

// ansiCodes matches the escape codes a palette writes.
var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

// colorTables prints each colored table with colors off and on.
func colorTables(t *testing.T, render func(termcolor.Palette, *bytes.Buffer)) (string, string) {
	t.Helper()

	var plain, colored bytes.Buffer

	render(termcolor.New(false), &plain)
	render(termcolor.New(true), &colored)

	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("table without colors has escape codes:\n%q", plain.String())
	}

	// tablewriter measures a cell without its escape codes, so the borders line up
	if diff := cmp.Diff(plain.String(), ansiCodes.ReplaceAllString(colored.String(), "")); diff != "" {
		t.Errorf("colored table without its codes differs (-plain +stripped):\n%s", diff)
	}

	return plain.String(), colored.String()
}

// The Started column of a running experiment is green, its interrupted mark
// yellow, and a stopped experiment's empty.
func TestPrintTableOfExperimentsColors(t *testing.T) {
	t.Parallel()

	running := types.NewExperiment(store.ConfigMetadata{Name: "running"})
	running.Status.SetStartTime("2024-01-01T00:00:00Z")

	interrupted := types.NewExperiment(store.ConfigMetadata{Name: "interrupted"})
	interrupted.Status.SetStartTime("2024-01-01T00:00:00Z")
	interrupted.Status.SetInterrupted("minimega could not list the VMs")

	stopped := types.NewExperiment(store.ConfigMetadata{Name: "stopped"})

	_, colored := colorTables(t, func(p termcolor.Palette, out *bytes.Buffer) {
		PrintTableOfExperiments(out, p, *running, *interrupted, *stopped)
	})

	for _, want := range []string{"\x1b[32m2024-01-01T00:00:00Z\x1b[0m", "\x1b[33m(interrupted)\x1b[0m"} {
		if !strings.Contains(colored, want) {
			t.Errorf("colored experiments table lacks %q:\n%s", want, colored)
		}
	}
}

// The Running value of a VM is green when it runs, red when minimega shows it
// as QUIT or ERROR, and faint otherwise, in both VM tables.
func TestPrintTableOfVMsColors(t *testing.T) {
	t.Parallel()

	vms := []mm.VM{
		{Name: "up", State: "RUNNING", Running: true},
		{Name: "quit", State: "QUIT"},
		{Name: "error", State: "ERROR"},
		{Name: "paused", State: "PAUSED"},
		{Name: "building", State: "BUILDING"},
	}

	_, colored := colorTables(t, func(p termcolor.Palette, out *bytes.Buffer) { PrintTableOfVMs(out, p, true, vms...) })

	if got, want := strings.Count(colored, "\x1b[32mtrue\x1b[0m"), 1; got != want {
		t.Errorf("got %d green true values, want %d:\n%s", got, want, colored)
	}

	if got, want := strings.Count(colored, "\x1b[31mfalse\x1b[0m"), 2; got != want {
		t.Errorf("got %d red false values, want %d:\n%s", got, want, colored)
	}

	if got, want := strings.Count(colored, "\x1b[2mfalse\x1b[22m"), 2; got != want {
		t.Errorf("got %d faint false values, want %d:\n%s", got, want, colored)
	}

	_, single := colorTables(t, func(p termcolor.Palette, out *bytes.Buffer) { PrintTableOfVMs(out, p, false, vms[1]) })

	if !strings.Contains(single, "\x1b[31mfalse\x1b[0m") {
		t.Errorf("single VM table lacks a red false:\n%s", single)
	}
}

// Each job state has its color in the jobs table, and in the job and item
// table of one job.
func TestPrintJobColors(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	jobs := []job.Job{
		{ID: "j1", Status: job.StatusQueued, Created: created},
		{ID: "j2", Status: job.StatusRunning, Created: created},
		{ID: "j3", Status: job.StatusSucceeded, Created: created},
		{ID: "j4", Status: job.StatusPartial, Created: created},
		{ID: "j5", Status: job.StatusFailed, Created: created},
		{ID: "j6", Status: job.StatusCanceled, Created: created},
	}

	_, colored := colorTables(t, func(p termcolor.Palette, out *bytes.Buffer) { PrintTableOfJobs(out, p, jobs...) })

	for _, want := range []string{
		"\x1b[2mqueued\x1b[22m", "\x1b[36mrunning\x1b[0m", "\x1b[32msucceeded\x1b[0m",
		"\x1b[33mpartial\x1b[0m", "\x1b[31mfailed\x1b[0m", "\x1b[33mcanceled\x1b[0m",
	} {
		if !strings.Contains(colored, want) {
			t.Errorf("colored jobs table lacks %q:\n%s", want, colored)
		}
	}

	one := job.Job{
		ID: "j7", Status: job.StatusPartial, Created: created,
		Items: []job.Item{
			{Name: "a", Status: job.ItemSucceeded},
			{Name: "b", Status: job.ItemFailed, Error: "locked"},
			{Name: "c", Status: job.ItemSkipped},
			{Name: "d", Status: job.ItemPending},
		},
	}

	plain, colored := colorTables(t, func(p termcolor.Palette, out *bytes.Buffer) { PrintJob(out, p, one) })

	if !strings.Contains(plain, "Status:     partial\n") {
		t.Errorf("job without colors lacks its status line:\n%s", plain)
	}

	for _, want := range []string{
		"Status:     \x1b[33mpartial\x1b[0m\n", "\x1b[32msucceeded\x1b[0m", "\x1b[31mfailed\x1b[0m",
		"\x1b[2mskipped\x1b[22m", "\x1b[2mpending\x1b[22m",
	} {
		if !strings.Contains(colored, want) {
			t.Errorf("colored job lacks %q:\n%s", want, colored)
		}
	}
}

// Whatever the VM names and states, a colored VM table without its codes is
// the plain table.
func TestPrintTableOfVMsColorsAlign(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 6).Draw(t, "n")
		vms := make([]mm.VM, n)

		for i := range vms {
			state := rapid.SampledFrom([]string{"RUNNING", "PAUSED", "QUIT", "ERROR", "BUILDING", ""}).Draw(t, "state")
			vms[i] = mm.VM{
				Name: rapid.StringMatching(`[a-z0-9-]{1,40}`).Draw(t, "name"), State: state, Running: state == "RUNNING",
			}
		}

		var plain, colored bytes.Buffer

		PrintTableOfVMs(&plain, termcolor.New(false), false, vms...)
		PrintTableOfVMs(&colored, termcolor.New(true), false, vms...)

		if got := ansiCodes.ReplaceAllString(colored.String(), ""); got != plain.String() {
			t.Fatalf("colored table without its codes differs:\n%s\nwant:\n%s", got, plain.String())
		}
	})
}
