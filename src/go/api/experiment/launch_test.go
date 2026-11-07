package experiment

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/store"
	"phenix/store/storetest"
	dct "phenix/util/diskchain/diskchaintest"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
	"phenix/util/notes"
)

// launchMM is the minimega client phenix uses, sent to the fake minimega of mmtest, with head node "head".
type launchMM struct {
	mm.Minimega
}

func (launchMM) Headnode() string {
	return "head"
}

// deletedFiles records, in order, the cluster files phenix deletes, and fails each deletion with err;
// other ClusterFiles methods panic.
type deletedFiles struct {
	file.ClusterFiles

	mu      sync.Mutex
	deleted []string
	err     error
}

func (d *deletedFiles) DeleteFile(path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.deleted = append(d.deleted, path)

	return d.err
}

func (d *deletedFiles) DeleteExistingFiles(names []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.deleted = append(d.deleted, names...)

	return d.err
}

func (d *deletedFiles) files() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]string(nil), d.deleted...)
}

// useDeletedFiles makes a new deletedFiles the cluster files until the test ends.
func useDeletedFiles(t *testing.T) *deletedFiles {
	t.Helper()

	fake := new(deletedFiles)

	original := file.DefaultClusterFiles
	file.DefaultClusterFiles = fake //nolint:reassign // install test double

	t.Cleanup(func() { file.DefaultClusterFiles = original }) //nolint:reassign // restore default

	return fake
}

// launchReplies are the fake minimega's answers to the commands Start sends, by command base; a base it does not
// hold gets an empty success.
type launchReplies map[string][]*minicli.Response

// useLaunchMM makes launchMM the minimega client and the fake minimega answer with replies until the test ends,
// and returns a lister of the commands the fake received.
func useLaunchMM(t *testing.T, replies launchReplies) func() []mmtest.Command {
	t.Helper()

	original := mm.DefaultMM
	mm.DefaultMM = launchMM{} //nolint:reassign // install test double

	t.Cleanup(func() { mm.DefaultMM = original }) //nolint:reassign // restore default

	return mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		for prefix, resps := range replies {
			if strings.HasPrefix(cmd.Base, prefix) {
				return resps
			}
		}

		return nil
	})
}

// defaultLaunchReplies are the fake minimega's answers that place VM a on host node1 and VLAN EXP-1 at 101.
func defaultLaunchReplies() launchReplies {
	return launchReplies{
		"vm info": {mmtest.Tabular("node1", []string{"host", "name"}, []string{"node1", "a"})},
		"vlans":   {mmtest.Tabular("head", []string{"namespace", "alias", "vlan"}, []string{"exp", "EXP-1", "101"})},
	}
}

// launchNode is a VM node of a stored experiment, booting from image.
func launchNode(hostname, image string, extra map[string]any) map[string]any {
	node := map[string]any{
		"type":    "VirtualMachine",
		"general": map[string]any{"hostname": hostname},
		"hardware": map[string]any{
			"os_type": "linux",
			"drives":  []any{map[string]any{"image": image}},
		},
		"network": map[string]any{
			"interfaces": []any{
				map[string]any{"name": "IF0", "type": "ethernet", "vlan": "EXP-1", "proto": "dhcp"},
			},
		},
	}

	maps.Copy(node, extra)

	return node
}

// storeLaunchExperiment stores experiment exp, stopped, with the given nodes and spec settings, and returns its
// base directory.
func storeLaunchExperiment(t *testing.T, spec map[string]any, nodes ...map[string]any) string {
	t.Helper()

	base := t.TempDir()

	full := map[string]any{
		"experimentName": "exp",
		"baseDir":        base,
		"vlans":          map[string]any{"aliases": map[string]any{}},
		"topology":       map[string]any{"nodes": toAny(nodes)},
	}

	maps.Copy(full, spec)

	err := store.Create(&store.Config{
		Version:  "phenix.sandia.gov/v1",
		Kind:     "Experiment",
		Metadata: store.ConfigMetadata{Name: "exp"},
		Spec:     full,
		Status:   map[string]any{},
	})
	if err != nil {
		t.Fatalf("store.Create(experiment exp) error = %v", err)
	}

	return base
}

func toAny(nodes []map[string]any) []any {
	out := make([]any, 0, len(nodes))

	for _, n := range nodes {
		out = append(out, n)
	}

	return out
}

// launchImage writes a qcow2 image with no backing file, and returns its path.
func launchImage(t *testing.T) string {
	t.Helper()

	image := t.TempDir() + "/a.qc2"
	dct.WriteQcow2(t, image, dct.Qcow2{})

	return image
}

// countHooks counts the hooks run for stage while the test runs.
func countHooks(t *testing.T, stage string) *int {
	t.Helper()

	original := hooks[stage]
	t.Cleanup(func() { hooks[stage] = original })

	count := new(int)
	hooks[stage] = []Hook{func(string, string) { *count++ }}

	return count
}

// failStartStore is a BoltDB whose Update fails with err for a config that records an experiment started.
type failStartStore struct {
	*store.BoltDB

	err error
}

func (s *failStartStore) Update(c *store.Config) error {
	if started, _ := c.Status["startTime"].(string); started != "" && s.err != nil {
		return s.err
	}

	return s.BoltDB.Update(c) //nolint:wrapcheck // the store's own error
}

// launched is what a test of Start checks after the start.
type launched struct {
	Commands  []mmtest.Command
	Deleted   []string
	Info      []string
	Running   bool
	Starting  bool // whether the stored status still holds a start marker
	Schedules map[string]string
	VLANs     map[string]int
	Hooks     int
}

// launchResult reads what Start did, for a test to compare with what it wants.
func launchResult(t *testing.T, ctx context.Context, received func() []mmtest.Command, files *deletedFiles, started *int) launched {
	t.Helper()

	exp := experimentNamed(t, "exp")

	return launched{
		Commands:  sentCommands(received),
		Deleted:   files.files(),
		Info:      notes.Info(ctx, true),
		Running:   exp.Running(),
		Starting:  exp.Status.Starting().PID != 0,
		Schedules: exp.Status.Schedules(),
		VLANs:     exp.Status.VLANs(),
		Hooks:     *started,
	}
}

// sentCommands lists the commands the fake minimega received, but for the args command: phenix asks minimega's args
// for its files directory again only once a few seconds passed, so whether a test sends it depends on the tests before.
func sentCommands(received func() []mmtest.Command) []mmtest.Command {
	var cmds []mmtest.Command

	for _, cmd := range received() {
		if cmd.Base != "args" {
			cmds = append(cmds, cmd)
		}
	}

	return cmds
}

// ns is a command the fake minimega received in namespace exp.
func ns(base string) mmtest.Command {
	return mmtest.Command{Namespace: "exp", Base: base}
}

var ignoreRaw = cmpopts.IgnoreFields(mmtest.Command{}, "Raw") //nolint:gochecknoglobals // a cmp option

// Start deletes the old C2 responses and snapshots, has minimega read the script and launch the VMs, starts the VMs
// that are not delayed, creates the GRE bridge, and stores the experiment started with its VM hosts and VLANs.
func TestStartLaunchesTheExperiment(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name     string
		gre      bool
		nodes    func(image string) []map[string]any
		launches []mmtest.Command // the commands from vm launch to vm info, not included
		info     []string
	}{
		{
			name: "starts every VM at once when none is delayed",
			nodes: func(image string) []map[string]any {
				return []map[string]any{launchNode("a", image, nil)}
			},
			launches: []mmtest.Command{ns("vm launch"), ns("vm start all")},
		},
		{
			name: "starts only the VMs not delayed, and creates the GRE bridge",
			gre:  true,
			nodes: func(image string) []map[string]any {
				return []map[string]any{
					launchNode("a", image, nil),
					launchNode("b", image, map[string]any{"delay": map[string]any{"user": true}}),
					{"type": "VirtualMachine", "general": map[string]any{"hostname": "out"}, "external": true},
				}
			},
			launches: []mmtest.Command{ns("vm launch"), ns("vm start a"), ns("ns bridge br-exp gre")},
			info:     []string{"1 VM delayed - to be started by user: b"},
		},
		{
			name: "lists the VMs that the user starts in one note",
			nodes: func(image string) []map[string]any {
				return []map[string]any{
					launchNode("a", image, nil),
					launchNode("b", image, map[string]any{"delay": map[string]any{"user": true}}),
					launchNode("c", image, map[string]any{"delay": map[string]any{"user": true}}),
				}
			},
			launches: []mmtest.Command{ns("vm launch"), ns("vm start a")},
			info:     []string{"2 VMs delayed - to be started by user: b, c"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)

			nodes := tc.nodes(launchImage(t))
			base := storeLaunchExperiment(t, map[string]any{"useGREMesh": tc.gre, "defaultBridge": "br-exp"}, nodes...)

			files := useDeletedFiles(t)
			received := useLaunchMM(t, defaultLaunchReplies())
			started := countHooks(t, "start")

			ctx := notes.Context(t.Context(), false)

			if err := Start(ctx, StartWithName("exp")); err != nil {
				t.Fatalf("Start(exp) error = %v", err)
			}

			var snapshots []string
			for _, n := range nodes {
				if n["external"] == nil {
					snapshots = append(snapshots, "head_exp_"+n["general"].(map[string]any)["hostname"].(string)+"_snapshot")
				}
			}

			commands := append([]mmtest.Command{ns("read " + base + "/mm_files/exp.mm")}, tc.launches...)

			want := launched{
				Commands:  append(commands, ns("vm info"), ns("vlans")),
				Deleted:   append([]string{"exp/miniccc_responses"}, snapshots...),
				Info:      tc.info,
				Running:   true,
				Schedules: map[string]string{"a": "node1"},
				VLANs:     map[string]int{"EXP-1": 101},
				Hooks:     1,
			}

			got := launchResult(t, ctx, received, files, started)
			if diff := cmp.Diff(want, got, ignoreRaw, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Start(exp) mismatch (-want +got):\n%s", diff)
			}

			if got := experimentNamed(t, "exp").Status.StartTime(); strings.HasSuffix(got, "-DRYRUN") {
				t.Errorf("stored start time = %q, want one not marked as a dry run", got)
			}
		})
	}
}

// When a minimega step of Start fails, Start clears the namespace, leaves the experiment stopped with no start marker,
// and runs no start hook. With minimega errors as warnings, a failed step is a warning and the start goes on.
func TestStartUndoesAFailedStep(t *testing.T) { //nolint:paralleltest // replaces package globals
	failed := []*minicli.Response{mmtest.Error("head", "boom")}

	tests := []struct {
		name      string
		replies   launchReplies
		deleteErr error
		updateErr error
		asWarn    bool
		wantErr   string
		wantLast  string // the base of the last command sent, "" for none
		warnings  []string
	}{
		{
			name:     "reading the script",
			replies:  launchReplies{"read ": failed},
			wantErr:  "reading minimega script: reading mmcli script: 1 error occurred:\n\t* boom\n\n",
			wantLast: "clear namespace exp",
		},
		{
			name:     "launching the VMs",
			replies:  launchReplies{"vm launch": failed},
			wantErr:  "launching experiment VMs: launching VMs: 1 error occurred:\n\t* boom\n\n",
			wantLast: "clear namespace exp",
		},
		{
			name:    "creating the GRE bridge",
			replies: launchReplies{"ns bridge": failed},
			wantErr: "creating experiment bridge: creating bridge br-exp with GRE mesh between namespace hosts: " +
				"1 error occurred:\n\t* boom\n\n",
			wantLast: "clear namespace exp",
		},
		{
			name: "reading the VLANs",
			replies: launchReplies{
				"vlans": {mmtest.Tabular("head", []string{"alias", "vlan"}, []string{"EXP-1", "x"})},
			},
			wantErr:  `processing experiment VLANs: converting VLAN ID to integer: strconv.Atoi: parsing "x": invalid syntax`,
			wantLast: "clear namespace exp",
		},
		{
			name:      "storing the start",
			updateErr: errors.New("store is read-only"),
			wantErr:   "updating experiment config: store is read-only",
			wantLast:  "clear namespace exp",
		},
		{
			name:      "deleting the old C2 responses",
			deleteErr: errors.New("mesh unreachable"),
			wantErr: "deleting experiment snapshots and CC responses: deleting CC responses for experiment exp: " +
				"mesh unreachable",
		},
		{
			name:     "launching the VMs, with minimega errors as warnings",
			replies:  launchReplies{"vm launch": failed},
			asWarn:   true,
			wantLast: "vlans",
			warnings: []string{"boom"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &failStartStore{BoltDB: storetest.NewBolt(t), err: tc.updateErr}
			storetest.Use(t, s)

			storeLaunchExperiment(t, map[string]any{"useGREMesh": true, "defaultBridge": "br-exp"},
				launchNode("a", launchImage(t), nil))

			files := useDeletedFiles(t)
			files.err = tc.deleteErr

			replies := defaultLaunchReplies()
			maps.Copy(replies, tc.replies)

			received := useLaunchMM(t, replies)
			started := countHooks(t, "start")

			ctx := notes.Context(t.Context(), false)

			err := Start(ctx, StartWithName("exp"), StartWithMMErrorsAsWarnings(tc.asWarn))

			var gotErr string
			if err != nil {
				gotErr = err.Error()
			}

			if gotErr != tc.wantErr {
				t.Errorf("Start(exp) error = %q, want %q", gotErr, tc.wantErr)
			}

			var last string
			if cmds := sentCommands(received); len(cmds) > 0 {
				last = cmds[len(cmds)-1].Base
			}

			if got := last; got != tc.wantLast {
				t.Errorf("Start(exp) sent last %q, want %q", got, tc.wantLast)
			}

			got := notes.Warnings(ctx, true)
			warnings := make([]string, 0, len(got))

			for _, w := range got {
				warnings = append(warnings, w.Error())
			}

			if diff := cmp.Diff(tc.warnings, warnings, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Start(exp) warnings mismatch (-want +got):\n%s", diff)
			}

			exp := experimentNamed(t, "exp")
			wantRunning := tc.wantErr == ""

			if exp.Running() != wantRunning || exp.Status.Starting().PID != 0 {
				t.Errorf("stored experiment running = %t, start marker %+v, want running %t and no marker",
					exp.Running(), exp.Status.Starting(), wantRunning)
			}

			if wantHooks := map[bool]int{true: 1}[wantRunning]; *started != wantHooks {
				t.Errorf("Start(exp) ran %d start hooks, want %d", *started, wantHooks)
			}
		})
	}
}

// Start waits for a VM with a timer delay and then starts it, and stores the VLAN range it is given.
func TestStartStartsATimerDelayedVM(t *testing.T) { //nolint:paralleltest // replaces package globals
	storetest.Use(t)

	image := launchImage(t)
	base := storeLaunchExperiment(t, nil,
		launchNode("a", image, nil),
		launchNode("c", image, map[string]any{"delay": map[string]any{"timer": "1ms"}}),
	)

	files := useDeletedFiles(t)
	received := useLaunchMM(t, defaultLaunchReplies())
	started := countHooks(t, "start")

	ctx := notes.Context(t.Context(), false)

	if err := Start(ctx, StartWithName("exp"), StartWithVLANMin(200), StartWithVLANMax(300)); err != nil {
		t.Fatalf("Start(exp) error = %v", err)
	}

	want := launched{
		Commands: []mmtest.Command{
			ns("read " + base + "/mm_files/exp.mm"), ns("vm launch"), ns("vm start a"),
			ns("vm info"), ns("vlans"), ns("vm start c"),
		},
		Deleted: []string{"exp/miniccc_responses", "head_exp_a_snapshot", "head_exp_c_snapshot"},
		Info: []string{
			"1 VM delayed - will be started after a set time: c (after 1ms)", "Waiting for delayed VMs to be started...",
			"Time delayed VM c started",
		},
		Running:   true,
		Schedules: map[string]string{"a": "node1"},
		VLANs:     map[string]int{"EXP-1": 101},
		Hooks:     1,
	}

	got := launchResult(t, ctx, received, files, started)
	if diff := cmp.Diff(want, got, ignoreRaw, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("Start(exp) mismatch (-want +got):\n%s", diff)
	}

	vlans := experimentNamed(t, "exp").Spec.VLANs()
	if gotMin, gotMax := vlans.Min(), vlans.Max(); gotMin != 200 || gotMax != 300 {
		t.Errorf("stored VLAN range = %d-%d, want 200-300", gotMin, gotMax)
	}
}

// With an error channel, Start stores the experiment started with its C2-delayed VM pending and returns; the wait
// for the C2 clients goes on in the background, and a canceled start sends its error and closes the channel.
func TestStartWithAnErrorChannelWaitsInTheBackground(t *testing.T) { //nolint:paralleltest // replaces package globals
	storetest.Use(t)

	image := launchImage(t)
	storeLaunchExperiment(t, nil,
		launchNode("a", image, nil),
		launchNode("c", image, map[string]any{"delay": map[string]any{
			"c2": []any{map[string]any{"hostname": ""}, map[string]any{"hostname": "a"}},
		}}),
	)

	useDeletedFiles(t)
	useLaunchMM(t, defaultLaunchReplies())

	ctx, cancel := context.WithCancel(notes.Context(t.Context(), false))
	defer cancel()

	errs := make(chan error, 2)

	if err := Start(ctx, StartWithName("exp"), StartWithErrorChannel(errs)); err != nil {
		t.Fatalf("Start(exp) error = %v", err)
	}

	status := experimentNamed(t, "exp").Status

	if diff := cmp.Diff(map[string]map[string]bool{"c": {"a": false}}, status.DelayedC2()); diff != "" {
		t.Errorf("stored delayed C2 starts mismatch (-want +got):\n%s", diff)
	}

	if !status.PostStartPending() || status.StartTime() == "" {
		t.Errorf("stored post-start pending = %t, start time %q, want pending and a start time",
			status.PostStartPending(), status.StartTime())
	}

	cancel()

	var got []error
	for err := range errs {
		got = append(got, err)
	}

	if len(got) != 1 || !errors.Is(got[0], context.Canceled) {
		t.Errorf("errors sent after the start was canceled = %v, want one wrapping context.Canceled", got)
	}

	// a C2 delay with no hostname is left out of the note
	wantInfo := []string{
		"1 VM delayed - will be started when C2 is active on other VMs: c (after a)",
		"Waiting for delayed VMs to be started...",
	}
	if diff := cmp.Diff(wantInfo, notes.Info(ctx, true)); diff != "" {
		t.Errorf("Start(exp) info mismatch (-want +got):\n%s", diff)
	}
}

// Stop deletes the injection snapshots of the experiment's VMs after it kills them, and reports a failure to delete
// them with the experiment stopped.
func TestStopDeletesTheSnapshots(t *testing.T) { //nolint:paralleltest // replaces package globals
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{name: "snapshots deleted"},
		{
			name: "snapshots that cannot be deleted", err: errors.New("mesh unreachable"),
			wantErr: "1 error occurred:\n\t* deleting experiment snapshots: deleting snapshot files for experiment exp: " +
				"mesh unreachable\n\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storetest.Use(t)
			storeLaunchExperiment(t, nil, launchNode("a", "a.qc2", nil))

			c, _ := store.NewConfig("experiment/exp")
			if err := store.Get(c); err != nil {
				t.Fatalf("store.Get(experiment exp) error = %v", err)
			}

			c.Status = map[string]any{"startTime": stopTestStart}
			if err := store.Update(c); err != nil {
				t.Fatalf("store.Update(experiment exp) error = %v", err)
			}

			fake := useStartMM(t, &startMM{})
			files := useDeletedFiles(t)
			files.err = tc.err

			err := Stop("exp")
			if got := gotErr(err); got != tc.wantErr {
				t.Errorf("Stop(exp) error = %q, want %q", got, tc.wantErr)
			}

			if diff := cmp.Diff([]string{"exp"}, fake.cleared); diff != "" {
				t.Errorf("Stop(exp) cleared namespaces mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff([]string{"head_exp_a_snapshot"}, files.files()); diff != "" {
				t.Errorf("Stop(exp) deleted files mismatch (-want +got):\n%s", diff)
			}

			if experimentNamed(t, "exp").Running() {
				t.Error("experiment running after Stop(exp), want it stopped")
			}
		})
	}
}
