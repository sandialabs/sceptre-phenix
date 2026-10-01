package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"phenix/util/plog"
)

// gitCommandTimeout bounds the three git processes of gitCommitRef together
// (the repository check, rev-parse and status), so a slow or hanging git
// cannot block the run between the config upsert and the apply, and a
// canceled run ends git promptly.
const gitCommandTimeout = 10 * time.Second

// gitWaitDelay bounds how long the output of a git process is still read once
// git has exited or its context has ended. A process that git started, such as
// a filter, can hold git's output pipes open after git is gone, and without
// this bound the run would wait for that process to end.
const gitWaitDelay = time.Second

// gitCommitRef returns the short commit of HEAD for the git work tree holding
// dir, with a "-dirty" suffix when tracked files have uncommitted changes. It
// returns "" when git is not installed, when dir is not in an ordinary work
// tree, or when a git process fails, is canceled or times out.
//
// It never trusts a git repository that dir itself provides. A directory a
// clone delivered can be laid out as an embedded bare repository whose config
// would run a command (in core.fsmonitor, or a filter's clean command) as the
// invoking user during git status, so gitCommitRef first asks git only what
// it discovered, with a command that runs no hook, and uses the repository
// only when its work tree holds a real .git entry that is, or points at, the
// git directory git reported. Every git process runs under ctx with a
// timeout, with the file-system monitor and the optional index lock off, and
// with a bounded wait for its output after it ends.
func gitCommitRef(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()

	if !ordinaryGitRepository(ctx, dir) {
		return ""
	}

	out, err := runGitCommand(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		plog.Debug(plog.TypeSystem, "leaving out the commit tag: git rev-parse failed", "dir", dir, "err", err)

		return ""
	}

	ref := strings.TrimSpace(string(out))

	// A status that fails or times out leaves the commit without -dirty,
	// rather than dropping the commit tag.
	status, err := runGitCommand(ctx, dir, "status", "--porcelain", "--untracked-files=no")
	if err == nil && len(bytes.TrimSpace(status)) > 0 {
		ref += "-dirty"
	}

	return ref
}

// runGitCommand runs git in dir with the hardening options every git process
// of this command needs, and returns its standard output.
//
// It turns off the file-system monitor with the empty value of core.fsmonitor,
// which does so on git before and after 2.36: older versions read the setting
// as the path of a hook, so the value false would run a program called false.
// It refuses to treat a bare repository as an ordinary one (git 2.38 and later
// honor this; on older git, ordinaryGitRepository refuses an embedded one), so
// a config discovered in an untrusted directory runs no command. It turns off
// the optional index lock so git rewrites nothing, and it stops reading git's
// output [gitWaitDelay] after git ends or ctx is done, so that a process git
// started cannot hold the run.
func runGitCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	argv := append(
		[]string{"-c", "core.fsmonitor=", "-c", "safe.bareRepository=explicit", "-C", dir},
		args...,
	)

	cmd := exec.CommandContext(ctx, "git", argv...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = gitWaitDelay

	return cmd.Output()
}

// ordinaryGitRepository reports whether git discovers an ordinary repository
// from dir: one whose work tree holds a .git entry that is, or points at, the
// git directory git reported. It asks git with rev-parse, which runs no hook,
// no filter and no index refresh. An embedded bare repository laid out as
// tracked files cannot satisfy this, because a repository's tracked content
// can never hold an entry named .git.
func ordinaryGitRepository(ctx context.Context, dir string) bool {
	out, err := runGitCommand(ctx, dir, "rev-parse", "--absolute-git-dir", "--show-toplevel")
	if err != nil {
		logNoGitRepository(dir, err)

		return false
	}

	// rev-parse prints the git directory, then the work tree's top level.
	gitDir, topLevel, found := strings.Cut(strings.TrimRight(string(out), "\n"), "\n")
	if !found || gitDir == "" || topLevel == "" || strings.Contains(topLevel, "\n") {
		return false
	}

	dotGit := filepath.Join(topLevel, ".git")

	info, err := os.Lstat(dotGit)
	if err != nil {
		return false
	}

	switch {
	case info.IsDir():
		return sameGitDir(dotGit, gitDir)
	case info.Mode().IsRegular():
		target, err := gitFileTarget(dotGit, topLevel)
		if err != nil {
			return false
		}

		return sameGitDir(target, gitDir)
	default:
		return false
	}
}

// gitOwnershipHint ends the warning about a repository that git refuses
// because another user owns it: the normal case when root runs phenix, for
// example through docker exec, on a clone that a host user owns.
const gitOwnershipHint = "add the directory to git's safe.directory for the user that runs phenix"

// logNoGitRepository says why the commit tag is left out when git's
// repository check failed, with git's message. A repository that git
// refuses for its owner is a warning, with gitOwnershipHint; anything else,
// such as a directory outside any repository, is a debug record. git words
// that refusal as "dubious ownership", or as "unsafe repository" in its
// first releases with the check, and both wordings name safe.directory.
func logNoGitRepository(dir string, err error) {
	var exitErr *exec.ExitError

	message := ""
	if errors.As(err, &exitErr) {
		message = strings.TrimSpace(string(exitErr.Stderr))
	}

	const msg = "leaving out the commit tag: git found no usable repository"

	if strings.Contains(message, "dubious ownership") || strings.Contains(message, "safe.directory") {
		plog.Warn(plog.TypeSystem, msg, "dir", dir, "err", err, "stderr", message, "hint", gitOwnershipHint)

		return
	}

	plog.Debug(plog.TypeSystem, msg, "dir", dir, "err", err, "stderr", message)
}

// sameGitDir reports whether a and b name the same directory once symlinks are
// resolved, so that two spellings of one path (as with /tmp and its target)
// still match.
func sameGitDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}

	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}

	return ra == rb
}

// gitFileTarget returns the git directory a gitfile points at. A gitfile (a
// linked work tree's or a submodule's .git) holds one "gitdir: <path>" line,
// and a relative path is resolved against the work tree that holds it.
func gitFileTarget(gitFile, topLevel string) (string, error) {
	body, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}

	target, ok := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir:")
	if !ok {
		return "", fmt.Errorf("%s is not a gitdir file", gitFile)
	}

	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("%s has an empty gitdir", gitFile)
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(topLevel, target)
	}

	return target, nil
}
