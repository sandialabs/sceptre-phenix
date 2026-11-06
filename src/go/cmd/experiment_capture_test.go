package cmd

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/util/freshness"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// expCaptureHeader is the header of minimega 3.4.0's capture pcap namespace listing.
//
//nolint:gochecknoglobals // read-only test table
var expCaptureHeader = []string{"id", "host", "bridge", "state", "packets", "kdrops", "path", "vlans", "filter"}

// expCaptureCluster is minimega for the experiment capture commands: the real mm commands, sent to the fake minimega,
// with test-vm running on compute1, the head node.
type expCaptureCluster struct {
	mm.Minimega

	mu      sync.Mutex
	rows    [][]string // the capture listing, without its header
	mirrors int        // Open vSwitch mirrors of other namespaces on compute1's phenix bridge
	version string     // the version answer, "" for 3.4.0
	stopped bool       // the experiment does not run
	// mirrorsErr is the error of the bridge mirrors listing, "" for none
	mirrorsErr string
}

func (*expCaptureCluster) Headnode() string { return "compute1" }

func (*expCaptureCluster) IsHeadnode(host string) bool { return host == "compute1" }

func (*expCaptureCluster) GetVMInfoObserved(...mm.Option) (mm.VMs, freshness.Observation) {
	vms := mm.VMs{{Name: "test-vm", Host: "compute1", Running: true, Networks: []string{"EXP_1 (101)", "EXP_2 (102)"}}}

	return vms, freshness.Observed(time.Now())
}

func (c *expCaptureCluster) reply(cmd mmtest.Command) []*minicli.Response {
	c.mu.Lock()
	defer c.mu.Unlock()

	base := cmd.Base

	switch {
	case base == "version":
		version := c.version
		if version == "" {
			version = "minimega 3.4.0 abc 2026-10-01"
		}

		return []*minicli.Response{mmtest.Text("compute1", version)}
	case base == "capture pcap namespace":
		return []*minicli.Response{mmtest.Tabular("compute1", expCaptureHeader, c.rows...)}
	case strings.HasPrefix(base, "capture pcap delete namespace "):
		var final [][]string

		for _, row := range c.rows {
			if row[0] == strings.TrimPrefix(base, "capture pcap delete namespace ") {
				final = append(final, append([]string{row[0], row[1], row[2], "stopped"}, row[4:]...))
			}
		}

		return []*minicli.Response{mmtest.Tabular("compute1", expCaptureHeader, final...)}
	case strings.HasPrefix(base, "capture pcap namespace "):
		return []*minicli.Response{mmtest.Text("compute1", "4")}
	case base == "bridge mirrors" && c.mirrorsErr != "":
		return []*minicli.Response{mmtest.Error("compute1", c.mirrorsErr)}
	case base == "bridge mirrors":
		rows := make([][]string, c.mirrors)
		for i := range rows {
			rows[i] = []string{"phenix", "m", "o", "other", "capture"}
		}

		return []*minicli.Response{mmtest.Tabular("compute1", []string{"bridge", "mirror", "output", "namespace", "kind"}, rows...)}
	}

	return nil
}

// useExpCaptureCluster makes minimega the fake cluster, with no forced capture support, until the test ends, and
// returns the commands sent to it since.
func useExpCaptureCluster(t *testing.T, c *expCaptureCluster) func() []string {
	t.Helper()

	if c.stopped {
		installCaptureCmdTestExperimentStatus(t, nil)
	} else {
		installCaptureCmdTestExperiment(t)
	}

	original := mm.DefaultMM
	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore the default

	mm.DefaultMM = c //nolint:reassign // install the test double

	mm.ForgetCluster()
	t.Cleanup(func() {
		mm.SetCaptureFeaturesForced(false)
		mm.ForgetCluster()
	})

	received := mmtest.Use(t, c.reply)

	return func() []string { return mmtest.Bases(received()) }
}

// runExpCaptureCmd runs phenix experiment capture with args, and returns its output and error output.
func runExpCaptureCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	cmd := newExperimentCaptureCmd()
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true

	err := cmd.Execute()

	return out.String(), errOut.String(), err
}

// starts are the experiment capture starts among the commands sent.
func starts(sent []string) []string {
	var starts []string

	for _, cmd := range sent {
		if strings.HasPrefix(cmd, "capture pcap namespace ") {
			starts = append(starts, cmd)
		}
	}

	return starts
}

func TestExperimentCaptureStart(t *testing.T) {
	tests := map[string]struct {
		args     []string
		features string // ui.features
		version  string
		mirrors  int
		// the start command sent, "" for none
		want        string
		wantOut     string
		wantWarning string
	}{
		"starts a capture with the flags": {
			args: []string{
				"test-experiment", "--vlan", "EXP_1", "--filter", "tcp port 80", "--snaplen", "96", "--format", "pcap",
				"--name", "mine",
			},
			want:    `capture pcap namespace test-experiment/files/mine.pcap vlans=EXP_1 filter="tcp port 80" snaplen=96 format=pcap maxsize=64M`,
			wantOut: "Started experiment capture 4 of test-experiment into mine.pcap\n",
		},
		"warns of the mirror budget": {
			args:        []string{"test-experiment", "--name", "x"},
			mirrors:     28,
			want:        "capture pcap namespace test-experiment/files/x.pcapng snaplen=1600 format=pcapng maxsize=64M",
			wantOut:     "Started experiment capture 4 of test-experiment into x.pcapng\n",
			wantWarning: "Warning: compute1 / phenix has 29 of 32 mirrors; 3 are left (1 by test-experiment, 28 by others)\n",
		},
		"starts on an older minimega with the minimega-captures feature": {
			args: []string{"test-experiment", "--name", "x"}, features: "vm-mount minimega-captures",
			version: "minimega 3.3.0 abc 2026-01-01",
			want:    "capture pcap namespace test-experiment/files/x.pcapng snaplen=1600 format=pcapng maxsize=64M",
			wantOut: "Started experiment capture 4 of test-experiment into x.pcapng\n",
		},
		"refuses an older minimega":       {args: []string{"test-experiment"}, version: "minimega 3.3.0 abc 2026-01-01"},
		"refuses another format":          {args: []string{"test-experiment", "--format", "erf"}},
		"refuses a VLAN of no experiment": {args: []string{"test-experiment", "--vlan", "EXP_9"}},
		"refuses a full bridge":           {args: []string{"test-experiment"}, mirrors: 32},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PHENIX_UI_CAPTURES_MAX_SIZE", "64")
			t.Setenv("PHENIX_UI_FEATURES", tc.features)

			sent := useExpCaptureCluster(t, &expCaptureCluster{mirrors: tc.mirrors, version: tc.version})

			out, errOut, err := runExpCaptureCmd(t, append([]string{"start"}, tc.args...)...)
			if (err != nil) != (tc.want == "") {
				t.Fatalf("capture start %v error = %v, want error %t", tc.args, err, tc.want == "")
			}

			var want []string
			if tc.want != "" {
				want = []string{tc.want}
			}

			if diff := cmp.Diff(want, starts(sent())); diff != "" {
				t.Errorf("capture start %v starts mismatch (-want +got):\n%s", tc.args, diff)
			}

			if out != tc.wantOut || errOut != tc.wantWarning {
				t.Errorf("capture start %v output = %q and %q, want %q and %q", tc.args, out, errOut, tc.wantOut, tc.wantWarning)
			}
		})
	}
}

func TestExperimentCaptureStop(t *testing.T) {
	sent := useExpCaptureCluster(t, &expCaptureCluster{rows: [][]string{
		{"4", "", "", "running", "120", "2", "/phenix/images/test-experiment/files/x.pcapng", "", ""},
		{"4", "compute1", "phenix", "running", "120", "2", "", "", ""},
	}})

	out, _, err := runExpCaptureCmd(t, "stop", "test-experiment", "4")
	if err != nil {
		t.Fatalf("capture stop test-experiment 4 error = %v", err)
	}

	if got := sent(); !strings.Contains(strings.Join(got, "\n"), "capture pcap delete namespace 4") {
		t.Errorf("capture stop test-experiment 4 sent %q, want capture pcap delete namespace 4", got)
	}

	// the final counters: the capture's row and its host's
	for _, want := range []string{
		"|  4 |          |        | stopped |     120 |     0 |     2 |",
		"|    | compute1 | phenix | stopped |     120 |     0 |     2 |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("capture stop test-experiment 4 output = %s, want a row %q", out, want)
		}
	}

	if _, _, err := runExpCaptureCmd(t, "stop", "test-experiment", "9"); err == nil {
		t.Error("capture stop test-experiment 9 error = nil, want an error for an unknown capture")
	}
}

func TestExperimentCaptureList(t *testing.T) {
	useExpCaptureCluster(t, &expCaptureCluster{mirrors: 3, rows: [][]string{
		{"4", "", "", "running", "120", "2", "/phenix/images/test-experiment/files/x.pcapng", "EXP_1", "tcp"},
		{"4", "compute1", "phenix", "running", "120", "2", "", "", ""},
	}})

	out, _, err := runExpCaptureCmd(t, "list", "test-experiment")
	if err != nil {
		t.Fatalf("capture list test-experiment error = %v", err)
	}

	for _, want := range []string{
		"|  4 |          |        | running |     120 |     0 |     2 |    0 | EXP_1 | tcp    | /phenix/images/test-experiment/files/x.pcapng |       |",
		"|    | compute1 | phenix | running |     120 |     0 |     2 |",
		"| compute1 | phenix |       3 |          0 |    32 | ok    |",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("capture list test-experiment output = %s, want a row %q", out, want)
		}
	}
}

// phenix experiment capture alone prints its help, which names its subcommands.
func TestExperimentCaptureHelp(t *testing.T) {
	t.Parallel()

	out, _, err := runExpCaptureCmd(t)
	if err != nil {
		t.Fatalf("capture error = %v", err)
	}

	for _, want := range []string{"Experiment capture management", "start", "stop", "list"} {
		if !strings.Contains(out, want) {
			t.Errorf("capture output = %s, want %q", out, want)
		}
	}
}

// Each experiment capture command refuses an invalid ui.captures.max-size before it asks minimega anything.
func TestExperimentCaptureRefusesInvalidMaxSize(t *testing.T) {
	const want = `invalid ui.captures.max-size "lots": it must be a whole number of megabytes, such as 10240`

	for _, args := range [][]string{
		{"start", "test-experiment"}, {"stop", "test-experiment", "4"}, {"list", "test-experiment"},
	} {
		t.Run(args[0], func(t *testing.T) {
			t.Setenv("PHENIX_UI_CAPTURES_MAX_SIZE", "lots")

			sent := useExpCaptureCluster(t, &expCaptureCluster{})

			_, _, err := runExpCaptureCmd(t, args...)
			if err == nil || err.Error() != want {
				t.Fatalf("capture %v error = %v, want %q", args, err, want)
			}

			if got := sent(); len(got) != 0 {
				t.Errorf("capture %v sent %q, want nothing", args, got)
			}
		})
	}
}

// capture list says when an experiment has no captures, and prints no mirror budgets for a stopped experiment. It
// warns when the mirrors cannot be counted, and fails when minimega cannot list captures.
func TestExperimentCaptureListOutcomes(t *testing.T) {
	tests := map[string]struct {
		cluster     *expCaptureCluster
		wantOut     string
		wantWarning string
		wantErr     string
	}{
		"stopped experiment": {
			cluster: &expCaptureCluster{stopped: true},
			wantOut: "Experiment test-experiment has no experiment captures\n",
		},
		"mirrors cannot be counted": {
			cluster:     &expCaptureCluster{mirrorsErr: "ovs-vsctl failed"},
			wantOut:     "Experiment test-experiment has no experiment captures\n",
			wantWarning: "Warning: cannot count the Open vSwitch mirrors: ",
		},
		"older minimega": {
			cluster: &expCaptureCluster{version: "minimega 3.3.0 abc 2026-01-01"},
			wantErr: "Unable to list the experiment captures of test-experiment",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PHENIX_UI_CAPTURES_MAX_SIZE", "64")
			t.Setenv("PHENIX_UI_FEATURES", "")

			useExpCaptureCluster(t, tc.cluster)

			out, errOut, err := runExpCaptureCmd(t, "list", "test-experiment")

			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}

			// the error ends with the ID of its log entry
			if !strings.HasPrefix(gotErr, tc.wantErr) || (tc.wantErr == "") != (gotErr == "") {
				t.Fatalf("capture list test-experiment error = %q, want %q", gotErr, tc.wantErr)
			}

			if out != tc.wantOut {
				t.Errorf("capture list test-experiment output = %q, want %q", out, tc.wantOut)
			}

			if !strings.HasPrefix(errOut, tc.wantWarning) || (tc.wantWarning == "") != (errOut == "") {
				t.Errorf("capture list test-experiment error output = %q, want %q", errOut, tc.wantWarning)
			}
		})
	}
}
