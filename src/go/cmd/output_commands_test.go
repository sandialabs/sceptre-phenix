package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"pgregory.net/rapid"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/api/image"
	"phenix/api/job"
	"phenix/scheduler"
	"phenix/store"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/version"
	"phenix/web"
	webutil "phenix/web/util"
)

// decodeCommandOutput decodes out, the json or yaml output of a command, into
// v. YAML goes through JSON, so a YAML key that is not the JSON key of a
// field leaves that field empty.
func decodeCommandOutput(format, out string, v any) error {
	data := []byte(out)

	if format == FormatYAML {
		var generic any
		if err := yaml.Unmarshal(data, &generic); err != nil {
			return fmt.Errorf("decoding YAML: %w", err)
		}

		var err error
		if data, err = json.Marshal(generic); err != nil {
			return fmt.Errorf("encoding the YAML as JSON: %w", err)
		}
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decoding JSON: %w", err)
	}

	return nil
}

// outputCommandsJSONOnly is a type with only JSON tags, as the types of the REST API have.
type outputCommandsJSONOnly struct {
	PlainText bool              `json:"plainText"`
	Name      string            `json:"name"`
	Count     int               `json:"count,omitempty"`
	Lines     string            `json:"lines"`
	Tags      map[string]string `json:"tags"`
	When      *time.Time        `json:"when"`
}

// writeJSONKeyedYAML writes the keys of the JSON output in their order, in
// block style, and quotes a string that YAML would read as another type.
func TestWriteJSONKeyedYAMLStyle(t *testing.T) {
	t.Parallel()

	var nilRows []outputCommandsJSONOnly

	tests := []struct {
		name string
		data any
		want string
	}{
		{name: "nil list", data: nilRows, want: "[]\n"},
		{
			name: "struct",
			data: outputCommandsJSONOnly{PlainText: true, Name: "123", Lines: "a\nb", Tags: map[string]string{"b": "true", "a": "x"}},
			want: "plainText: true\nname: \"123\"\nlines: |-\n    a\n    b\ntags:\n    a: x\n    b: \"true\"\nwhen: null\n",
		},
		{name: "list of strings", data: []string{"soh", "tap"}, want: "- soh\n- tap\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			if err := writeJSONKeyedYAML(&buf, tt.data); err != nil {
				t.Fatalf("writeJSONKeyedYAML(%v) error = %v", tt.data, err)
			}

			if diff := cmp.Diff(tt.want, buf.String()); diff != "" {
				t.Errorf("writeJSONKeyedYAML(%v) mismatch (-want +got):\n%s", tt.data, diff)
			}
		})
	}

	if err := writeJSONKeyedYAML(jobFailWriter{}, outputCommandsJSONOnly{}); err == nil {
		t.Error("writeJSONKeyedYAML to a failing writer error = nil, want an error")
	}

	if err := writeJSONKeyedYAML(io.Discard, make(chan int)); err == nil {
		t.Error("writeJSONKeyedYAML(chan) error = nil, want an error")
	}
}

// The YAML of writeJSONKeyedYAML decodes, through JSON, to the data.
func TestWriteJSONKeyedYAMLRoundTrip(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		row := outputCommandsJSONOnly{
			PlainText: rapid.Bool().Draw(t, "plainText"),
			Name:      rapid.String().Draw(t, "name"),
			Count:     rapid.Int().Draw(t, "count"),
			Lines:     rapid.OneOf(rapid.String(), rapid.StringMatching(`[a-z0-9 :#\t\n"'-]*`)).Draw(t, "lines"),
			Tags:      rapid.MapOf(rapid.StringMatching(`[a-z]{1,5}`), rapid.String()).Draw(t, "tags"),
			When:      nil,
		}

		var buf bytes.Buffer
		if err := writeJSONKeyedYAML(&buf, []outputCommandsJSONOnly{row}); err != nil {
			t.Fatalf("writeJSONKeyedYAML error = %v", err)
		}

		var got []outputCommandsJSONOnly
		if err := decodeCommandOutput(FormatYAML, buf.String(), &got); err != nil {
			t.Fatalf("decoding %q: %v", buf.String(), err)
		}

		if len(row.Tags) == 0 {
			row.Tags = nil
		}

		if len(got) == 1 && len(got[0].Tags) == 0 {
			got[0].Tags = nil
		}

		if diff := cmp.Diff([]outputCommandsJSONOnly{row}, got); diff != "" {
			t.Fatalf("round trip mismatch (-want +got):\n%s\nYAML:\n%s", diff, buf.String())
		}
	})
}

// experiment list, apps and schedulers give their rows as JSON and as YAML
// with the same keys, and refuse an unknown format.
func TestExpCmdListOutputFormats(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	expCmdUseStore(t, vmCmdExperiment(t, "alpha", false), vmCmdExperiment(t, "beta", true))

	wantRows := []experimentListRow{
		{Name: "alpha", VMCount: 2, Apps: []string{}},
		{Name: "beta", StartTime: "2024-01-01T00:00:00Z", Running: true, VMCount: 2, Apps: []string{}},
	}

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err := expCmdOutput("list", "-o", format)
		if err != nil {
			t.Fatalf("experiment list -o %s error = %v", format, err)
		}

		var rows []experimentListRow
		if err := decodeCommandOutput(format, out, &rows); err != nil {
			t.Fatalf("experiment list -o %s: %v\n%s", format, err, out)
		}

		if diff := cmp.Diff(wantRows, rows); diff != "" {
			t.Errorf("experiment list -o %s mismatch (-want +got):\n%s", format, diff)
		}

		out, err = expCmdOutput("schedulers", "-o", format)
		if err != nil {
			t.Fatalf("experiment schedulers -o %s error = %v", format, err)
		}

		var schedulers []scheduler.Info
		if err := decodeCommandOutput(format, out, &schedulers); err != nil {
			t.Fatalf("experiment schedulers -o %s: %v\n%s", format, err, out)
		}

		if diff := cmp.Diff(scheduler.Available(), schedulers); diff != "" {
			t.Errorf("experiment schedulers -o %s mismatch (-want +got):\n%s", format, diff)
		}

		out, err = expCmdOutput("apps", "-o", format)
		if err != nil {
			t.Fatalf("experiment apps -o %s error = %v", format, err)
		}

		var apps []string
		if err := decodeCommandOutput(format, out, &apps); err != nil {
			t.Fatalf("experiment apps -o %s: %v\n%s", format, err, out)
		}

		if !cmp.Equal(apps, []string{"tap", "soh", "scorch"}, cmpopts.SortSlices(func(a, b string) bool { return a < b })) {
			t.Errorf("experiment apps -o %s = %q, want tap, soh and scorch", format, apps)
		}
	}

	if out, err := expCmdOutput("list", "-o", formatTable); err != nil || !vmCmdTableHas(out, "alpha") {
		t.Errorf("experiment list -o table = %q, %v, want the table", out, err)
	}

	if err := expCmdRun("list", "-o", "csv"); err == nil || !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("experiment list -o csv error = %v, want an unknown output format", err)
	}

	expCmdUseStore(t)

	if out, err := expCmdOutput("list", "-o", FormatJSON); err != nil || out != "[]\n" {
		t.Errorf("experiment list -o json of no experiments = %q, %v, want []", out, err)
	}
}

// experiment captures and experiment soh give JSON and YAML; a stopped
// experiment has no captures.
func TestExpCmdCapturesAndSoHOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	expCmdUseStore(t, vmCmdExperiment(t, "exp1", false))

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err := runExpFilesCmd(t, newExperimentCapturesCmd(), "exp1", "-o", format)
		if err != nil {
			t.Fatalf("experiment captures exp1 -o %s error = %v", format, err)
		}

		var captures []mm.Capture
		if err := decodeCommandOutput(format, out, &captures); err != nil || len(captures) != 0 {
			t.Errorf("experiment captures exp1 -o %s = %q (%v), want an empty list", format, out, err)
		}

		out, err = runExpFilesCmd(t, newExperimentSoHCmd(), "-o", format)
		if err != nil {
			t.Fatalf("experiment soh -o %s error = %v", format, err)
		}

		var summaries []struct {
			Name       string `json:"name"`
			Configured bool   `json:"configured"`
		}
		if err := decodeCommandOutput(format, out, &summaries); err != nil {
			t.Fatalf("experiment soh -o %s: %v\n%s", format, err, out)
		}

		if len(summaries) != 1 || summaries[0].Name != "exp1" {
			t.Errorf("experiment soh -o %s = %+v, want the summary of exp1", format, summaries)
		}
	}

	if _, err := runExpFilesCmd(t, newExperimentCapturesCmd(), "none", "-o", FormatJSON); err == nil {
		t.Error("experiment captures none error = nil, want an error")
	}
}

// experiment files list and checksum give JSON and YAML with the keys of the
// REST API, and files delete --dry-run deletes nothing.
func TestExperimentFilesOutputAndDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and the cluster files
	cluster := useExpFiles(t, map[string]string{"a.log": "aaa", "sub/b.log": "bb"})

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err := runExpFilesCmd(t, newExperimentFilesCmd(), "list", "exp1", "-o", format)
		if err != nil {
			t.Fatalf("files list -o %s error = %v", format, err)
		}

		var files []file.File
		if err := decodeCommandOutput(format, out, &files); err != nil {
			t.Fatalf("files list -o %s: %v\n%s", format, err, out)
		}

		paths := make([]string, 0, len(files))
		for _, f := range files {
			paths = append(paths, f.Path)
		}

		if diff := cmp.Diff([]string{"a.log", "sub/b.log"}, paths); diff != "" {
			t.Errorf("files list -o %s paths mismatch (-want +got):\n%s", format, diff)
		}

		out, err = runExpFilesCmd(t, newExperimentFilesCmd(), "checksum", "exp1", "a.log", "-o", format)
		if err != nil {
			t.Fatalf("files checksum -o %s error = %v", format, err)
		}

		var sum webutil.Checksum
		if err := decodeCommandOutput(format, out, &sum); err != nil {
			t.Fatalf("files checksum -o %s: %v\n%s", format, err, out)
		}

		if sum.Path != "a.log" || sum.Size != 3 || len(sum.SHA256) != 64 {
			t.Errorf("files checksum -o %s = %+v, want the path a.log, the size 3 and a SHA-256", format, sum)
		}
	}

	out, err := runExpFilesCmd(t, newExperimentFilesCmd(), "delete", "exp1", "a.log", "sub/b.log", "--dry-run")
	if err != nil {
		t.Fatalf("files delete --dry-run error = %v", err)
	}

	want := "would delete file a.log of experiment exp1\nwould delete file sub/b.log of experiment exp1\n"
	if diff := cmp.Diff(want, out); diff != "" {
		t.Errorf("files delete --dry-run output mismatch (-want +got):\n%s", diff)
	}

	_, err = runExpFilesCmd(t, newExperimentFilesCmd(), "delete", "exp1", "missing.log", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "missing.log") {
		t.Errorf("files delete missing.log --dry-run error = %v, want one that names missing.log", err)
	}

	if len(cluster.deleted) != 0 {
		t.Errorf("files delete --dry-run deleted %q, want nothing", cluster.deleted)
	}

	if _, err := runExpFilesCmd(t, newExperimentFilesCmd(), "get", "exp1", "a.log", "-o", "x"); err == nil {
		t.Error("files get -o x error = nil, want -o refused: the path flag is -O/--output-file")
	}
}

// The experiment file arguments complete with the files of the experiment,
// each once.
func TestCompleteExperimentFiles(t *testing.T) { //nolint:paralleltest // replaces the phenix store and the cluster files
	useExpFiles(t, map[string]string{"a.log": "a", "sub/b.log": "b"})

	noFile := cobra.ShellCompDirectiveNoFileComp

	runCompletionTests(t, []completionTestCase{
		{name: "files", fn: completeExperimentFiles, args: []string{"exp1"}, want: []string{"a.log", "sub/b.log"}, directive: noFile},
		{
			name: "prefix", fn: completeExperimentFiles, args: []string{"exp1"}, toComplete: "s",
			want: []string{"sub/b.log"}, directive: noFile,
		},
		{
			name: "given once", fn: experimentFileArgsCompletion, args: []string{"exp1", "a.log"},
			want: []string{"sub/b.log"}, directive: noFile,
		},
		{name: "experiment", fn: experimentFileArgsCompletion, want: []string{"exp1"}, directive: noFile},
		{name: "no experiment", fn: completeExperimentFiles, directive: noFile},
	})
}

// experiment netflow status and captures give JSON and YAML, and the capture
// IDs complete from the server.
func TestExperimentNetflowOutput(t *testing.T) {
	fake := fakeNetflowServer(t)

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err := runSocketCmd(t, fake.socket, newExperimentNetflowCmd(), "status", "exp1", "-o", format)
		if err != nil {
			t.Fatalf("netflow status -o %s error = %v", format, err)
		}

		var status netflowStatus
		if err := decodeCommandOutput(format, out, &status); err != nil {
			t.Fatalf("netflow status -o %s: %v\n%s", format, err, out)
		}

		if diff := cmp.Diff(netflowStatus{Experiment: "exp1", Running: false}, status); diff != "" {
			t.Errorf("netflow status -o %s mismatch (-want +got):\n%s", format, diff)
		}

		out, err = runSocketCmd(t, fake.socket, newExperimentNetflowCmd(), "captures", "exp1", "-o", format)
		if err != nil {
			t.Fatalf("netflow captures -o %s error = %v", format, err)
		}

		var captures []experiment.NetflowCapture
		if err := decodeCommandOutput(format, out, &captures); err != nil {
			t.Fatalf("netflow captures -o %s: %v\n%s", format, err, out)
		}

		if len(captures) != 1 || captures[0].ID != netflowTestID || captures[0].Flows == nil || *captures[0].Flows != 3 {
			t.Errorf("netflow captures -o %s = %+v, want capture %s with 3 flows", format, captures, netflowTestID)
		}
	}

	expCmdUseSocket(t, fake.socket)

	runCompletionTests(t, []completionTestCase{
		{
			name: "capture IDs", fn: completeNetflowCaptureIDs, args: []string{"exp1"},
			want:      []string{netflowTestID + "\tstopped"},
			directive: cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder,
		},
		{name: "no experiment", fn: completeNetflowCaptureIDs, directive: cobra.ShellCompDirectiveNoFileComp},
		{name: "unknown experiment", fn: completeNetflowCaptureIDs, args: []string{"none"}, directive: cobra.ShellCompDirectiveNoFileComp},
	})
}

// experiment scorch --status gives JSON and YAML, and --cancel and --clear
// with --dry-run check the run and send no change.
func TestExpCmdScorchStatusOutputAndDryRun(t *testing.T) { //nolint:paralleltest // replaces the socket
	fake := &scorchFake{final: "success"}
	server := newFakeServer(t, fake.handle)
	expCmdUseSocket(t, server.socket)

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err := expCmdOutput("scorch", "exp", "--status", "-o", format)
		if err != nil {
			t.Fatalf("experiment scorch --status -o %s error = %v", format, err)
		}

		var status scorchRunStatus
		if err := decodeCommandOutput(format, out, &status); err != nil {
			t.Fatalf("experiment scorch --status -o %s: %v\n%s", format, err, out)
		}

		if status.Experiment != "exp" || status.Name != "baseline" || status.Executing || len(status.Pipeline) != 5 {
			t.Errorf("experiment scorch --status -o %s = %+v, want run 0 (baseline) of exp, not executing, with 5 nodes",
				format, status)
		}
	}

	tests := []struct {
		args    []string
		want    string
		wantErr string
	}{
		{args: []string{"--cancel", "--dry-run"}, want: "would cancel Scorch run 0 of experiment exp\n"},
		{args: []string{"--clear", "--dry-run"}, want: "would clear the status of Scorch run 0 of experiment exp\n"},
		{args: []string{"--cancel", "--dry-run", "--run", "3"}, wantErr: "experiment exp has no Scorch run 3"},
		{args: []string{"--dry-run"}, wantErr: "--dry-run works only with --cancel or --clear"},
	}

	for _, tt := range tests {
		out, err := expCmdOutput(append([]string{"scorch", "exp"}, tt.args...)...)
		if tt.wantErr != "" {
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("experiment scorch exp %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			continue
		}

		if err != nil || out != tt.want {
			t.Errorf("experiment scorch exp %q = %q, %v, want %q", tt.args, out, err, tt.want)
		}
	}

	for _, r := range server.recorded() {
		if r.method != http.MethodGet {
			t.Errorf("experiment scorch --dry-run sent %s %s, want only GET requests", r.method, r.path)
		}
	}
}

// experiment trigger --cancel --dry-run checks the server and the
// experiments, and sends no cancel.
func TestExpCmdTriggerCancelDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store and the socket
	expCmdUseStore(t, vmCmdExperiment(t, "other", false), vmCmdExperiment(t, "stopped", false))

	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { writeJSONBody(w, map[string]any{}) })
	expCmdUseSocket(t, fake.socket)

	tests := []struct {
		args    []string
		want    string
		wantErr string
	}{
		{
			args: []string{"stopped", "soh"},
			want: "would cancel the triggered apps soh of experiment stopped\n",
		},
		{
			args: []string{"all"},
			want: "would cancel every triggered app of experiment other\nwould cancel every triggered app of experiment stopped\n",
		},
		{args: []string{"missing"}, wantErr: "unable to get experiment missing"},
	}

	for _, tt := range tests {
		out, err := expCmdOutput(append([]string{"trigger", "--cancel", "--dry-run"}, tt.args...)...)
		if tt.wantErr != "" {
			if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
				t.Errorf("experiment trigger --cancel --dry-run %q error = %v, want %q", tt.args, err, tt.wantErr)
			}

			continue
		}

		if err != nil || out != tt.want {
			t.Errorf("experiment trigger --cancel --dry-run %q = %q, %v, want %q", tt.args, out, err, tt.want)
		}
	}

	for _, r := range fake.recorded() {
		if r.method != http.MethodGet {
			t.Errorf("experiment trigger --cancel --dry-run sent %s %s, want only GET requests", r.method, r.path)
		}
	}

	if err := expCmdRun("trigger", "stopped", "configure", "--dry-run"); err == nil ||
		err.Error() != "--dry-run works only with --cancel" {
		t.Errorf("experiment trigger stopped configure --dry-run error = %v, want --dry-run refused", err)
	}

	expCmdNoServer(t)

	if err := expCmdRun("trigger", "stopped", "--cancel", "--dry-run"); err == nil ||
		!strings.Contains(err.Error(), "phenix ui is not running") {
		t.Errorf("experiment trigger --cancel --dry-run without a server error = %v, want phenix ui is not running", err)
	}
}

// config list gives the configs without their specs as JSON and YAML, as GET
// /configs does.
func TestConfigListOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)
	storeConfigTestConfigs(t, "topology/alpha", "image/disk")

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err := runConfigTestCommand("list", "topology", "-o", format)
		if err != nil {
			t.Fatalf("config list topology -o %s error = %v", format, err)
		}

		var configs []store.Config
		if err := decodeCommandOutput(format, out, &configs); err != nil {
			t.Fatalf("config list -o %s: %v\n%s", format, err, out)
		}

		if len(configs) != 1 || configs[0].Kind != "Topology" || configs[0].Metadata.Name != "alpha" || configs[0].Spec != nil {
			t.Errorf("config list topology -o %s = %+v, want topology alpha without its spec", format, configs)
		}
	}
}

// image list gives every column as JSON and YAML, and image delete --dry-run
// deletes nothing.
func TestImageCmdListOutputAndDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)

	if _, err := imageCmdRun("create", "one", "--format", "raw", "--packages", "vim,git"); err != nil {
		t.Fatalf("phenix image create one error = %v", err)
	}

	imgs, err := image.List()
	if err != nil {
		t.Fatalf("image.List() error = %v", err)
	}

	want := imageListRows(imgs)
	if len(want) != 1 || want[0].Format != "raw" || !slices.Contains(want[0].Packages, "vim") || len(want[0].Scripts) == 0 {
		t.Fatalf("imageListRows(image.List()) = %+v, want image one with the format raw, vim and its scripts", want)
	}

	for _, format := range []string{FormatJSON, FormatYAML} {
		out, err := imageCmdRun("list", "-o", format)
		if err != nil {
			t.Fatalf("phenix image list -o %s error = %v", format, err)
		}

		var rows []imageListRow
		if err := decodeCommandOutput(format, out, &rows); err != nil {
			t.Fatalf("phenix image list -o %s: %v\n%s", format, err, out)
		}

		if diff := cmp.Diff(want, rows); diff != "" {
			t.Errorf("phenix image list -o %s mismatch (-want +got):\n%s", format, diff)
		}
	}

	out, err := imageCmdRun("delete", "one", "--dry-run")
	if err != nil || out != "would delete image one\n" {
		t.Errorf("phenix image delete one --dry-run = %q, %v, want %q", out, err, "would delete image one\n")
	}

	if _, err := imageCmdRun("delete", "one", "missing", "--dry-run"); err == nil ||
		!strings.Contains(err.Error(), "Unable to delete the missing image") {
		t.Errorf("phenix image delete one missing --dry-run error = %v, want the missing image named", err)
	}

	if _, err := config.Get("image/one"); err != nil {
		t.Errorf("image one after phenix image delete --dry-run: config.Get error = %v, want it stored", err)
	}
}

// settings unset --dry-run shows the key or the file that it would remove,
// and changes nothing.
func TestSettingsCmdUnsetDryRun(t *testing.T) {
	t.Parallel()

	const original = "log:\n  level: info\n"

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	v := settingsCmdViper(t, path)

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"unset", "log.level", "--dry-run"}, want: "would unset setting log.level in " + path + "\n"},
		{args: []string{"unset", "log.missing", "--dry-run"}, want: ""},
		{args: []string{"unset", "--all", "--dry-run"}, want: "would delete config file " + path + "\n"},
	}

	for _, tt := range tests {
		out, err := settingsCmdRun(v, tt.args...)
		if err != nil || out != tt.want {
			t.Errorf("settings %q = %q, %v, want %q", tt.args, out, err, tt.want)
		}
	}

	if b, err := os.ReadFile(path); err != nil || string(b) != original {
		t.Errorf("config file after settings unset --dry-run = %q, %v, want %q", b, err, original)
	}
}

// job cancel --dry-run gets the job and sends no cancel.
func TestJobCancelDryRun(t *testing.T) {
	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs/j1" {
			http.Error(w, "job not found", http.StatusNotFound)

			return
		}

		writeJSONBody(w, testJob(job.StatusRunning))
	})

	out, err := runJobCmd(t, fake.socket, "cancel", "j1", "--dry-run")
	if err != nil || out != "would cancel job j1 (running)\n" {
		t.Errorf("job cancel j1 --dry-run = %q, %v, want %q", out, err, "would cancel job j1 (running)\n")
	}

	if _, err := runJobCmd(t, fake.socket, "cancel", "j2", "--dry-run"); err == nil {
		t.Error("job cancel j2 --dry-run error = nil, want the missing job refused")
	}

	want := []recordedRequest{
		{method: http.MethodGet, path: "/api/v1/jobs/j1"},
		{method: http.MethodGet, path: "/api/v1/jobs/j2"},
	}
	if diff := cmp.Diff(want, fake.recorded(), requestFields); diff != "" {
		t.Errorf("job cancel --dry-run requests mismatch (-want +got):\n%s", diff)
	}
}

// version gives the keys of GET /version as JSON and YAML.
func TestVersionCmdOutput(t *testing.T) {
	t.Parallel()

	want := web.Version{Commit: version.Commit, Tag: version.Tag, BuildDate: version.Date, Repo: version.Repo}

	for _, format := range []string{FormatJSON, FormatYAML} {
		cmd := newVersionCmd()

		out, _, err := outputTestRun(cmd, "-o", format)
		if err != nil {
			t.Fatalf("version -o %s error = %v", format, err)
		}

		var got web.Version
		if err := decodeCommandOutput(format, out, &got); err != nil {
			t.Fatalf("version -o %s: %v\n%s", format, err, out)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("version -o %s mismatch (-want +got):\n%s", format, diff)
		}
	}
}

// The minimega console writes its output to the command's writer.
func TestNoPagerWritesToItsWriter(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	p := noPager{w: &buf}
	p.Page("")
	p.Page("vm info")

	if got, want := buf.String(), "vm info\n"; got != want {
		t.Errorf("noPager output = %q, want %q", got, want)
	}
}

// The flag completions of group commands: config names or files, app names,
// and doctor checks.
func TestCompleteNamesOrValues(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	vmCmdUseStore(t)
	storeConfigTestConfigs(t, "topology/alpha", "topology/beta")

	noFile := cobra.ShellCompDirectiveNoFileComp
	list := cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace

	runCompletionTests(t, []completionTestCase{
		{name: "topologies", fn: completeConfigNamesOrFiles("Topology"), toComplete: "a", want: []string{"alpha"}, directive: noFile},
		{name: "a file path", fn: completeConfigNamesOrFiles("Topology"), toComplete: "./", directive: cobra.ShellCompDirectiveDefault},
		{name: "apps", fn: completeAppNames, toComplete: "nt", want: []string{"ntp"}, directive: list},
		{name: "second app", fn: completeAppNames, toComplete: "ntp,se", want: []string{"ntp,serial"}, directive: list},
		{name: "app given once", fn: completeAppNames, toComplete: "ntp,nt", directive: list},
		{name: "doctor checks", fn: completeDoctorChecks, toComplete: "config-f", want: []string{"config-file"}, directive: list},
		{
			name: "second doctor check", fn: completeDoctorChecks, toComplete: "minimega,config-k",
			want: []string{"minimega,config-keys"}, directive: list,
		},
	})
}

// settings db edit completes the categories that it changes, then the names
// of the settings of the category.
func TestCompleteDBSettings(t *testing.T) {
	t.Parallel()

	noFile := cobra.ShellCompDirectiveNoFileComp

	runCompletionTests(t, []completionTestCase{
		{name: "categories", fn: completeDBEditCategories, toComplete: "Pass", want: []string{"Password"}, directive: noFile},
		{name: "no logging", fn: completeDBEditCategories, toComplete: dbLoggingCategory, directive: noFile},
		{
			name: "names", fn: completeDBSettingNames, args: []string{"Password"}, toComplete: "Min",
			want: []string{"MinLength"}, directive: noFile,
		},
		{name: "no category", fn: completeDBSettingNames, directive: noFile},
	})
}
