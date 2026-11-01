package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/activeshadow/structs"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/api/scorch/scorchmd"
	"phenix/store"
	"phenix/types"
	v1 "phenix/types/version/v1"
)

// phenixCmdline is the /proc/<pid>/cmdline of a process that runs args.
func phenixCmdline(args ...string) string {
	return strings.Join(args, "\x00") + "\x00"
}

// The stored status of a running experiment gives the app running flags, the SCORCH run, the delayed starts and an
// interrupted start, as phenix writes them to the store and reads them back as JSON.
func TestDecodeDoctorStuck(t *testing.T) {
	t.Parallel()

	exp := types.NewExperiment(store.ConfigMetadata{Name: "e1"})
	exp.Spec.SetExperimentName("e1")
	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: nil})
	exp.Status.SetStartTime("2026-10-30T10:00:00Z")
	exp.Status.SetAppRunning("soh", true)
	exp.Status.SetAppRunning("ntp", false)
	exp.Status.SetAppRunning("scorch", true)
	exp.Status.SetAppStatus("scorch", scorchmd.ScorchStatus{RunID: 2})
	exp.Status.SetDelayedStarts(map[string]string{"vm1": "2026-10-30T10:05:00Z"}, nil, false)
	exp.Status.SetInterrupted("VMs were waiting to start when phenix stopped tracking them")

	c := store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "e1"},
		Spec:   structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status: structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var stored store.Config
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	got := decodeDoctorExperiment(stored)
	want := doctorStuck{apps: []string{"soh"}, scorch: true, scorchRun: 2, pending: true, interrupted: true}

	if !got.running {
		t.Errorf("decodeDoctorExperiment().running = false, want true")
	}

	if diff := cmp.Diff(want, got.stuck, cmp.AllowUnexported(doctorStuck{})); diff != "" {
		t.Errorf("decodeDoctorExperiment().stuck mismatch (-want +got):\n%s", diff)
	}
}

// A phenix process runs phenix ui when ui is its first argument that is not a
// flag or the value of a flag, which follows a flag in the next argument.
func TestDoctorProcIsUI(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		args []string
		want bool
	}{
		"ui":                         {args: []string{"phenix", "ui"}, want: true},
		"flag with =":                {args: []string{"phenix", "--log.level=debug", "ui"}, want: true},
		"flag and value":             {args: []string{"phenix", "--log.level", "debug", "ui"}, want: true},
		"two flags and values":       {args: []string{"phenix", "--unix-socket", "/run/p.sock", "--log.level", "ui", "ui"}, want: true},
		"bool flag":                  {args: []string{"phenix", "--use-gre-mesh", "ui"}, want: true},
		"unknown flag":               {args: []string{"phenix", "--other", "ui"}, want: true},
		"value is ui":                {args: []string{"phenix", "--log.level", "ui", "experiment"}, want: false},
		"ui after another command":   {args: []string{"phenix", "experiment", "ui"}, want: false},
		"ui after the end of flags":  {args: []string{"phenix", "--", "ui"}, want: false},
		"flag with no value at end":  {args: []string{"phenix", "--log.level"}, want: false},
		"program only":               {args: []string{"phenix"}, want: false},
		"ui flags after the command": {args: []string{"phenix", "ui", "--listen-endpoint", "0.0.0.0:3000"}, want: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := (doctorProc{pid: 1, args: tc.args}).isUI(); got != tc.want {
				t.Errorf("isUI(%q) = %t, want %t", tc.args, got, tc.want)
			}
		})
	}
}

// Root flags with their values in the next argument never hide the command:
// ui after them is phenix ui, and another command after them is not.
func TestDoctorProcIsUIProperties(t *testing.T) {
	t.Parallel()

	valued := []string{"log.level", "unix-socket", "store.endpoint", "base-dir.phenix", "hostname-suffixes", "color"}

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 4).Draw(t, "flags")
		args := make([]string, 1, 2*n+2) // the program, a flag and a value each, and the command
		args[0] = phenixProgram

		for range n {
			flag := rapid.SampledFrom(valued).Draw(t, "flag")
			args = append(args, "--"+flag, rapid.String().Draw(t, "value"))
		}

		command := rapid.SampledFrom([]string{"ui", "experiment", "doctor"}).Draw(t, "command")
		args = append(args, command)

		if got := (doctorProc{pid: 1, args: args}).isUI(); got != (command == uiCommand) {
			t.Fatalf("isUI(%q) = %t, want %t", args, got, command == uiCommand)
		}
	})
}

// stuck-status warns about the app runs, SCORCH runs and delayed starts that the store marks as in progress for a
// running experiment when no phenix ui answers and no other phenix process names the experiment, and about an
// interrupted start. A phenix ui or a phenix process that names the experiment can own them.
func TestStuckStatusCheck(t *testing.T) {
	t.Parallel()

	flagged := doctorExperiment{
		name: "e1", running: true,
		stuck: doctorStuck{apps: []string{"soh"}, scorch: true, scorchRun: 0, pending: true, interrupted: false},
	}
	clean := doctorExperiment{name: "e2", running: true, stuck: doctorStuck{scorchRun: -1}}
	stopped := doctorExperiment{name: "off", stuck: doctorStuck{apps: []string{"soh"}, scorchRun: -1}}
	interrupted := doctorExperiment{name: "e3", running: true, stuck: doctorStuck{scorchRun: -1, interrupted: true}}
	unknownRun := doctorExperiment{name: "e4", running: true, stuck: doctorStuck{scorch: true, scorchRun: -1}}
	items := "app soh of e1, SCORCH run 0 of e1, the delayed starts of e1"
	stuckHint := "start or restart phenix ui. As it starts, it clears the app running flags of the running " +
		"experiments and resumes their delayed starts"
	stopHint := "for an interrupted start, stop the experiment with phenix experiment stop <experiment>, then start it again"

	tests := map[string]struct {
		stored []doctorExperiment
		why    string
		noUI   bool
		os     string
		procs  map[string]string // the cmdline of each pid
		want   string            // the detail; {socket} is unix-socket and {noUI} is why no phenix ui answers
		status doctorStatus
		hint   string
	}{
		"no store": {
			why: "the store file x does not exist or is empty", status: doctorSkip,
			want: "not checked, because the store file x does not exist or is empty",
		},
		"nothing runs": {stored: []doctorExperiment{stopped}, status: doctorPass, want: "no stored experiment runs"},
		"nothing in progress": {
			stored: []doctorExperiment{clean, stopped}, status: doctorPass,
			want: "no app run, SCORCH run or delayed start of the 1 running experiment is marked as in progress",
		},
		"phenix ui answers": {
			stored: []doctorExperiment{flagged}, status: doctorPass,
			want: "the store marks some operations as in progress, and phenix ui answers, so it can own them. " +
				"The operations are: " + items,
		},
		"no owner": {
			stored: []doctorExperiment{flagged, clean, unknownRun}, noUI: true, status: doctorWarn, hint: stuckHint,
			procs: map[string]string{"10": phenixCmdline("/usr/local/bin/phenix", "doctor"), "11": phenixCmdline("bash")},
			want: "The store marks some operations as in progress, but no phenix process can own them. " +
				"The operations are: " + items + ", a SCORCH run of e4. No phenix ui answers. Reason: {noUI}. " +
				"No other phenix process on this host names the experiment",
		},
		"a phenix process names the experiment": {
			stored: []doctorExperiment{flagged}, noUI: true, status: doctorPass,
			procs: map[string]string{"12": phenixCmdline("phenix", "experiment", "scorch", "e1")},
			want: "the store marks some operations as in progress, and phenix process 12 names the experiment, " +
				"so it can own them. The operations are: " + items,
		},
		"a phenix ui process": {
			stored: []doctorExperiment{flagged}, noUI: true, status: doctorPass,
			procs: map[string]string{"13": phenixCmdline("/usr/bin/phenix", "--log.level", "debug", "ui")},
			want: "the store marks some operations as in progress, and phenix ui runs as process 13, so it can own them. " +
				"The operations are: " + items,
		},
		"interrupted": {
			stored: []doctorExperiment{interrupted}, status: doctorWarn, hint: stopHint,
			want: "The start of e3 was interrupted, so some VMs did not start",
		},
		"not linux": {
			stored: []doctorExperiment{flagged, interrupted}, noUI: true, os: "darwin", status: doctorWarn,
			hint: stuckHint + ". " + capitalize(stopHint),
			want: "The store marks some operations as in progress, but no phenix process can own them. " +
				"The operations are: " + items + ". No phenix ui answers. Reason: {noUI}. " +
				"Doctor cannot list the phenix processes, because only Linux has /proc. " +
				"The start of e3 was interrupted, so some VMs did not start",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			for pid, cmdline := range tc.procs {
				host["/proc/"+pid+"/cmdline"] = cmdline
			}

			in, _ := leftoverInput(t, host, tc.stored, nil)
			in.found.experiments.why = tc.why

			if tc.os != "" {
				in.system.os = tc.os
			}

			if tc.noUI {
				in.server = doctorServerDialError(syscall.ENOENT)
			}

			socket := in.text("unix-socket")
			noUI := fmt.Sprintf("no phenix ui on %s: phenix ui is not running, or serves another unix socket", socket)
			want := doctorCheck{
				Name: "", Status: tc.status, Detail: strings.ReplaceAll(tc.want, "{noUI}", noUI),
			}

			got := stuckStatusCheck(t.Context(), in)
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("stuckStatusCheck() mismatch (-want +got):\n%s", diff)
			}

			if hint := doctorHintOf(got, stuckStatusHint); hint != tc.hint {
				t.Errorf("stuckStatusHint() = %q, want %q", hint, tc.hint)
			}
		})
	}
}

// The server check records whether phenix ui answered, so stuck-status does not ask it again.
func TestStuckStatusUsesServerAnswer(t *testing.T) {
	t.Parallel()

	exp := types.NewExperiment(store.ConfigMetadata{Name: "e1"})
	exp.Spec.SetExperimentName("e1")
	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: nil})
	exp.Status.SetStartTime("2026-10-30T10:00:00Z")
	exp.Status.SetAppRunning("soh", true)

	in, _, _ := storedInput(t, map[string]store.Configs{"Experiment": {{
		Version: "phenix.sandia.gov/v1", Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "e1"},
		Spec:   structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status: structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	}}}, nil)

	asked := 0
	answer := in.server
	in.server = func(socket string, timeout time.Duration) *http.Client {
		asked++

		return answer(socket, timeout)
	}

	var defs []doctorCheckDef

	for _, def := range doctorChecks() {
		if def.id == serverCheckName || def.id == stuckStatusName {
			defs = append(defs, def)
		}
	}

	results := runDoctorChecks(t.Context(), in, defs, map[string]bool{})

	if asked != 1 {
		t.Errorf("runDoctorChecks() asked phenix ui %d times, want 1", asked)
	}

	want := "the store marks some operations as in progress, and phenix ui answers, so it can own them. " +
		"The operations are: app soh of e1"
	if got := results[len(results)-1]; got.ID != stuckStatusName || got.Detail != want {
		t.Errorf("stuck-status = %+v, want the detail %q", got, want)
	}
}

// ui-limits finds phenix ui through the peer of its socket, or in /proc, and warns when more than 80% of its soft
// limit of open files are open. It skips when it cannot find or read the process.
func TestUILimitsCheck(t *testing.T) {
	t.Parallel()

	withFiles := func(host doctorHostFS, pid string, n int) {
		host["/proc/"+pid+"/fd/"] = ""
		for i := range n {
			host[fmt.Sprintf("/proc/%s/fd/%d", pid, i)] = ""
		}
	}

	tests := map[string]struct {
		os     string
		peer   int
		host   func(doctorHostFS)
		status doctorStatus
		want   string
	}{
		"not linux": {
			os: "darwin", status: doctorSkip,
			want: "not checked, because doctor reads the limits of phenix ui in /proc, which only Linux has",
		},
		"socket peer": {
			peer: 7, status: doctorPass,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = limits1024
				withFiles(h, "7", 3)
			},
			want: "phenix ui (pid 7) can open 1024 files (soft limit, hard limit 4096). 3 files are open now",
		},
		"busy": {
			peer: 7, status: doctorWarn,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = limits1024
				withFiles(h, "7", 900)
			},
			want: "phenix ui (pid 7) can open 1024 files (soft limit, hard limit 4096). 900 files are open now, so " +
				"more than 80 percent of the limit are open",
		},
		"unlimited": {
			peer: 7, status: doctorPass,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = limitsNone
			},
			want: "phenix ui (pid 7) has no limit of open files",
		},
		"found in proc": {
			status: doctorPass,
			host: func(h doctorHostFS) {
				h["/proc/8/cmdline"] = phenixCmdline("phenix", "experiment", "list")
				h["/proc/9/cmdline"] = phenixCmdline("/opt/phenix/bin/phenix", "ui", "--unix-socket", "/tmp/phenix.sock")
				h["/proc/9/limits"] = limits1024
				withFiles(h, "9", 1)
			},
			want: "phenix ui (pid 9) can open 1024 files (soft limit, hard limit 4096). 1 file is open now",
		},
		"peer is not phenix": {
			peer: 7, status: doctorSkip,
			host: func(h doctorHostFS) { h["/proc/7/cmdline"] = phenixCmdline("socat") },
			want: "not checked, because no phenix ui process runs on this host, or it runs in another container",
		},
		"no process": {
			status: doctorSkip,
			want:   "not checked, because no phenix ui process runs on this host, or it runs in another container",
		},
		"open files not readable": {
			peer: 7, status: doctorSkip,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = limits1024
			},
			want: "not checked, because this user cannot count the open files in /proc/7/fd, such as when phenix ui " +
				"runs as another user",
		},
		"limits not readable": {
			peer: 7, status: doctorSkip,
			host: func(h doctorHostFS) { h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui") },
			want: "not checked, because this user cannot read /proc/7/limits: file does not exist",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			if tc.host != nil {
				tc.host(host)
			}

			in := hostInput(t, host, doctorHostTools)
			in.found = &doctorFound{}
			in.env.socketPeer = func(context.Context, string, time.Duration) (int, error) {
				if tc.peer == 0 {
					return 0, errors.New("connecting: no such file or directory")
				}

				return tc.peer, nil
			}

			if tc.os != "" {
				in.system.os = tc.os
			}

			got := uiLimitsCheck(t.Context(), in)
			if diff := cmp.Diff(doctorCheck{Name: "", Status: tc.status, Detail: tc.want}, got); diff != "" {
				t.Errorf("uiLimitsCheck() mismatch (-want +got):\n%s", diff)
			}

			wantHint := ""
			if tc.status == doctorWarn {
				wantHint = "increase the open files limit of phenix ui with LimitNOFILE= in its systemd unit, or with " +
					"docker run --ulimit nofile=<n>:<n>, then restart phenix ui"
			}

			if hint := doctorHintOf(got, uiLimitsHint); hint != wantHint {
				t.Errorf("uiLimitsHint() = %q, want %q", hint, wantHint)
			}
		})
	}
}
