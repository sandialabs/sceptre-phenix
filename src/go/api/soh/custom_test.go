package soh

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"phenix/util/common"
	"phenix/util/mm"
	"phenix/util/mm/mmtest"
)

// useSOHFilesDir makes a new <PhenixBase>/images the minimega files directory
// until the test ends, with no minimega answering, and returns PhenixBase.
func useSOHFilesDir(t *testing.T) string {
	t.Helper()

	mmtest.Use(t, nil)

	base := t.TempDir()

	original := common.PhenixBase
	common.PhenixBase = base //nolint:reassign // the files directory's parent

	t.Cleanup(func() { common.PhenixBase = original }) //nolint:reassign // restore

	if err := os.Mkdir(filepath.Join(base, "images"), 0o750); err != nil {
		t.Fatalf("os.Mkdir(images) error = %v", err)
	}

	return base
}

// filesUnder lists each regular file under dir, relative to dir, sorted.
func filesUnder(t *testing.T, dir string) []string {
	t.Helper()

	var files []string

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, err := filepath.Rel(dir, p)
		files = append(files, filepath.ToSlash(rel))

		return err
	})
	if err != nil {
		t.Fatalf("filepath.WalkDir(%s) error = %v", dir, err)
	}

	slices.Sort(files)

	return files
}

func TestCustomScriptName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host, name, want string
	}{
		{host: "web", name: "check", want: "web-check"},
		{host: "web", name: "two  words\there", want: "web-two_words_here-1891251e"},
		{host: "web", name: "../../etc/cron.d/x", want: "web-.._.._etc_cron.d_x-6544ccb3"},
		{host: "web", name: `a\b`, want: "web-a_b-d45dc538"},
		{host: "web", name: `'q"#$y`, want: "web-_q___y-992e17f5"},
		{host: "web", name: "..", want: "web-.."},
		{host: "web", name: "db_check", want: "web-db_check"},
		{host: "web", name: "db/check", want: "web-db_check-420ef7c9"},
		{host: "web", name: "db check", want: "web-db_check-006f25d3"},
		{host: "../up", name: "t", want: ".._up-t-42c26807"},
		{host: "", name: "", want: "-"},
		{host: "web", name: "café\x00\xff", want: "web-caf___-b5cf3060"},
	}

	for _, tt := range tests {
		if got := customScriptName(tt.host, tt.name); got != tt.want {
			t.Errorf("customScriptName(%q, %q) = %q, want %q", tt.host, tt.name, got, tt.want)
		}
	}
}

// A custom test writes its script in the experiment's folder of the minimega
// files directory, whatever its name, and quotes the paths it sends to
// cc send and cc exec. A path that cannot stay in the folder or cannot reach
// the VM unchanged gives an error, and no file is written.
func TestCustomTestKeepsTheScriptInTheFilesDirectory(t *testing.T) { //nolint:paralleltest // replaces package globals
	const powershell = "powershell -NoProfile -ExecutionPolicy bypass -File"

	tests := []struct {
		name      string
		ns, host  string
		osType    string
		testName  string
		wantFiles []string
		wantCmds  []string
		wantState string
	}{
		{
			name: "name that leaves the folder", ns: "exp", host: "web", osType: "linux",
			testName:  `../../../../etc/cron.d/x 'q"#$y`,
			wantFiles: []string{"images/exp/web-.._.._.._.._etc_cron.d_x__q___y-8a202bd8"},
			wantCmds: []string{
				"exp/web: send exp/web-.._.._.._.._etc_cron.d_x__q___y-8a202bd8; " +
					"bash /tmp/miniccc/files/exp/web-.._.._.._.._etc_cron.d_x__q___y-8a202bd8",
			},
			wantState: "STDOUT contained ok",
		},
		{
			name: "host that leaves the folder", ns: "exp", host: "../../up", osType: "linux", testName: "t",
			wantFiles: []string{"images/exp/.._.._up-t-f5942ee6"},
			wantCmds:  []string{"exp/../../up: send exp/.._.._up-t-f5942ee6; bash /tmp/miniccc/files/exp/.._.._up-t-f5942ee6"},
			wantState: "STDOUT contained ok",
		},
		{
			name: "namespace with a space", ns: "my exp", host: "win", osType: "windows", testName: "check",
			wantFiles: []string{"images/my exp/win-check.ps1"},
			wantCmds: []string{
				"my exp/win: send 'my exp/win-check.ps1'; " + powershell + " '/tmp/miniccc/files/my exp/win-check.ps1'",
			},
			wantState: "STDOUT contained ok",
		},
		{
			name: "namespace that leaves the folder", ns: "..", host: "web", osType: "linux", testName: "t",
			wantState: "error: test script path",
		},
		{
			name: "namespace with a variable", ns: "$HOME", host: "web", osType: "linux", testName: "t",
			wantState: "error: " + errUnsafeC2Arg.Error(),
		},
	}

	for _, tt := range tests { //nolint:paralleltest // replaces package globals
		t.Run(tt.name, func(t *testing.T) {
			base := useSOHFilesDir(t)

			if err := os.MkdirAll(filepath.Join(base, "images", tt.ns), 0o750); err != nil {
				t.Fatalf("os.MkdirAll(%s) error = %v", tt.ns, err)
			}

			fake := useSOHMM(t, func(c2Call) (string, error) { return "ok", nil })

			wg := new(mm.StateGroup)
			test := customHostTest{Name: tt.testName, TestScript: "echo ok", TestStdout: "ok"}

			newSOH().customTest(t.Context(), wg, tt.ns, sohNode(tt.host, tt.osType), test)
			wg.Wait()

			if diff := cmp.Diff(tt.wantFiles, filesUnder(t, base)); diff != "" {
				t.Errorf("customTest(%q) files mismatch (-want +got):\n%s", tt.testName, diff)
			}

			if diff := cmp.Diff(tt.wantCmds, fake.commands(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("customTest(%q) C2 commands mismatch (-want +got):\n%s", tt.testName, diff)
			}

			if len(wg.States) != 1 {
				t.Fatalf("customTest(%q) gave %d results, want 1", tt.testName, len(wg.States))
			}

			got := wg.States[0].Msg
			if err := wg.States[0].Err; err != nil {
				got = "error: " + err.Error()
			}

			if !strings.HasPrefix(got, tt.wantState) {
				t.Errorf("customTest(%q) result = %q, want prefix %q", tt.testName, got, tt.wantState)
			}
		})
	}
}

// Two custom tests on one host whose names clean to the same text each write
// and send their own script, so one test does not run the script of the other.
func TestCustomTestsWithSimilarNamesUseTheirOwnScripts(t *testing.T) { //nolint:paralleltest // replaces package globals
	base := useSOHFilesDir(t)

	if err := os.MkdirAll(filepath.Join(base, "images", "exp"), 0o750); err != nil {
		t.Fatalf("os.MkdirAll(exp) error = %v", err)
	}

	fake := useSOHMM(t, func(c2Call) (string, error) { return "ok", nil })

	wg := new(mm.StateGroup)
	scripts := map[string]string{"db_check": "echo one", "db/check": "echo two", "db check": "echo three"}

	for _, name := range []string{"db_check", "db/check", "db check"} {
		test := customHostTest{Name: name, TestScript: scripts[name], TestStdout: "ok"}
		newSOH().customTest(t.Context(), wg, "exp", sohNode("web", "linux"), test)
	}

	wg.Wait()

	gotScripts := map[string]string{}

	for _, name := range []string{"db_check", "db/check", "db check"} {
		data, err := os.ReadFile(filepath.Join(base, "images", "exp", customScriptName("web", name)))
		if err != nil {
			t.Fatalf("os.ReadFile(script of %q) error = %v", name, err)
		}

		gotScripts[name] = string(data)
	}

	if diff := cmp.Diff(scripts, gotScripts); diff != "" {
		t.Errorf("customTest scripts mismatch (-want +got):\n%s", diff)
	}

	want := []string{
		"exp/web: send exp/web-db_check-006f25d3; bash /tmp/miniccc/files/exp/web-db_check-006f25d3",
		"exp/web: send exp/web-db_check-420ef7c9; bash /tmp/miniccc/files/exp/web-db_check-420ef7c9",
		"exp/web: send exp/web-db_check; bash /tmp/miniccc/files/exp/web-db_check",
	}
	if diff := cmp.Diff(want, fake.commands()); diff != "" {
		t.Errorf("customTest C2 commands mismatch (-want +got):\n%s", diff)
	}
}

// A custom test makes the namespace folder of its script when the folder does not exist yet.
func TestCustomTestMakesTheNamespaceFolder(t *testing.T) { //nolint:paralleltest // replaces package globals
	base := useSOHFilesDir(t)
	fake := useSOHMM(t, func(c2Call) (string, error) { return "ok", nil })

	wg := new(mm.StateGroup)
	test := customHostTest{Name: "t", TestScript: "echo ok", TestStdout: "ok"}

	newSOH().customTest(t.Context(), wg, "fresh", sohNode("web", "linux"), test)
	wg.Wait()

	if diff := cmp.Diff([]string{"images/fresh/web-t"}, filesUnder(t, base)); diff != "" {
		t.Errorf("customTest(t) files mismatch (-want +got):\n%s", diff)
	}

	if len(wg.States) != 1 || wg.States[0].Err != nil {
		t.Fatalf("customTest(t) results = %+v, want one result with no error", wg.States)
	}

	if got := len(fake.commands()); got != 1 {
		t.Errorf("customTest(t) C2 commands = %d, want 1", got)
	}
}
