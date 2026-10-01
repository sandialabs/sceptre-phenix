package cmd

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"phenix/api/workflow"
)

func TestResolveTopologyDir(t *testing.T) {
	root := t.TempDir()
	topologies := filepath.Join(root, "topologies")

	writeWorkflowTree(t, root, map[string]string{
		"local/phenix.yml":                "",
		"both/phenix.yml":                 "",
		"file.txt":                        "",
		"topologies/named/phenix.yml":     "",
		"topologies/both/phenix.yml":      "",
		"topologies/sub/named/phenix.yml": "",
		"topologies/plain":                "",
	})
	t.Chdir(root)

	tests := []struct {
		name    string
		arg     string
		base    string
		want    string
		wantErr string
	}{
		{name: "empty argument", arg: "", base: topologies, wantErr: "must not be empty"},
		{name: "current directory", arg: ".", base: topologies, want: root},
		{name: "relative path", arg: "local", base: topologies, want: filepath.Join(root, "local")},
		{name: "absolute path", arg: filepath.Join(root, "local"), base: "", want: filepath.Join(root, "local")},
		{name: "name under topologies", arg: "named", base: topologies, want: filepath.Join(topologies, "named")},
		{name: "existing path wins over the name", arg: "both", base: topologies, want: filepath.Join(root, "both")},
		{
			name:    "missing name names both paths",
			arg:     "nope",
			base:    topologies,
			wantErr: "topology directory not found: tried nope and " + filepath.Join(topologies, "nope"),
		},
		{
			name:    "path with a separator is not looked up",
			arg:     filepath.Join("sub", "named"),
			base:    topologies,
			wantErr: "topology directory not found: stat " + filepath.Join("sub", "named"),
		},
		{name: "no topologies base", arg: "named", base: "", wantErr: "topology directory not found: stat named"},
		{name: "local file", arg: "file.txt", base: topologies, wantErr: "file.txt is not a directory"},
		{
			name:    "file under topologies",
			arg:     "plain",
			base:    topologies,
			wantErr: filepath.Join(topologies, "plain") + " is not a directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTopologyDir(tt.arg, tt.base)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveTopologyDir(%q) error = %v, want it to contain %q", tt.arg, err, tt.wantErr)
				}

				return
			}

			if err != nil || got != tt.want {
				t.Fatalf("resolveTopologyDir(%q) = %q, %v; want %q", tt.arg, got, err, tt.want)
			}
		})
	}
}

func TestResolveWorkflowConfig(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.yml")
	writeWorkflowTree(t, filepath.Dir(outside), map[string]string{"outside.yml": ""})

	tests := []struct {
		name     string
		files    []string
		config   string
		explicit bool
		want     string
		wantErr  string
	}{
		{name: "phenix.yml", files: []string{workflowConfigName}, config: workflowConfigName, want: workflowConfigName},
		{name: ".phenix.yml fallback", files: []string{legacyWorkflowConfig}, config: workflowConfigName, want: legacyWorkflowConfig},
		{
			name:    "both is an error",
			files:   []string{workflowConfigName, legacyWorkflowConfig},
			config:  workflowConfigName,
			wantErr: "both phenix.yml and .phenix.yml exist in ",
		},
		{name: "neither", config: workflowConfigName, want: ""},
		{name: "a directory is not a config", files: []string{"phenix.yml/x"}, config: workflowConfigName, want: ""},
		{
			name:     "-c is relative to the directory",
			files:    []string{workflowConfigName, "alt.yml"},
			config:   "alt.yml",
			explicit: true,
			want:     "alt.yml",
		},
		{name: "-c absolute", config: outside, explicit: true, want: outside},
		{
			name:     "-c chooses one of both",
			files:    []string{workflowConfigName, legacyWorkflowConfig},
			config:   legacyWorkflowConfig,
			explicit: true,
			want:     legacyWorkflowConfig,
		},
		{
			name:     "-c missing",
			files:    []string{workflowConfigName},
			config:   "nope.yml",
			explicit: true,
			wantErr:  "nope.yml does not exist or is not a file",
		},
		{name: "phenix.yaml is not read without -c", files: []string{"phenix.yaml"}, config: workflowConfigName, want: ""},
		{name: "-c phenix.yaml", files: []string{"phenix.yaml"}, config: "phenix.yaml", explicit: true, want: "phenix.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{}

			for _, file := range tt.files {
				files[file] = ""
			}

			writeWorkflowTree(t, dir, files)

			got, err := resolveWorkflowConfig(dir, tt.config, tt.explicit)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("resolveWorkflowConfig() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}

			want := tt.want
			if want != "" && !filepath.IsAbs(want) {
				want = filepath.Join(dir, want)
			}

			if err != nil || got != want {
				t.Fatalf("resolveWorkflowConfig() = %q, %v; want %q", got, err, want)
			}
		})
	}
}

func TestInjectsBaseDir(t *testing.T) {
	tests := []struct {
		name     string
		explicit bool
		server   map[string]any
		want     string
	}{
		{name: "explicit flag wins", explicit: true, server: map[string]any{optionInjectsBase: "/srv"}, want: "/local"},
		{name: "server value", server: map[string]any{optionInjectsBase: "/srv"}, want: "/srv"},
		{name: "server value missing", server: map[string]any{}, want: "/local"},
		{name: "server value empty", server: map[string]any{optionInjectsBase: ""}, want: "/local"},
		{name: "server value not a string", server: map[string]any{optionInjectsBase: 1}, want: "/local"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := applyOptions{InjectsBase: "/local", InjectsBaseExplicit: tt.explicit}

			if got := injectsBaseDir(opts, tt.server); got != tt.want {
				t.Errorf("injectsBaseDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunWorkflowApplyStopsBeforeContactingServer(t *testing.T) {
	tests := []struct {
		name     string
		dirName  string
		files    map[string]string
		emptyArg bool
		setup    func(opts *applyOptions)
		wantErr  string
		wantIs   error
	}{
		{name: "nothing to apply", files: map[string]string{"README.md": "hi"}, wantErr: "nothing to apply: "},
		{
			name:    "invalid -b",
			setup:   func(opts *applyOptions) { opts.Name = "bad name" },
			wantErr: "choose a valid workflow name with -b: ",
			wantIs:  workflow.ErrInvalidName,
		},
		{name: "invalid directory name", dirName: ".foo", wantIs: workflow.ErrInvalidName},
		{
			name:    "reserved -b",
			setup:   func(opts *applyOptions) { opts.Name = "phenix.yml" },
			wantErr: `the name "phenix.yml" is reserved; pass another with -b`,
		},
		{
			name:    "reserved -b in another letter case",
			setup:   func(opts *applyOptions) { opts.Name = "Phenix-Configs" },
			wantErr: `the name "Phenix-Configs" is reserved; pass another with -b`,
		},
		{
			name:    "reserved directory name",
			dirName: "phenix-injects",
			wantErr: `the name "phenix-injects" is reserved; pass another with -b`,
		},
		{
			name:    "both workflow configs",
			files:   map[string]string{workflowConfigName: wfWorkflowYAML, legacyWorkflowConfig: wfWorkflowYAML},
			wantErr: "remove one or pass -c",
		},
		{
			name:    "-c missing",
			setup:   func(opts *applyOptions) { opts.Config, opts.ConfigExplicit = "nope.yml", true },
			wantErr: "nope.yml does not exist or is not a file",
		},
		{name: "empty argument", emptyArg: true, wantErr: "must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirName, files := tt.dirName, tt.files
			if dirName == "" {
				dirName = "foo"
			}

			if files == nil {
				files = fullTopology()
			}

			dir := newTopologyDir(t, dirName, files)
			fake := newFakePhenix(t, fakePhenix{options: serverOptions(t.TempDir())})

			opts := testApplyOptions(fake.socket, t.TempDir())
			if tt.setup != nil {
				tt.setup(&opts)
			}

			arg := dir
			if tt.emptyArg {
				arg = ""
			}

			err := runWorkflowApply(t.Context(), opts, arg)

			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("runWorkflowApply() error = %v, want %v", err, tt.wantIs)
			}

			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("runWorkflowApply() error = %v, want it to contain %q", err, tt.wantErr)
			}

			checkRequests(t, fake)
		})
	}
}

func TestRunWorkflowApplyRefusesOldServer(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]any
	}{
		{name: "option missing", options: map[string]any{"bridge-mode": "manual"}},
		{name: "option false", options: map[string]any{optionWorkflowDryRun: false}},
		{name: "option not a bool", options: map[string]any{optionWorkflowDryRun: "true"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "foo", fullTopology())
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{options: tt.options})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, injects), dir)
			if err == nil || !strings.Contains(err.Error(), "does not support workflow dry runs; upgrade the phenix server") {
				t.Fatalf("runWorkflowApply() error = %v, want an upgrade error", err)
			}

			checkRequests(t, fake, reqOptions)
			assertEmptyDir(t, injects)
		})
	}
}

// TestRunWorkflowApplyNoConfigFiles runs directories whose only input is a
// phenix-configs/ with no file the command reads as a config, so that the
// run would deploy nothing.
func TestRunWorkflowApplyNoConfigFiles(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{name: "only README.md", files: map[string]string{"phenix-configs/README.md": "# notes\n"}},
		{name: "only a hidden file", files: map[string]string{"phenix-configs/.topology.yml": wfTopologyYAML}},
		{name: "only a template", files: map[string]string{"phenix-configs/topology.yaml.tmpl": wfTopologyYAML}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "foo", tt.files)
			fake := newFakePhenix(t, fakePhenix{options: serverOptions(t.TempDir())})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

			want := "nothing to apply: " + dir + " has no phenix-injects/, phenix.yml or .phenix.yml, " +
				"and phenix-configs/ holds no .json, .yaml or .yml file that is not hidden"
			if err == nil || err.Error() != want {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
			}

			checkRequests(t, fake, reqOptions)
		})
	}
}

// TestRunWorkflowApplyRefusesSymlinkedConfigsDir runs a topology directory
// whose phenix-configs is a symbolic link, which the walk would not follow.
func TestRunWorkflowApplyRefusesSymlinkedConfigsDir(t *testing.T) {
	tests := []struct {
		name   string
		target string // the link's target, relative to the topology directory
	}{
		{name: "to a directory with configs", target: "configs"},
		{name: "dangling", target: "missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "foo", map[string]string{
				"configs/topology.yml": wfTopologyYAML,
				workflowConfigName:     wfWorkflowYAML,
			})
			link := filepath.Join(dir, configsDirName)

			if err := os.Symlink(tt.target, link); err != nil {
				t.Fatalf("creating symlink: %v", err)
			}

			fake := newFakePhenix(t, fakePhenix{options: serverOptions(t.TempDir())})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

			want := link + " is a symbolic link; a symlinked configs directory is not supported"
			if err == nil || err.Error() != want {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
			}

			checkRequests(t, fake)
		})
	}
}

func TestRunWorkflowApplyServerUnavailable(t *testing.T) {
	none := filepath.Join(t.TempDir(), "none.sock")

	tests := []struct {
		name    string
		socket  string
		wantErr string
		wantIs  error
	}{
		{name: "no socket configured", socket: "", wantErr: "no phenix unix socket configured; set --unix-socket"},
		{
			name:    "nothing listening",
			socket:  none,
			wantErr: "reading the phenix server options via " + none + ": phenix server unreachable: ",
			wantIs:  errServerUnreachable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "foo", fullTopology())
			injects := t.TempDir()

			err := runWorkflowApply(t.Context(), testApplyOptions(tt.socket, injects), dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("runWorkflowApply() error = %v, want it to contain %q", err, tt.wantErr)
			}

			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Errorf("runWorkflowApply() error = %v, want %v", err, tt.wantIs)
			}

			assertEmptyDir(t, injects)
		})
	}
}

// TestRunWorkflowApplyServerNeverAnswers runs against a listener that
// accepts every connection and never answers: the options request gets no
// answer within the preflight bound, and nothing more is sent.
func TestRunWorkflowApplyServerNeverAnswers(t *testing.T) {
	lowerPreflightTimeout(t, 200*time.Millisecond)

	socket := shortSocketPath(t)

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listening on %s: %v", socket, err)
	}

	var (
		mu    sync.Mutex
		conns []net.Conn
	)

	t.Cleanup(func() {
		_ = listener.Close()

		mu.Lock()
		defer mu.Unlock()

		for _, conn := range conns {
			_ = conn.Close()
		}
	})

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()

	dir := newTopologyDir(t, "foo", fullTopology())
	injects := t.TempDir()

	err = runWorkflowApply(t.Context(), testApplyOptions(socket, injects), dir)

	want := "reading the phenix server options via " + socket + ": phenix server unreachable: no answer within 200ms"
	if !errors.Is(err, errServerUnreachable) || err == nil || err.Error() != want {
		t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
	}

	mu.Lock()
	accepted := len(conns)
	mu.Unlock()

	if accepted != 1 {
		t.Errorf("the listener accepted %d connections, want 1: nothing after the options request", accepted)
	}

	assertEmptyDir(t, injects)
}

// TestRunWorkflowApplySocketPermissionDenied runs as a user who may not
// write to the socket, as when phenix ui runs as root: the run stops at the
// options request, and the returned error is the only report.
func TestRunWorkflowApplySocketPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores socket permissions")
	}

	logs := captureLogs(t)
	dir := newTopologyDir(t, "helloworld", fullTopology())
	injects := t.TempDir()
	fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})

	if err := os.Chmod(fake.socket, 0o555); err != nil {
		t.Fatalf("making %s read-only: %v", fake.socket, err)
	}

	err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

	prefix := "reading the phenix server options via " + fake.socket + ": phenix server unreachable: "
	suffix := "dial unix " + fake.socket + ": connect: permission denied; " + socketPermissionHint

	if err == nil || !strings.HasPrefix(err.Error(), prefix) || !strings.HasSuffix(err.Error(), suffix) {
		t.Fatalf("runWorkflowApply() error = %v, want %q, then %q", err, prefix, suffix)
	}

	if !errors.Is(err, fs.ErrPermission) || !errors.Is(err, errServerUnreachable) {
		t.Errorf("runWorkflowApply() error = %v, want fs.ErrPermission and errServerUnreachable", err)
	}

	checkRequests(t, fake)
	assertEmptyDir(t, injects)

	if errs := logs.messages(t, "ERROR"); len(errs) != 0 {
		t.Errorf("logged error records: %q", errs)
	}
}

func TestRunWorkflowApplyReportsParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name:    "config syntax error",
			files:   map[string]string{"phenix-configs/bad.yml": "metadata:\n\tname: x\n", workflowConfigName: wfWorkflowYAML},
			wantErr: filepath.Join(configsDirName, "bad.yml") + ":2: found character that cannot start any token",
		},
		{
			name:    "config without kind",
			files:   map[string]string{"phenix-configs/nokind.yml": "apiVersion: phenix.sandia.gov/v1\nmetadata: {}\n"},
			wantErr: "nokind.yml:1:1: missing kind",
		},
		{
			name:    "config with two documents",
			files:   map[string]string{"phenix-configs/two.yml": wfTopologyYAML + "---\n" + wfScenarioYAML},
			wantErr: "two.yml:7:1: found a second YAML document",
		},
		{
			name:    "workflow config syntax error",
			files:   map[string]string{workflowConfigName: "spec:\n\tauto: {}\n"},
			wantErr: "phenix.yml:2: found character that cannot start any token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.files["phenix-injects/a.txt"] = "a"
			dir := newTopologyDir(t, "foo", tt.files)
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("runWorkflowApply() error = %v, want it to contain %q", err, tt.wantErr)
			}

			checkRequests(t, fake, reqOptions)
			assertEmptyDir(t, injects)
		})
	}
}

// TestRunWorkflowApplyIgnoresPhenixYAML runs a directory whose workflow
// config is named phenix.yaml, which is not read without -c. What
// --log.level warn shows names the ignored file before the two skips, so
// that the run does not read as a deploy.
func TestRunWorkflowApplyIgnoresPhenixYAML(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "topo", map[string]string{
		"phenix-injects/a.txt":        "a",
		"phenix-configs/topology.yml": wfTopologyYAML,
		"phenix.yaml":                 wfWorkflowYAML,
	})
	injects := t.TempDir()
	fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})

	if err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	checkRequests(t, fake, reqOptions, reqConfigDryRun("topo"), reqConfig("topo"))

	if _, err := os.Stat(filepath.Join(injects, "topo", "a.txt")); err != nil {
		t.Errorf("injects not staged: %v", err)
	}

	want := []string{
		"ignoring a possible workflow config",
		"no workflow config; skipping the dry run",
		"no workflow config; skipping apply",
	}
	if got := logs.messages(t, "WARN"); !reflect.DeepEqual(got, want) {
		t.Errorf("warn records = %q, want %q", got, want)
	}

	if errs := logs.messages(t, "ERROR"); len(errs) != 0 {
		t.Errorf("logged error records: %q", errs)
	}

	checkFields(t, logs.first(t, "ignoring a possible workflow config"), map[string]string{
		"level": "WARN",
		"type":  "SYSTEM",
		"step":  "preflight",
		"dir":   dir,
		"file":  "phenix.yaml",
		"hint":  "rename it to phenix.yml, or pass -c phenix.yaml",
	})
}

func TestRunWorkflowApplyExplicitInjectsBase(t *testing.T) {
	logs := captureLogs(t)
	dir := newTopologyDir(t, "helloworld", map[string]string{"phenix-injects/a.txt": "a"})
	serverInjects := t.TempDir()
	localInjects := t.TempDir()
	fake := newFakePhenix(t, fakePhenix{options: serverOptions(serverInjects)})

	opts := testApplyOptions(fake.socket, localInjects)
	opts.InjectsBaseExplicit = true

	if err := runWorkflowApply(t.Context(), opts, dir); err != nil {
		t.Fatalf("runWorkflowApply() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(localInjects, "helloworld", "a.txt")); err != nil {
		t.Errorf("injects not staged under --base-dir.injects: %v", err)
	}

	assertEmptyDir(t, serverInjects)
	checkFields(t, logs.first(t, "phenix server ready"), map[string]string{"injects": localInjects})
}

// TestRunWorkflowApplyRefusesRelativeInjectsDir runs against a server that
// reports a relative injects directory, which would be resolved against the
// working directory, unless --base-dir.injects overrides it or there is
// nothing to stage.
func TestRunWorkflowApplyRefusesRelativeInjectsDir(t *testing.T) {
	injectsOnly := map[string]string{"phenix-injects/a.txt": "a"}

	tests := []struct {
		name     string
		files    map[string]string
		explicit bool
		want     []string
		wantErr  string
		staged   bool
	}{
		{
			name:    "from the server",
			files:   injectsOnly,
			want:    []string{reqOptions},
			wantErr: `the phenix server reports the relative injects directory "relative/injects"; pass --base-dir.injects`,
		},
		{name: "--base-dir.injects given", files: injectsOnly, explicit: true, want: []string{reqOptions}, staged: true},
		{
			name: "no phenix-injects",
			files: map[string]string{
				"phenix-configs/scenario.yml": wfScenarioYAML,
				"phenix-configs/topology.yml": wfTopologyYAML,
				workflowConfigName:            wfWorkflowYAML,
			},
			want: fullRun("foo", workflow.ActionUpdate),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newTopologyDir(t, "foo", tt.files)
			cwd := t.TempDir()
			t.Chdir(cwd)

			fake := newFakePhenix(t, fakePhenix{
				options: serverOptions("relative/injects"),
				plan:    workflow.Plan{Action: workflow.ActionUpdate, Experiment: "foo", Reason: ""},
			})

			local := t.TempDir()
			opts := testApplyOptions(fake.socket, local)
			opts.InjectsBaseExplicit = tt.explicit

			err := runWorkflowApply(t.Context(), opts, dir)

			checkRequests(t, fake, tt.want...)
			assertEmptyDir(t, cwd)

			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("runWorkflowApply() error = %v, want %q", err, tt.wantErr)
				}

				assertEmptyDir(t, local)

				return
			}

			if err != nil {
				t.Fatalf("runWorkflowApply() error = %v", err)
			}

			if _, statErr := os.Stat(filepath.Join(local, "foo", "a.txt")); (statErr == nil) != tt.staged {
				t.Errorf("injects staged under --base-dir.injects = %v, want %v", statErr == nil, tt.staged)
			}
		})
	}
}

// TestRunWorkflowApplyRefusesMarkerNamesInInjects runs topology directories
// whose phenix-injects holds an entry named like one that marks a topology
// directory, in the two levels the preflight looks at, in any letter case.
// "<dir>" in an expected error is the topology directory.
func TestRunWorkflowApplyRefusesMarkerNamesInInjects(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name:    "a file phenix.yml at the top of phenix-injects",
			files:   map[string]string{"phenix-injects/phenix.yml": "x", "phenix-injects/a.txt": "a"},
			wantErr: "<dir>/phenix-injects holds phenix.yml, a name that marks a topology directory; rename it or move it deeper",
		},
		{
			name:    "a directory phenix-configs one level down",
			files:   map[string]string{"phenix-injects/sub/phenix-configs/topology.yml": "x"},
			wantErr: "<dir>/phenix-injects holds sub/phenix-configs, a name that marks a topology directory; rename it or move it deeper",
		},
		{
			name:    "a name in another letter case",
			files:   map[string]string{"phenix-injects/sub/.PHENIX.yml": "x"},
			wantErr: "<dir>/phenix-injects holds sub/.PHENIX.yml, a name that marks a topology directory; rename it or move it deeper",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			dir := newTopologyDir(t, "foo", tt.files)
			injects := t.TempDir()
			fake := newFakePhenix(t, fakePhenix{options: serverOptions(injects)})
			before := treeSnapshot(t, dir)

			err := runWorkflowApply(t.Context(), testApplyOptions(fake.socket, t.TempDir()), dir)

			if want := strings.ReplaceAll(tt.wantErr, "<dir>", dir); err == nil || err.Error() != want {
				t.Fatalf("runWorkflowApply() error = %v, want %q", err, want)
			}

			checkRequests(t, fake, reqOptions)
			assertEmptyDir(t, injects)

			if after := treeSnapshot(t, dir); !reflect.DeepEqual(after, before) {
				t.Errorf("the run changed the topology directory:\nbefore %v\nafter  %v", before, after)
			}

			checkNotLogged(t, logs, "staging injects")
		})
	}
}
