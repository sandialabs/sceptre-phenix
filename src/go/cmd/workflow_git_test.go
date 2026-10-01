package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runGit runs git in dir and returns its output, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return string(out)
}

// gitCommitArgs returns the identity and safety options for a test commit,
// followed by extra. They keep every test commit off the user's real git
// configuration and off any hook.
func gitCommitArgs(extra ...string) []string {
	return append([]string{
		"-c", "user.name=test",
		"-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null",
	}, extra...)
}

// markerScript writes an executable script under the test's temporary
// directory whose only effect is to create marker, and returns its path. It
// stands in for the command a hostile git config would run; the trailing cat
// lets it double as a filter's clean command. It is never run outside the
// test.
func markerScript(t *testing.T, marker string) string {
	t.Helper()

	script := filepath.Join(t.TempDir(), "payload.sh")
	body := "#!/bin/sh\n: > \"" + marker + "\"\ncat\n"

	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("writing the payload script: %v", err)
	}

	return script
}

func TestGitCommitRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	plain := filepath.Join(root, "plain")
	repo := filepath.Join(root, "repo")

	writeWorkflowTree(t, plain, map[string]string{"a.txt": "a"})
	writeWorkflowTree(t, repo, map[string]string{"tracked.txt": "one\n"})

	if got := gitCommitRef(t.Context(), plain); got != "" {
		t.Errorf("gitCommitRef(not a repository) = %q, want empty", got)
	}

	empty := filepath.Join(root, "empty")
	writeWorkflowTree(t, empty, map[string]string{"a.txt": "a"})
	runGit(t, empty, "init", "-q")

	// An ordinary repository with no commit: git discovers it, but HEAD does
	// not resolve, so the commit tag is left out.
	if got := gitCommitRef(t.Context(), empty); got != "" {
		t.Errorf("gitCommitRef(repository with no commit) = %q, want empty", got)
	}

	runGit(t, repo, "init", "-q")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, gitCommitArgs("commit", "-q", "-m", "init")...)

	short := strings.TrimSpace(runGit(t, repo, "rev-parse", "--short", "HEAD"))

	if got := gitCommitRef(t.Context(), repo); got != short {
		t.Errorf("gitCommitRef(clean) = %q, want %q", got, short)
	}

	writeWorkflowTree(t, repo, map[string]string{"untracked.txt": "x"})

	if got := gitCommitRef(t.Context(), repo); got != short {
		t.Errorf("gitCommitRef(untracked file only) = %q, want %q", got, short)
	}

	writeWorkflowTree(t, repo, map[string]string{"tracked.txt": "two\n", "sub/b.txt": "b"})

	if got := gitCommitRef(t.Context(), filepath.Join(repo, "sub")); got != short+"-dirty" {
		t.Errorf("gitCommitRef(dirty, from a subdirectory) = %q, want %q", got, short+"-dirty")
	}

	tags := workflowTags(t.Context(), repo, "foo", applyTestNow())
	if want := "commit=" + short + "-dirty"; len(tags) != 5 || tags[4] != want {
		t.Errorf("workflowTags() = %q, want a final %q tag", tags, want)
	}
}

// TestGitCommitRefSaysWhyNoRepository checks that a directory in which git
// finds no repository leaves out the commit tag with a debug record that
// holds git's message.
func TestGitCommitRefSaysWhyNoRepository(t *testing.T) {
	skipWithoutCommands(t, "git")

	logs := captureLogs(t)
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	plain := filepath.Join(root, "plain")
	writeWorkflowTree(t, plain, map[string]string{"a.txt": "a"})

	if got := gitCommitRef(t.Context(), plain); got != "" {
		t.Errorf("gitCommitRef(not a repository) = %q, want empty", got)
	}

	record := logs.first(t, "leaving out the commit tag: git found no usable repository")
	checkFields(t, record, map[string]string{"level": "DEBUG", "type": "SYSTEM", "dir": plain, "err": "exit status 128"})

	if stderr := fmt.Sprint(record["stderr"]); !strings.HasPrefix(stderr, "fatal: not a git repository") {
		t.Errorf("stderr field = %q, want git's message", stderr)
	}

	if _, ok := record["hint"]; ok {
		t.Errorf("record = %v, want no hint", record)
	}
}

// TestGitCommitRefDubiousOwnership checks that a repository git refuses
// because another user owns it, the normal case when root runs phenix on a
// clone a host user owns, leaves out the commit tag with a warning that says
// what to do. git's own test switch makes it treat the repository as owned
// by someone else; the user's and the system's git configuration, which may
// trust every directory, are kept out. The first git releases with that
// check word the refusal differently, and a stand-in git on PATH prints that
// older wording.
func TestGitCommitRefDubiousOwnership(t *testing.T) {
	tests := []struct {
		name    string
		standIn bool
		wording string
	}{
		{name: "dubious ownership", wording: "dubious ownership"},
		{name: "unsafe repository", standIn: true, wording: "fatal: unsafe repository ("},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			root := t.TempDir()
			t.Setenv("GIT_CEILING_DIRECTORIES", root)

			repo := filepath.Join(root, "repo")

			if tt.standIn {
				skipWithoutCommands(t, "sh")
				writeWorkflowTree(t, repo, map[string]string{"tracked.txt": "one\n"})

				bin := filepath.Join(root, "bin")
				writeExecutable(t, filepath.Join(bin, "git"), "#!/bin/sh\nprintf '%s\\n' "+
					"\"fatal: unsafe repository ('"+repo+"' is owned by someone else)\" "+
					"'To add an exception for this directory, call:' '' "+
					"\"\tgit config --global --add safe.directory "+repo+"\" >&2\nexit 128\n")
				prependPath(t, bin)
			} else {
				skipWithoutCommands(t, "git")
				commitGitRepository(t, repo, map[string]string{"tracked.txt": "one\n"})

				globalConfig := filepath.Join(root, "gitconfig")
				writeWorkflowTree(t, root, map[string]string{"gitconfig": ""})
				t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
				t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
				t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
			}

			tags := workflowTags(t.Context(), repo, "foo", applyTestNow())

			want := []string{"method=workflow", "dir=" + repo, "branch=foo", "workflow_date=20260923120000UTC"}
			if !slices.Equal(tags, want) {
				t.Errorf("workflowTags() = %q, want %q without a commit", tags, want)
			}

			record := logs.first(t, "leaving out the commit tag: git found no usable repository")
			checkFields(t, record, map[string]string{
				"level": "WARN",
				"type":  "SYSTEM",
				"dir":   repo,
				"err":   "exit status 128",
				"hint":  "add the directory to git's safe.directory for the user that runs phenix",
			})

			stderr := fmt.Sprint(record["stderr"])
			if !strings.Contains(stderr, tt.wording) || !strings.Contains(stderr, "git config --global --add safe.directory ") {
				t.Errorf("stderr field = %q, want git's message with %q and its safe.directory line", stderr, tt.wording)
			}
		})
	}
}

// TestGitCommitRefLinkedWorkTree checks that git's own discovery, which
// gitCommitRef relies on, resolves a linked work tree (git worktree add) to
// that work tree's commit.
func TestGitCommitRefLinkedWorkTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	main := filepath.Join(root, "main")
	writeWorkflowTree(t, main, map[string]string{"tracked.txt": "one\n"})
	runGit(t, main, "init", "-q")
	runGit(t, main, "add", "tracked.txt")
	runGit(t, main, gitCommitArgs("commit", "-q", "-m", "init")...)

	linked := filepath.Join(root, "linked")
	runGit(t, main, "worktree", "add", "-q", linked)

	short := strings.TrimSpace(runGit(t, linked, "rev-parse", "--short", "HEAD"))

	if got := gitCommitRef(t.Context(), linked); got != short {
		t.Errorf("gitCommitRef(linked work tree) = %q, want %q", got, short)
	}
}

// TestGitCommitRefEmbeddedBareRepository checks that gitCommitRef runs no
// command from a git repository the topology directory itself provides. A
// normal clone can deliver a directory laid out as a bare repository whose
// config, in core.fsmonitor or a filter's clean command, would run as the
// invoking user during git status. gitCommitRef must return "" and run
// nothing.
func TestGitCommitRefEmbeddedBareRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	tests := []struct {
		name string
		arm  func(t *testing.T, gitDir, script string)
	}{
		{
			name: "fsmonitor",
			arm: func(t *testing.T, gitDir, script string) {
				t.Helper()
				runGit(t, gitDir, "config", "core.fsmonitor", script)
			},
		},
		{
			name: "filter clean",
			arm: func(t *testing.T, gitDir, script string) {
				t.Helper()
				runGit(t, gitDir, "config", "filter.evil.clean", script)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("GIT_CEILING_DIRECTORIES", root)
			marker := filepath.Join(root, "marker")

			// An ordinary outer repository, as a clone of the topologies repo.
			outer := filepath.Join(root, "outer")
			writeWorkflowTree(t, outer, map[string]string{"README.md": "outer\n"})
			runGit(t, outer, "init", "-q")
			runGit(t, outer, gitCommitArgs("commit", "-q", "--allow-empty", "-m", "init")...)

			// A topology directory inside its work tree, laid out as a bare
			// repository (its own HEAD, config, objects and refs), disguised
			// as a work tree with core.bare off and core.worktree set. It
			// holds a tracked file with a filter attribute and a commit, so
			// an unhardened rev-parse HEAD would resolve and git status would
			// run the hostile command.
			topo := filepath.Join(outer, "topo")
			runGit(t, outer, "init", "-q", "--bare", "topo")
			runGit(t, topo, "config", "core.bare", "false")
			runGit(t, topo, "config", "core.worktree", ".")
			writeWorkflowTree(t, topo, map[string]string{"data": "hello\n", ".gitattributes": "data filter=evil\n"})
			runGit(t, topo, "config", "filter.evil.clean", "cat")
			runGit(t, topo, "--work-tree=.", "add", "data", ".gitattributes")
			runGit(t, topo, gitCommitArgs("--work-tree=.", "commit", "-q", "-m", "seed")...)

			tt.arm(t, topo, markerScript(t, marker))

			if got := gitCommitRef(t.Context(), topo); got != "" {
				t.Errorf("gitCommitRef(embedded bare repository) = %q, want empty", got)
			}

			if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the embedded repository's git config ran: marker %s exists (err = %v)", marker, err)
			}
		})
	}
}

// TestGitFileTarget checks how a linked work tree's or submodule's .git file
// is resolved to its git directory.
func TestGitFileTarget(t *testing.T) {
	top := t.TempDir()

	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{name: "absolute path", content: "gitdir: /repo/.git/worktrees/w\n", want: "/repo/.git/worktrees/w"},
		{
			name:    "relative path is resolved against the work tree",
			content: "gitdir: ../.git/modules/m\n",
			want:    filepath.Join(top, "../.git/modules/m"),
		},
		{name: "not a gitdir file", content: "hello\n", wantErr: true},
		{name: "empty gitdir", content: "gitdir:\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gitFile := filepath.Join(t.TempDir(), ".git")
			if err := os.WriteFile(gitFile, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("writing gitfile: %v", err)
			}

			got, err := gitFileTarget(gitFile, top)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("gitFileTarget() error = nil, want an error")
				}

				return
			}

			if err != nil || got != tt.want {
				t.Fatalf("gitFileTarget() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}

	if _, err := gitFileTarget(filepath.Join(t.TempDir(), "gone"), top); err == nil {
		t.Error("gitFileTarget(missing file) error = nil, want an error")
	}
}

// TestSameGitDir checks the symlink-resolving directory comparison.
func TestSameGitDir(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	if !sameGitDir(dir, link) {
		t.Errorf("sameGitDir(%q, %q) = false, want true", dir, link)
	}

	other := t.TempDir()
	if sameGitDir(dir, other) {
		t.Errorf("sameGitDir(%q, %q) = true, want false", dir, other)
	}

	missing := filepath.Join(t.TempDir(), "gone")
	if sameGitDir(dir, missing) || sameGitDir(missing, dir) {
		t.Error("sameGitDir with an unresolvable path = true, want false")
	}
}

// TestGitCommitRefCanceledContext checks that gitCommitRef returns "" promptly
// when the run's context is already canceled, rather than running git.
func TestGitCommitRefCanceledContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	repo := filepath.Join(root, "repo")
	writeWorkflowTree(t, repo, map[string]string{"tracked.txt": "one\n"})
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "add", "tracked.txt")
	runGit(t, repo, gitCommitArgs("commit", "-q", "-m", "init")...)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Now()

	if got := gitCommitRef(ctx, repo); got != "" {
		t.Errorf("gitCommitRef(canceled context) = %q, want empty", got)
	}

	if elapsed := time.Since(start); elapsed >= gitCommandTimeout {
		t.Errorf("gitCommitRef(canceled context) took %v, want it to return before the %v timeout", elapsed, gitCommandTimeout)
	}
}

// skipWithoutCommands skips the test when any of the named commands is not on
// PATH.
func skipWithoutCommands(t *testing.T, names ...string) {
	t.Helper()

	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not installed", name)
		}
	}
}

// writeExecutable writes an executable script to path, creating its
// directory. Every script is written under the test's temporary directory.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}

	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// commitGitRepository creates an ordinary git repository in dir holding files,
// commits them, and returns the short commit of HEAD.
func commitGitRepository(t *testing.T, dir string, files map[string]string) string {
	t.Helper()

	writeWorkflowTree(t, dir, files)
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", ".")
	runGit(t, dir, gitCommitArgs("commit", "-q", "-m", "init")...)

	return strings.TrimSpace(runGit(t, dir, "rev-parse", "--short", "HEAD"))
}

// cleanFilterEverything makes every file of the ordinary repository in repo
// pass through the clean filter command, set in the repository's own config.
func cleanFilterEverything(t *testing.T, repo, command string) {
	t.Helper()

	writeWorkflowTree(t, filepath.Join(repo, ".git", "info"), map[string]string{"attributes": "* filter=payload\n"})
	runGit(t, repo, "config", "filter.payload.clean", command)
}

// ageFile moves the modification time of path an hour back without changing
// its content, so that git status reads the file again, through its clean
// filter, to learn whether it changed.
func ageFile(t *testing.T, path string) {
	t.Helper()

	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("changing the times of %s: %v", path, err)
	}
}

// prependPath puts dir first on PATH for the rest of the test.
func prependPath(t *testing.T, dir string) {
	t.Helper()

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestGitCommitRefHardeningOptions pins the options every git process of
// gitCommitRef gets, with a stand-in git on PATH that records its environment
// and arguments. The file-system monitor is turned off with the EMPTY value:
// git before 2.36 reads core.fsmonitor as the path of a hook, so the value
// false would run a program called false on every git status.
func TestGitCommitRefHardeningOptions(t *testing.T) {
	skipWithoutCommands(t, "sh")

	root := t.TempDir()
	record := filepath.Join(root, "record")
	bin := filepath.Join(root, "bin")

	// The stand-in fails, so gitCommitRef stops after its first git process.
	writeExecutable(t, filepath.Join(bin, "git"),
		"#!/bin/sh\nprintf '%s\\n' \"GIT_OPTIONAL_LOCKS=$GIT_OPTIONAL_LOCKS\" \"$@\" > \""+record+"\"\nexit 1\n")
	prependPath(t, bin)

	dir := filepath.Join(root, "topo")

	if got := gitCommitRef(t.Context(), dir); got != "" {
		t.Errorf("gitCommitRef(failing git) = %q, want empty", got)
	}

	body, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the stand-in git did not run: %v", err)
	}

	want := []string{
		"GIT_OPTIONAL_LOCKS=0",
		"-c", "core.fsmonitor=",
		"-c", "safe.bareRepository=explicit",
		"-C", dir,
		"rev-parse", "--absolute-git-dir", "--show-toplevel",
	}

	if got := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n"); !slices.Equal(got, want) {
		t.Errorf("git environment and arguments = %q, want %q", got, want)
	}
}

// TestGitCommitRefFileSystemMonitorOff checks that gitCommitRef runs no
// file-system monitor hook in an ordinary repository: neither the one the
// repository's own config names, nor a program called false found through
// PATH, which git before 2.36 runs as the hook when core.fsmonitor is false.
func TestGitCommitRefFileSystemMonitorOff(t *testing.T) {
	skipWithoutCommands(t, "git", "sh")

	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	hookMarker := filepath.Join(root, "hook-marker")
	falseMarker := filepath.Join(root, "false-marker")

	repo := filepath.Join(root, "repo")
	short := commitGitRepository(t, repo, map[string]string{"tracked.txt": "one\n"})
	runGit(t, repo, "config", "core.fsmonitor", markerScript(t, hookMarker))

	bin := filepath.Join(root, "bin")
	writeExecutable(t, filepath.Join(bin, "false"), "#!/bin/sh\n: > \""+falseMarker+"\"\nexit 1\n")
	prependPath(t, bin)

	if got := gitCommitRef(t.Context(), repo); got != short {
		t.Errorf("gitCommitRef(ordinary repository) = %q, want %q", got, short)
	}

	for _, marker := range []string{hookMarker, falseMarker} {
		if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("a file-system monitor hook ran: marker %s exists (err = %v)", marker, err)
		}
	}
}

// TestGitCommitRefForeignWorkTree reaches the .git entry comparison through
// git's normal discovery, which every git version performs: an ordinary
// repository whose own config points core.worktree at a directory whose .git
// entry is missing, or is another repository's. git reports that directory as
// the top level and the repository's own git directory as the git dir, so
// gitCommitRef must return "" and run no command from the repository's config.
// (git 2.38 and later refuse the embedded bare repository before this
// comparison, so TestGitCommitRefEmbeddedBareRepository pins it only on older
// git.)
func TestGitCommitRefForeignWorkTree(t *testing.T) {
	skipWithoutCommands(t, "git", "sh")

	layouts := []struct {
		name     string
		workTree func(t *testing.T, dir string)
	}{
		{
			name: "work tree without a .git entry",
			workTree: func(t *testing.T, dir string) {
				t.Helper()
				writeWorkflowTree(t, dir, map[string]string{"data": "hello\n"})
			},
		},
		{
			name: "work tree of another repository",
			workTree: func(t *testing.T, dir string) {
				t.Helper()
				commitGitRepository(t, dir, map[string]string{"data": "hello\n"})
			},
		},
	}

	payloads := []struct {
		name string
		arm  func(t *testing.T, repo, script string)
	}{
		{
			name: "fsmonitor",
			arm: func(t *testing.T, repo, script string) {
				t.Helper()
				runGit(t, repo, "config", "core.fsmonitor", script)
			},
		},
		{name: "filter clean", arm: cleanFilterEverything},
	}

	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			for _, payload := range payloads {
				t.Run(payload.name, func(t *testing.T) {
					root := t.TempDir()
					t.Setenv("GIT_CEILING_DIRECTORIES", root)
					marker := filepath.Join(root, "marker")

					workTree := filepath.Join(root, "worktree")
					layout.workTree(t, workTree)

					// An ordinary repository whose own config names that
					// directory as its work tree, with the file there committed.
					repo := filepath.Join(root, "repo")
					if err := os.Mkdir(repo, 0o750); err != nil {
						t.Fatalf("creating %s: %v", repo, err)
					}

					runGit(t, repo, "init", "-q")
					runGit(t, repo, "config", "core.worktree", workTree)
					runGit(t, repo, "add", "data")
					runGit(t, repo, gitCommitArgs("commit", "-q", "-m", "seed")...)

					payload.arm(t, repo, markerScript(t, marker))
					ageFile(t, filepath.Join(workTree, "data"))

					if got := gitCommitRef(t.Context(), repo); got != "" {
						t.Errorf("gitCommitRef(repository with a foreign work tree) = %q, want empty", got)
					}

					if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("the repository's git config ran: marker %s exists (err = %v)", marker, err)
					}
				})
			}
		})
	}
}

// TestGitCommitRefSymlinkedGitDir checks that a work tree whose .git entry is
// a symlink, which git itself follows, gets no commit tag: gitCommitRef uses a
// repository only when its .git is a directory or a gitdir file.
func TestGitCommitRefSymlinkedGitDir(t *testing.T) {
	skipWithoutCommands(t, "git")

	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	repo := filepath.Join(root, "repo")
	commitGitRepository(t, repo, map[string]string{"tracked.txt": "one\n"})

	moved := filepath.Join(root, "gitdir")
	if err := os.Rename(filepath.Join(repo, ".git"), moved); err != nil {
		t.Fatalf("moving the git directory: %v", err)
	}

	if err := os.Symlink(moved, filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("creating the .git symlink: %v", err)
	}

	if got := gitCommitRef(t.Context(), repo); got != "" {
		t.Errorf("gitCommitRef(work tree with a symlinked .git) = %q, want empty", got)
	}
}

// TestGitCommitRefUnusableAnswer checks, with a stand-in git on PATH that gives
// every command the same answer, that gitCommitRef returns "" when rev-parse
// does not name exactly a git directory and a top level, and when the top
// level's .git is a file that is not a gitdir file.
func TestGitCommitRefUnusableAnswer(t *testing.T) {
	skipWithoutCommands(t, "sh", "cat")

	root := t.TempDir()
	top := filepath.Join(root, "top")
	gitDir := filepath.Join(root, "gitdir")
	writeWorkflowTree(t, top, map[string]string{".git": "not a gitdir file\n"})

	tests := []struct {
		name   string
		answer string
	}{
		{name: "one line", answer: gitDir + "\n"},
		{name: "empty git directory", answer: "\n" + top + "\n"},
		{name: "three lines", answer: gitDir + "\n" + top + "\nextra\n"},
		{name: ".git file that is not a gitdir file", answer: gitDir + "\n" + top + "\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin := t.TempDir()
			answer := filepath.Join(bin, "answer")
			writeWorkflowTree(t, bin, map[string]string{"answer": tt.answer})
			writeExecutable(t, filepath.Join(bin, "git"), "#!/bin/sh\nexec cat \""+answer+"\"\n")
			prependPath(t, bin)

			if got := gitCommitRef(t.Context(), top); got != "" {
				t.Errorf("gitCommitRef(rev-parse answered %q) = %q, want empty", tt.answer, got)
			}
		})
	}
}

// killRecordedProcesses ends the processes whose IDs file lists, one per line.
func killRecordedProcesses(file string) {
	body, err := os.ReadFile(file)
	if err != nil {
		return
	}

	for field := range strings.FieldsSeq(string(body)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}

		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	}
}

// TestGitCommitRefBoundsTheWaitForGit checks that a process git started, which
// keeps git's output pipes open after git has been stopped, does not hold the
// run. An ordinary repository's own clean filter starts a child that holds the
// pipes and sleeps for a minute, so git status hangs; once the context is
// canceled, gitCommitRef must return within seconds, with the commit and
// without -dirty.
func TestGitCommitRefBoundsTheWaitForGit(t *testing.T) {
	skipWithoutCommands(t, "git", "sh", "sleep")

	const (
		cancelAfter = 200 * time.Millisecond
		returnBound = 5 * time.Second
		pollEvery   = 10 * time.Millisecond
	)

	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", root)

	repo := filepath.Join(root, "repo")
	short := commitGitRepository(t, repo, map[string]string{"data": "hello\n"})

	// The filter records its child's process ID, so that the test can end the
	// child instead of leaving it to sleep out its minute.
	pids := filepath.Join(root, "pids")
	filter := filepath.Join(root, "hang.sh")
	writeExecutable(t, filter, "#!/bin/sh\nsleep 60 &\necho $! >> \""+pids+"\"\ncat\n")
	t.Cleanup(func() { killRecordedProcesses(pids) })

	cleanFilterEverything(t, repo, filter)
	ageFile(t, filepath.Join(repo, "data"))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Cancel about 200 ms after the start, but not before the filter's child
	// holds the pipes, so that the cancel always stops a hanging git.
	done := make(chan struct{})

	go func() {
		defer cancel()

		deadline := time.After(cancelAfter)

		for started := false; ; {
			select {
			case <-done:
				return
			case <-deadline:
				deadline = nil
			case <-time.After(pollEvery):
			}

			if !started {
				_, err := os.Stat(pids)
				started = err == nil
			}

			if started && deadline == nil {
				return
			}
		}
	}()

	start := time.Now()
	got := gitCommitRef(ctx, repo)
	elapsed := time.Since(start)

	close(done)

	if _, err := os.Stat(pids); err != nil {
		t.Fatalf("the clean filter did not start its child, so git never hung: %v", err)
	}

	if got != short {
		t.Errorf("gitCommitRef(git status stopped) = %q, want %q", got, short)
	}

	if elapsed >= returnBound {
		t.Errorf("gitCommitRef returned after %v, want less than %v", elapsed, returnBound)
	}
}
