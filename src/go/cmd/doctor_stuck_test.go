package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
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
	ifaces "phenix/types/interfaces"
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
		"limits without open files": {
			peer: 7, status: doctorSkip,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = "Max cpu time              unlimited            unlimited            seconds   \n"
			},
			want: "not checked, because /proc/7/limits gives no limit of open files",
		},
		"no hard limit": {
			peer: 7, status: doctorPass,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = "Max open files            1024                 unlimited            files     \n"
				withFiles(h, "7", 2)
			},
			want: "phenix ui (pid 7) can open 1024 files (soft limit, hard limit unlimited). 2 files are open now",
		},
		"just at the limit": {
			peer: 7, status: doctorPass,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = "Max open files            10                   10                   files     \n"
				withFiles(h, "7", 8)
			},
			want: "phenix ui (pid 7) can open 10 files (soft limit, hard limit 10). 8 files are open now",
		},
		"over the limit": {
			peer: 7, status: doctorWarn,
			host: func(h doctorHostFS) {
				h["/proc/7/cmdline"] = phenixCmdline("phenix", "ui")
				h["/proc/7/limits"] = "Max open files            10                   10                   files     \n"
				withFiles(h, "7", 9)
			},
			want: "phenix ui (pid 7) can open 10 files (soft limit, hard limit 10). 9 files are open now, so more than 80 " +
				"percent of the limit are open",
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

// phenixUI asks phenix ui on unix-socket once in a run, and says why none answered; an empty unix-socket asks nothing.
func TestDoctorPhenixUI(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		socket  string // "" keeps the socket of the input
		noUI    bool
		noFound bool
		want    func(socket string) *doctorUI
	}{
		"answers": {want: func(string) *doctorUI { return &doctorUI{answered: true} }},
		"no answer": {
			noUI: true,
			want: func(socket string) *doctorUI {
				return &doctorUI{why: "no phenix ui on " + socket + ": phenix ui is not running, or serves another unix socket"}
			},
		},
		"empty socket": {
			socket: "-", want: func(string) *doctorUI { return &doctorUI{why: "unix-socket is empty"} },
		},
		"no run": {noFound: true, want: func(string) *doctorUI { return &doctorUI{answered: true} }},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := hostInput(t, doctorGoodHost(), doctorHostTools)
			in.found = &doctorFound{}

			if tc.noFound {
				in.found = nil
			}

			if tc.socket == "-" {
				in.settings["unix-socket"] = resolvedSetting{value: "", text: "", source: sourceDefault}
			}

			asked := 0
			answer := in.server
			in.server = func(socket string, timeout time.Duration) *http.Client {
				asked++

				if tc.noUI {
					return doctorServerDialError(syscall.ENOENT)(socket, timeout)
				}

				return answer(socket, timeout)
			}

			got := in.phenixUI(t.Context())
			if diff := cmp.Diff(tc.want(in.text("unix-socket")), got, cmp.AllowUnexported(doctorUI{})); diff != "" {
				t.Errorf("phenixUI() mismatch (-want +got):\n%s", diff)
			}

			again := in.phenixUI(t.Context())

			switch {
			case tc.noFound && again == got:
				t.Errorf("phenixUI() without a run shared its answer")
			case !tc.noFound && again != got:
				t.Errorf("phenixUI() asked phenix ui again in one run")
			case tc.socket == "-" && asked != 0:
				t.Errorf("phenixUI() asked phenix ui %d times with an empty unix-socket, want 0", asked)
			}
		})
	}
}

// phenixProcesses lists the phenix processes in /proc but phenix doctor, once in a run, and says why it listed none.
func TestDoctorPhenixProcesses(t *testing.T) {
	t.Parallel()

	procs := map[string]string{
		"/proc/10/cmdline": phenixCmdline("/usr/bin/phenix", "ui"),
		"/proc/11/cmdline": phenixCmdline("phenix", "doctor", "-p"),
		"/proc/12/cmdline": phenixCmdline("bash", "-c", "phenix ui"),
		"/proc/13/cmdline": "",
		"/proc/14/cmdline": phenixCmdline("phenix", "experiment", "start", "e1"),
		"/proc/self/":      "",
	}

	tests := map[string]struct {
		os       string
		noProc   bool
		canceled bool
		noFound  bool
		want     *doctorProcs
	}{
		"listed": {want: &doctorProcs{list: []doctorProc{
			{pid: 10, args: []string{"/usr/bin/phenix", "ui"}}, {pid: 14, args: []string{"phenix", "experiment", "start", "e1"}},
		}}},
		"no run": {noFound: true, want: &doctorProcs{list: []doctorProc{
			{pid: 10, args: []string{"/usr/bin/phenix", "ui"}}, {pid: 14, args: []string{"phenix", "experiment", "start", "e1"}},
		}}},
		"not linux":   {os: "darwin", want: &doctorProcs{why: "only Linux has /proc"}},
		"no /proc":    {noProc: true, want: &doctorProcs{why: "this host has no /proc to read"}},
		"out of time": {canceled: true, want: &doctorProcs{why: "the time ran out"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			maps.Copy(host, procs)

			in := hostInput(t, host, doctorHostTools)
			in.found = &doctorFound{}

			if tc.noFound {
				in.found = nil
			}

			if tc.os != "" {
				in.system.os = tc.os
			}

			if tc.noProc {
				in.env.readDir = func(string) ([]os.DirEntry, error) { return nil, os.ErrPermission }
			}

			ctx := t.Context()
			if tc.canceled {
				canceled, cancel := context.WithCancel(ctx)
				cancel()

				ctx = canceled
			}

			got := in.phenixProcesses(ctx)
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(doctorProcs{}, doctorProc{})); diff != "" {
				t.Errorf("phenixProcesses() mismatch (-want +got):\n%s", diff)
			}

			if again := in.phenixProcesses(ctx); (again == got) == tc.noFound {
				t.Errorf("phenixProcesses() shared its list %t, want %t", again == got, !tc.noFound)
			}
		})
	}
}

// A phenix ui that answers or runs, or a phenix process that names the experiment or all experiments, can own the
// operations of an experiment; the program name does not name it.
func TestDoctorProcsOwner(t *testing.T) {
	t.Parallel()

	proc := func(pid int, args ...string) doctorProc { return doctorProc{pid: pid, args: args} }

	tests := map[string]struct {
		answered bool
		list     []doctorProc
		want     string
	}{
		"ui answers": {answered: true, list: []doctorProc{proc(1, "phenix", "ui")}, want: "phenix ui answers"},
		"ui runs": {
			list: []doctorProc{proc(2, "phenix", "version"), proc(3, "phenix", "ui")},
			want: "phenix ui runs as process 3",
		},
		"names it": {
			list: []doctorProc{proc(4, "phenix", "experiment", "stop", "e1")},
			want: "phenix process 4 names the experiment",
		},
		"names all": {
			list: []doctorProc{proc(5, "phenix", "experiment", "stop", "all")},
			want: "phenix process 5 names the experiment",
		},
		"another":          {list: []doctorProc{proc(6, "phenix", "experiment", "stop", "e2")}},
		"program named e1": {list: []doctorProc{proc(7, "e1")}},
		"none":             {},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			procs := &doctorProcs{list: tc.list}
			if got := procs.owner(&doctorUI{answered: tc.answered}, "e1"); got != tc.want {
				t.Errorf("owner() = %q, want %q", got, tc.want)
			}
		})
	}
}

// stuck-status warns about a start marker that no phenix process runs: its process on this host is not a phenix
// process, or it is from another host and old. Without the processes of this host, a marker of this host is not
// stale.
func TestStuckStatusCheckStaleStartMarkers(t *testing.T) {
	t.Parallel()

	var (
		recent = "2026-10-30T11:00:00Z" // an hour before the test's time
		old    = "2026-10-28T12:00:00Z"
		marked = func(name string, pid int, host, since string) doctorExperiment {
			return doctorExperiment{name: name, stuck: doctorStuck{scorchRun: -1},
				starting: ifaces.StartMarker{Since: since, PID: pid, Host: host}}
		}
		experiments = []doctorExperiment{
			marked("gone", 77, "doctor-test-host", recent),
			marked("runs", 78, "doctor-test-host", recent),
			marked("old", 9, "far", old),
			marked("new", 9, "far", recent),
			{name: "none", stuck: doctorStuck{scorchRun: -1}},
		}
		hint = "for a start that no phenix process runs, start or restart phenix ui, which clears its marker. A new " +
			"start of the experiment also replaces the marker"
	)

	tests := map[string]struct {
		os   string
		want doctorCheck
	}{
		"linux": {want: doctorCheck{Name: "", Status: doctorWarn, Detail: "The store marks starts as in progress " +
			"that no phenix process runs: gone (process 77 on doctor-test-host since " + recent + "), old (process 9 on " +
			"far since " + old + ")"}},
		"no /proc": {os: "darwin", want: doctorCheck{Name: "", Status: doctorWarn, Detail: "The store marks starts " +
			"as in progress that no phenix process runs: old (process 9 on far since " + old + ")"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			host := doctorGoodHost()
			host["/proc/78/cmdline"] = phenixCmdline("phenix", "experiment", "start", "runs")
			host["/proc/77/cmdline"] = phenixCmdline("bash")

			in, _ := leftoverInput(t, host, experiments, nil)
			if tc.os != "" {
				in.system.os = tc.os
			}

			got := stuckStatusCheck(t.Context(), in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("stuckStatusCheck() mismatch (-want +got):\n%s", diff)
			}

			if got := doctorHintOf(got, stuckStatusHint); got != hint {
				t.Errorf("stuckStatusHint() = %q, want %q", got, hint)
			}
		})
	}
}

// decodeDoctorExperiment reads the start marker of a stored status, as phenix writes it and reads it back as JSON.
func TestDecodeDoctorStartMarker(t *testing.T) {
	t.Parallel()

	marker := ifaces.StartMarker{Since: "2026-10-30T10:00:00Z", PID: 4242, Host: "ws42"}

	exp := types.NewExperiment(store.ConfigMetadata{Name: "e1"})
	exp.Spec.SetExperimentName("e1")
	exp.Spec.SetTopology(&v1.TopologySpec{NodesF: nil})
	exp.Status.SetStarting(marker)

	data, err := json.Marshal(store.Config{
		Version: "phenix.sandia.gov/v1", Kind: "Experiment", Metadata: store.ConfigMetadata{Name: "e1"},
		Spec:   structs.MapDefaultCase(exp.Spec, structs.CASESNAKE),
		Status: structs.MapDefaultCase(exp.Status, structs.CASESNAKE),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var stored store.Config
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if diff := cmp.Diff(marker, decodeDoctorExperiment(stored).starting); diff != "" {
		t.Errorf("decodeDoctorExperiment().starting mismatch (-want +got):\n%s", diff)
	}
}

// doctorNoPartitionDrive is a drive whose inject partition is unset.
type doctorNoPartitionDrive struct{ ifaces.NodeDrive }

func (doctorNoPartitionDrive) InjectPartition() *int { return nil }

// The doctor store readers give their empty value for a missing status,
// scenario or inject partition.
func TestDoctorStoreMissingParts(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(doctorStuck{scorchRun: -1}, decodeDoctorStuck(nil), cmp.AllowUnexported(doctorStuck{})); diff != "" {
		t.Errorf("decodeDoctorStuck(nil) mismatch (-want +got):\n%s", diff)
	}

	if apps, scorch, components := doctorScenarioApps(nil); apps != nil || scorch != nil || components != nil {
		t.Errorf("doctorScenarioApps(nil) = %q, %q, %q, want none", apps, scorch, components)
	}

	if got := doctorInjectPartition(doctorNoPartitionDrive{}); got != 1 {
		t.Errorf("doctorInjectPartition(unset) = %d, want 1", got)
	}
}
