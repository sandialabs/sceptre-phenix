package cmd

import (
	"bytes"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/viper"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/common"
	"phenix/util/plog"
	"phenix/web/rbac"
)

// rootCmdRestoreGlobals restores, when the test ends, the settings in common,
// the store, the log level, the console log file and the system log file that
// the root command sets, and removes the console and UI log handlers it adds.
func rootCmdRestoreGlobals(t *testing.T) {
	t.Helper()

	var (
		socket, phenixBase, injects = common.UnixSocket, common.PhenixBase, common.InjectsBase
		topologies, templates       = common.TopologiesBase, common.BuilderTemplatesBase
		minimega, mount, suffixes   = common.MinimegaBase, common.MountBase, common.HostnameSuffixes
		endpoint, bridge, deploy    = common.StoreEndpoint, common.BridgeMode, common.DeployMode
		gre, diskCheck              = common.UseGREMesh, common.CurrentDiskCheck()
		db, level, console          = store.DefaultStore, plog.Level.Level(), currentConsoleFile
		systemLog                   = plog.LogFilePath()
	)

	t.Cleanup(func() {
		common.UnixSocket, common.PhenixBase, common.InjectsBase = socket, phenixBase, injects     //nolint:reassign // restore
		common.TopologiesBase, common.BuilderTemplatesBase = topologies, templates                 //nolint:reassign // restore
		common.MinimegaBase, common.MountBase, common.HostnameSuffixes = minimega, mount, suffixes //nolint:reassign // restore
		common.StoreEndpoint, common.BridgeMode, common.DeployMode = endpoint, bridge, deploy      //nolint:reassign // restore
		common.UseGREMesh = gre                                                                    //nolint:reassign // restore
		_ = common.SetDiskCheck(string(diskCheck))
		store.DefaultStore = db //nolint:reassign // restore

		plog.Level.Set(level)
		plog.RemoveHandler("phenix-default")
		plog.RemoveHandler("ui-default")

		if currentConsoleFile != nil && currentConsoleFile != console {
			_ = currentConsoleFile.Close()
		}

		currentConsoleFile = console

		_ = plog.CloseFile()
		// ChangeLogFile("") writes lookupCache.json in the working directory and a path
		// in a removed directory panics, so restore only a path whose directory exists.
		if _, err := os.Stat(filepath.Dir(systemLog)); systemLog != "" && err == nil {
			plog.ChangeLogFile(systemLog)
		}
	})
}

// rootCmdOptionsServer serves body as the options of a phenix ui on a new
// unix socket, and returns the socket and the paths that clients asked for.
func rootCmdOptionsServer(t *testing.T, body string) (string, func() []string) {
	t.Helper()

	socket := shortSocketPath(t)

	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("net.Listen(unix, %s) error = %v", socket, err)
	}

	var (
		mu    sync.Mutex
		paths []string
	)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	srv.Listener = listener
	srv.Start()
	t.Cleanup(srv.Close)

	return socket, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), paths...)
	}
}

// configureModes sets the bridge, deploy and GRE mesh modes from the local
// settings and from a phenix ui. A server in auto bridge mode overrides a
// local manual mode, a local deploy mode overrides the server's, and GRE mesh
// is on when either side turns it on.
func TestRootCmdConfigureModes(t *testing.T) { //nolint:paralleltest // sets the modes in common
	rootCmdRestoreGlobals(t)

	type modes struct {
		Bridge common.BridgingMode
		Deploy common.DeploymentMode
		GRE    bool
		Disk   common.DiskCheck
	}

	tests := []struct {
		name    string
		local   map[string]any
		server  string // the options body; "" is no server
		want    modes
		wantErr bool
	}{
		{name: "defaults", want: modes{common.BridgeModeManual, common.DeployModeNoHeadnode, false, common.DiskCheckError}},
		{
			name: "local only",
			local: map[string]any{
				"bridge-mode": "AUTO", "deploy-mode": "all", "use-gre-mesh": true, "experiment.disk-check": "warn",
			},
			want: modes{common.BridgeModeAuto, common.DeployModeAll, true, common.DiskCheckWarn},
		},
		{
			name: "server auto overrides local manual", local: map[string]any{"bridge-mode": "manual"},
			server: `{"bridge-mode":"auto"}`,
			want:   modes{common.BridgeModeAuto, common.DeployModeNoHeadnode, false, common.DiskCheckError},
		},
		{
			name: "server manual keeps local auto", local: map[string]any{"bridge-mode": "auto"},
			server: `{"bridge-mode":"manual"}`,
			want:   modes{common.BridgeModeAuto, common.DeployModeNoHeadnode, false, common.DiskCheckError},
		},
		{
			name: "server deploy mode", server: `{"deploy-mode":"only-headnode"}`,
			want: modes{common.BridgeModeManual, common.DeployModeOnlyHeadnode, false, common.DiskCheckError},
		},
		{
			name: "local deploy mode overrides server", local: map[string]any{"deploy-mode": "all"},
			server: `{"deploy-mode":"only-headnode"}`,
			want:   modes{common.BridgeModeManual, common.DeployModeAll, false, common.DiskCheckError},
		},
		{
			name: "server GRE mesh", server: `{"use-gre-mesh":true}`,
			want: modes{common.BridgeModeManual, common.DeployModeNoHeadnode, true, common.DiskCheckError},
		},
		{
			name: "local GRE mesh", local: map[string]any{"use-gre-mesh": true}, server: `{"use-gre-mesh":false}`,
			want: modes{common.BridgeModeManual, common.DeployModeNoHeadnode, true, common.DiskCheckError},
		},
		{
			name: "server answer not JSON", server: "<html>401</html>",
			want: modes{common.BridgeModeManual, common.DeployModeNoHeadnode, false, common.DiskCheckError},
		},
		{name: "bad local bridge mode", local: map[string]any{"bridge-mode": "bogus"}, wantErr: true},
		{name: "bad local deploy mode", local: map[string]any{"deploy-mode": "bogus"}, wantErr: true},
		{name: "bad server deploy mode", server: `{"deploy-mode":"bogus"}`, wantErr: true},
		{name: "bad disk check", local: map[string]any{"experiment.disk-check": "bogus"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			common.BridgeMode, common.DeployMode, common.UseGREMesh = common.BridgeModeManual, common.DeployModeNoHeadnode, false //nolint:reassign // start state
			_ = common.SetDiskCheck("")

			v := viper.New()
			for key, value := range tc.local {
				v.Set(key, value)
			}

			// a socket with no server behind it is a phenix ui that is not there
			socket, paths := shortSocketPath(t), func() []string { return nil }
			wantPaths := []string(nil)

			if tc.server != "" {
				socket, paths = rootCmdOptionsServer(t, tc.server)
				wantPaths = []string{"/api/v1/options"}
			}

			v.Set("unix-socket", socket)

			err := configureModes(v)
			if (err != nil) != tc.wantErr {
				t.Fatalf("configureModes() error = %v, want error %t", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			got := modes{common.BridgeMode, common.DeployMode, common.UseGREMesh, common.CurrentDiskCheck()}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("configureModes() modes mismatch (-want +got):\n%s", diff)
			}

			if diff := cmp.Diff(wantPaths, paths()); diff != "" {
				t.Errorf("configureModes() requests mismatch (-want +got):\n%s", diff)
			}

			if common.UnixSocket != socket {
				t.Errorf("configureModes() set common.UnixSocket = %q, want %q", common.UnixSocket, socket)
			}
		})
	}
}

// consoleLogOutput gives stderr for "" and stderr, stdout for stdout, and
// else the file at the path, which it creates with its directory.
func TestRootCmdConsoleLogOutput(t *testing.T) {
	t.Parallel()

	for out, want := range map[string]*os.File{"": os.Stderr, consoleStderr: os.Stderr, consoleStdout: os.Stdout} {
		w, f, err := consoleLogOutput(out)
		if w != want || f != nil || err != nil {
			t.Errorf("consoleLogOutput(%q) = %v, %v, %v; want %v, nil, nil", out, w, f, err, want)
		}
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "console.log")
	writeTestFile(t, dir, "file", "")

	w, f, err := consoleLogOutput(path)
	if err != nil {
		t.Fatalf("consoleLogOutput(%s) error = %v", path, err)
	}

	t.Cleanup(func() { _ = f.Close() })

	if w != f || f.Name() != path {
		t.Errorf("consoleLogOutput(%s) = %v, %v; want the file opened twice as the same *os.File", path, w, f)
	}

	for _, bad := range []string{filepath.Join(dir, "file", "console.log"), dir} {
		if _, f, err := consoleLogOutput(bad); err == nil {
			_ = f.Close()
			t.Errorf("consoleLogOutput(%s) error = nil, want an error", bad)
		}
	}
}

// configureRoot sets the settings in common from v, logs to the console file
// at the log level, opens and initializes the store, and returns the system
// log file and its rotation options.
func TestRootCmdConfigureRoot(t *testing.T) { //nolint:paralleltest // sets common, the store and the logger
	rootCmdRestoreGlobals(t)

	var (
		dir      = t.TempDir()
		console  = filepath.Join(dir, "logs", "console.log")
		endpoint = "bolt://" + filepath.Join(dir, "store.bdb")
		base     = filepath.Join(dir, "phenix")
	)

	v := viper.New()
	for key, value := range map[string]any{
		"store.endpoint": endpoint, "log.console": console, "log.level": "debug",
		"base-dir.phenix": base, "base-dir.topologies": "/topo", "base-dir.minimega": "/mm",
		"mount-dir": "/mnt", "hostname-suffixes": "-x", "log.system.path": filepath.Join(dir, "sys.log"),
		"log.system.max-size": 5, "log.system.max-backups": 2, "log.system.max-age": 9,
	} {
		v.Set(key, value)
	}

	logFile, opts, err := configureRoot(v)
	if err != nil {
		t.Fatalf("configureRoot() error = %v", err)
	}

	if logFile != filepath.Join(dir, "sys.log") {
		t.Errorf("configureRoot() log file = %q, want %q", logFile, filepath.Join(dir, "sys.log"))
	}

	if diff := cmp.Diff(plog.FileHandlerOpts{MaxSize: 5, MaxAge: 9, MaxBackups: 2}, opts); diff != "" {
		t.Errorf("configureRoot() file handler options mismatch (-want +got):\n%s", diff)
	}

	got := []string{
		common.PhenixBase, common.InjectsBase, common.TopologiesBase, common.BuilderTemplatesBase,
		common.MinimegaBase, common.MountBase, common.HostnameSuffixes, common.StoreEndpoint, common.UnixSocket,
	}
	want := []string{
		base, filepath.Join(base, "injects"), "/topo", filepath.Join(base, "builder", "templates"),
		"/mm", "/mnt", "-x", endpoint, "",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("configureRoot() settings in common mismatch (-want +got):\n%s", diff)
	}

	if !store.IsInitialized(store.ComponentConfigs) {
		t.Errorf("configureRoot() left the store's configs not initialized")
	}

	if _, err := config.Get("role/global-admin"); err != nil {
		t.Errorf("after configureRoot(), config.Get(role/global-admin) error = %v, want the default role stored", err)
	}

	if plog.Level.Level() != slog.LevelDebug {
		t.Errorf("configureRoot() log level = %v, want %v", plog.Level.Level(), slog.LevelDebug)
	}

	plog.Debug(plog.TypeSystem, "root command console check")

	if data, _ := os.ReadFile(console); !strings.Contains(string(data), "root command console check") {
		t.Errorf("console log file holds %q, want the debug message", data)
	}

	if currentConsoleFile == nil || currentConsoleFile.Name() != console {
		t.Errorf("configureRoot() console file = %v, want %s", currentConsoleFile, console)
	}
}

// configureRoot stops at the first setting it cannot apply.
func TestRootCmdConfigureRootRefuses(t *testing.T) { //nolint:paralleltest // sets the environment, common and the store
	rootCmdRestoreGlobals(t)

	dir := t.TempDir()
	writeTestFile(t, dir, "file", "")

	tests := []struct {
		name     string
		env      string // PHENIX_COLOR, which colorMode reads
		set      map[string]any
		baseDirs bool // whether configureRoot sets the base directories before it stops
	}{
		{name: "bridge mode", set: map[string]any{"bridge-mode": "bogus"}},
		{name: "color", env: "bogus"},
		{name: "console under a file", set: map[string]any{"log.console": filepath.Join(dir, "file", "c.log")}},
		{name: "store endpoint", set: map[string]any{"store.endpoint": "etcd://x"}, baseDirs: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PHENIX_COLOR", tc.env)

			common.PhenixBase = "unset" //nolint:reassign // shows whether configureRoot got this far

			base := t.TempDir()
			v := viper.New()
			v.Set("store.endpoint", "bolt://"+filepath.Join(t.TempDir(), "store.bdb"))
			v.Set("base-dir.phenix", base)

			for key, value := range tc.set {
				v.Set(key, value)
			}

			if _, _, err := configureRoot(v); err == nil {
				t.Errorf("configureRoot() with a bad %s error = nil, want an error", tc.name)
			}

			want := map[bool]string{true: base, false: "unset"}[tc.baseDirs]
			if common.PhenixBase != want {
				t.Errorf("configureRoot() with a bad %s set common.PhenixBase = %q, want %q", tc.name, common.PhenixBase, want)
			}
		})
	}
}

// After a config change, applyConfigChange sets the log level, the system log
// file, the disk check and the console log file, and closes the console file
// it replaces. A disk check it refuses keeps the current one, with a warning.
func TestRootCmdApplyConfigChange(t *testing.T) { //nolint:paralleltest // sets the logger and the disk check
	rootCmdRestoreGlobals(t)

	dir := t.TempDir()
	old, err := os.Create(filepath.Join(dir, "old.log"))
	if err != nil {
		t.Fatalf("os.Create() error = %v", err)
	}

	currentConsoleFile = old

	var (
		console = filepath.Join(dir, "new", "console.log")
		system  = filepath.Join(dir, "sys", "phenix.log")
	)

	if err := os.MkdirAll(filepath.Dir(system), 0o750); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}

	v := viper.New()
	v.Set("log.level", "warn")
	v.Set("log.system.path", system)
	v.Set("experiment.disk-check", "off")
	v.Set("log.console", console)

	applyConfigChange(v, fsnotify.Event{Name: filepath.Join(dir, "config.yaml")})

	if plog.Level.Level() != slog.LevelWarn {
		t.Errorf("after applyConfigChange(), log level = %v, want %v", plog.Level.Level(), slog.LevelWarn)
	}

	if got := plog.LogFilePath(); got != system {
		t.Errorf("after applyConfigChange(), plog.LogFilePath() = %q, want %q", got, system)
	}

	if got := common.CurrentDiskCheck(); got != common.DiskCheckOff {
		t.Errorf("after applyConfigChange(), disk check = %q, want %q", got, common.DiskCheckOff)
	}

	if _, err := old.WriteString("x"); !errors.Is(err, os.ErrClosed) {
		t.Errorf("writing the replaced console file: error = %v, want %v", err, os.ErrClosed)
	}

	logs := captureLogs(t)

	v.Set("experiment.disk-check", "bogus")
	applyConfigChange(v, fsnotify.Event{Name: filepath.Join(dir, "config.yaml")})

	if got := common.CurrentDiskCheck(); got != common.DiskCheckOff {
		t.Errorf("after applyConfigChange() with a bad disk check, disk check = %q, want %q", got, common.DiskCheckOff)
	}

	if got := len(logs.all(t, "keeping the current experiment.disk-check")); got != 1 {
		t.Errorf("applyConfigChange() with a bad disk check logged %d warnings, want 1", got)
	}

	if data, _ := os.ReadFile(console); !strings.Contains(string(data), "keeping the current experiment.disk-check") {
		t.Errorf("console log file holds %q, want the warning", data)
	}

	// a console path below a file cannot be opened, so logs go to stderr
	replaced := currentConsoleFile
	v.Set("log.console", filepath.Join(old.Name(), "console.log"))
	applyConfigChange(v, fsnotify.Event{Name: filepath.Join(dir, "config.yaml")})

	if currentConsoleFile != nil {
		t.Errorf("after applyConfigChange() with a console path below a file, console file = %v, want none", currentConsoleFile.Name())
	}

	if _, err := replaced.WriteString("x"); !errors.Is(err, os.ErrClosed) {
		t.Errorf("writing the replaced console file: error = %v, want %v", err, os.ErrClosed)
	}
}

// A change to the users file makes phenix ui add the users in ui.users. A
// change to the config file does not.
func TestRootCmdApplyConfigChangeUsers(t *testing.T) { //nolint:paralleltest // replaces the store and sets the logger
	rootCmdRestoreGlobals(t)
	storetest.UseBolt(t)

	common.PhenixBase = t.TempDir() //nolint:reassign // default configs are read below it

	if err := config.Init(); err != nil {
		t.Fatalf("config.Init() error = %v", err)
	}

	dir := t.TempDir()
	v := viper.New()
	v.Set("log.system.path", filepath.Join(dir, "phenix.log"))
	v.Set("ui.users", []string{"alice:secret:Global Admin"})

	applyConfigChange(v, fsnotify.Event{Name: filepath.Join(dir, "config.yaml")})

	if _, err := rbac.GetUser("alice"); err == nil {
		t.Errorf("after a config file change, rbac.GetUser(alice) error = nil, want no user")
	}

	applyConfigChange(v, fsnotify.Event{Name: filepath.Join(dir, "users.yml")})

	u, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatalf("after a users file change, rbac.GetUser(alice) error = %v", err)
	}

	if u.RoleName() != "Global Admin" {
		t.Errorf("after a users file change, alice's role = %q, want %q", u.RoleName(), "Global Admin")
	}
}

// invokingUser gives the user that ran sudo phenix only for root, with
// SUDO_USER set, when sudo ran phenix itself. A SUDO_USER it cannot find
// gives root, with a message.
func TestRootCmdInvokingUser(t *testing.T) {
	t.Parallel()

	var (
		root  = &user.User{Uid: "0", HomeDir: "/root"}
		alice = &user.User{Uid: "1000", HomeDir: "/home/alice"}
		bob   = &user.User{Uid: "1001", HomeDir: "/home/bob"}
	)

	lookup := func(name string) (*user.User, error) {
		if name == "bob" {
			return bob, nil
		}

		return nil, user.UnknownUserError(name)
	}

	tests := []struct {
		name      string
		u         *user.User
		sudo      string
		ran       bool
		wantUID   string
		wantHome  string
		wantWarns string
	}{
		{name: "not root", u: alice, sudo: "bob", ran: true, wantUID: "1000", wantHome: "/home/alice"},
		{name: "root without sudo", u: root, ran: true, wantUID: "0", wantHome: "/root"},
		{name: "root shell from sudo", u: root, sudo: "bob", wantUID: "0", wantHome: "/root"},
		{name: "sudo phenix", u: root, sudo: "bob", ran: true, wantUID: "1001", wantHome: "/home/bob"},
		{
			name: "unknown sudo user", u: root, sudo: "ghost", ran: true, wantUID: "0", wantHome: "/root",
			wantWarns: "Could not find SUDO_USER ghost. Looking for optional config file in sudo home directory\n",
		},
	}

	for _, tc := range tests {
		var warn bytes.Buffer

		uid, home := invokingUser(tc.u, tc.sudo, tc.ran, lookup, &warn)
		if uid != tc.wantUID || home != tc.wantHome || warn.String() != tc.wantWarns {
			t.Errorf("%s: invokingUser() = %q, %q (warning %q), want %q, %q (warning %q)",
				tc.name, uid, home, warn.String(), tc.wantUID, tc.wantHome, tc.wantWarns)
		}
	}
}

// Without SUDO_USER, getCurrentUserInfo gives the uid and home of the user
// running phenix.
func TestRootCmdGetCurrentUserInfo(t *testing.T) { //nolint:paralleltest // sets the environment
	t.Setenv("SUDO_USER", "")

	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() error = %v", err)
	}

	if uid, home := getCurrentUserInfo(); uid != current.Uid || home != current.HomeDir {
		t.Errorf("getCurrentUserInfo() = %q, %q; want %q, %q", uid, home, current.Uid, current.HomeDir)
	}
}
