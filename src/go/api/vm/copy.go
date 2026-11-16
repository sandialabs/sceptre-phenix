package vm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/plog"
)

// minicccFiles is the folder in the VM where miniccc puts the files that
// `cc send` sends, below their path relative to the minimega files directory.
const minicccFiles = "/tmp/miniccc/files/"

var (
	// ErrGuestPath is wrapped by the error of [ValidateGuestPath],
	// [CopyToVM] and [CopyFromVM] for a path in the VM that they refuse.
	ErrGuestPath = errors.New("invalid path in the VM")

	// ErrRemoteVM is wrapped by the error of [CopyFromVM] for a VM that does
	// not run on the headnode, where the received file is.
	ErrRemoteVM = errors.New("copying a file out of a VM works only for a VM on the headnode")

	// ErrCopyFailed is wrapped by the error of [CopyToVM] and [CopyFromVM]
	// when the VM did not complete the copy.
	ErrCopyFailed = errors.New("the VM did not complete the copy")
)

// windowsAbs matches the start of an absolute Windows path, such as C:\ or C:/.
var windowsAbs = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// ValidateGuestPath returns an error wrapping [ErrGuestPath] unless p is an
// absolute path in a VM that minimega takes unchanged as one word. A path to
// receive (recv true) must also be a Unix path with no "." or ".." part and
// no glob character, because miniccc globs it and sends it relative to "/".
func ValidateGuestPath(p string, recv bool) error {
	if !strings.HasPrefix(p, "/") && (recv || !windowsAbs.MatchString(p)) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrGuestPath, p)
	}

	if _, err := mm.CCExecCommand(p); err != nil {
		return fmt.Errorf("%w: %w", ErrGuestPath, err)
	}

	if !recv {
		return nil
	}

	if strings.ContainsAny(p, `*?[\`) {
		return fmt.Errorf("%w: %q contains a glob character", ErrGuestPath, p)
	}

	for part := range strings.SplitSeq(p, "/") {
		if part == "." || part == ".." {
			return fmt.Errorf("%w: %q contains a %q part", ErrGuestPath, p, part)
		}
	}

	if path.Clean(p) == "/" {
		return fmt.Errorf("%w: %q is not a file", ErrGuestPath, p)
	}

	return nil
}

// CopyToVM copies the local file src to dst, an absolute path in the running
// VM, and returns the path in the VM. A dst that ends with a separator is a
// folder. It puts a staging copy in the experiment's folder of the minimega
// files directory, sends it with `cc send`, moves it to dst with `cc exec`, and
// removes the staging copy.
func CopyToVM(ctx context.Context, expName, vmName, src, dst string, timeout time.Duration) (string, error) {
	if err := ValidateGuestPath(dst, false); err != nil {
		return "", err
	}

	if strings.HasSuffix(dst, "/") || strings.HasSuffix(dst, `\`) {
		dst += filepath.Base(src)
	}

	in, err := openRegular(src)
	if err != nil {
		return "", err
	}

	defer in.Close()

	target, err := activeC2VM(ctx, expName, vmName)
	if err != nil {
		return "", err
	}

	staged, err := stageFile(expName, in)
	if err != nil {
		return "", err
	}

	defer target.removeStaged(staged)

	move, err := moveCommand(target.osType, minicccFiles+staged, dst)
	if err != nil {
		return "", err
	}

	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}

	id, err := mm.ExecC2Command(
		target.c2Options(ctx, mm.C2SendFile(staged), mm.C2Command(move), mm.C2Wait(), mm.C2Timeout(timeout))...,
	)
	if err != nil {
		return "", fmt.Errorf("copying %s to VM %s: %w", src, vmName, err)
	}

	result, err := target.result(ctx, id)
	if err != nil {
		return "", err
	}

	if (result.ExitCodeKnown && result.ExitCode != 0) || (!result.ExitCodeKnown && result.Stderr != "") {
		return "", fmt.Errorf("%w: moving the file to %s: %s", ErrCopyFailed, dst, strings.TrimSpace(result.Stderr))
	}

	plog.Info(plog.TypeAction, "file copied to vm", "exp", expName, "vm", vmName, "src", src, "dst", dst)

	return dst, nil
}

// openRegular opens the local file p, which must be a regular file.
func openRegular(p string) (*os.File, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", p, err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("reading %s: %w", p, err)
	}

	if !info.Mode().IsRegular() {
		_ = f.Close()

		return nil, fmt.Errorf("%s is not a regular file", p)
	}

	return f, nil
}

// stageFile copies in to a new file with a random name in the experiment's
// folder of the minimega files directory, and returns its path relative to
// that directory.
func stageFile(expName string, in io.Reader) (string, error) {
	random := make([]byte, 8) //nolint:mnd // 64 random bits name the staging copy
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("naming the staging copy: %w", err)
	}

	rel := expName + "/phenix-cp-" + hex.EncodeToString(random)
	full := mm.GetMMFullPath(rel)

	if err := mm.ValidateArg(rel); err != nil || !file.WithinDir(mm.GetMMFullPath(""), full) {
		return "", fmt.Errorf("staging copy %s is not in the minimega files directory", full)
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", fmt.Errorf("making the staging folder: %w", err)
	}

	out, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("making the staging copy: %w", err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(full)

		return "", fmt.Errorf("writing the staging copy: %w", err)
	}

	if err := out.Close(); err != nil {
		_ = os.Remove(full)

		return "", fmt.Errorf("writing the staging copy: %w", err)
	}

	return rel, nil
}

// removeStaged removes the staging copy rel from the headnode and, for a VM
// on another host, from the hosts minimega copied it to.
func (t c2VM) removeStaged(rel string) {
	if err := os.Remove(mm.GetMMFullPath(rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
		plog.Warn(plog.TypeSystem, "removing the staging copy", "path", rel, "err", err)
	}

	if t.host == "" || mm.IsHeadnode(t.host) {
		return
	}

	if err := file.DeleteFile(rel); err != nil {
		plog.Warn(plog.TypeSystem, "removing the staging copy from the cluster", "path", rel, "err", err)
	}
}

// moveCommand returns the `cc exec` text that moves the file from to the path
// to in a VM of the OS type.
func moveCommand(osType, from, to string) (string, error) {
	var (
		text string
		err  error
	)

	if osType == "windows" {
		text, err = mm.CCExecCommand(
			"powershell", "-NoProfile", "-NonInteractive", "-Command",
			"Move-Item -LiteralPath "+mm.PowerShellQuote(from)+" -Destination "+mm.PowerShellQuote(to)+" -Force",
		)
	} else {
		text, err = mm.CCExecCommand("mv", "-f", "--", from, to)
	}

	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrGuestPath, err)
	}

	return text, nil
}

// CopyFromVM copies the file src, an absolute path in the running VM, to the
// local path dst through the VM's miniccc agent, with `cc recv`. A dst that is
// a folder, or that ends with a separator, gets the file with its name. The VM
// must run on the headnode. phenix removes the response folder that holds the
// received copy. It returns the local path of the file.
func CopyFromVM(ctx context.Context, expName, vmName, src, dst string, timeout time.Duration) (string, error) {
	if err := ValidateGuestPath(src, true); err != nil {
		return "", err
	}

	if info, err := os.Stat(dst); (err == nil && info.IsDir()) || strings.HasSuffix(dst, string(filepath.Separator)) {
		dst = filepath.Join(dst, path.Base(src))
	}

	target, err := activeC2VM(ctx, expName, vmName)
	if err != nil {
		return "", err
	}

	if !mm.IsHeadnode(target.host) {
		return "", fmt.Errorf("%w: VM %s runs on %s", ErrRemoteVM, vmName, target.host)
	}

	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}

	word, err := mm.CCExecCommand(src)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrGuestPath, err)
	}

	id, err := mm.ExecC2Command(target.c2Options(ctx, mm.C2RecvFile(word), mm.C2Wait(), mm.C2Timeout(timeout))...)
	if err != nil {
		return "", fmt.Errorf("copying %s from VM %s: %w", src, vmName, err)
	}

	responses, err := mm.C2ResponsePath(expName, id, "")
	if err != nil {
		return "", err
	}

	defer func() {
		if err := os.RemoveAll(responses); err != nil {
			plog.Warn(plog.TypeSystem, "removing the received copy", "path", responses, "err", err)
		}
	}()

	dir, err := mm.C2ResponsePath(expName, id, target.uuid)
	if err != nil {
		return "", err
	}

	received := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path.Clean(src), "/")))
	if !file.WithinDir(dir, received) {
		return "", fmt.Errorf("%w: %q leaves the response folder", ErrGuestPath, src)
	}

	in, err := openRegular(received)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: the VM sent no file %s: it does not exist, or miniccc cannot read it", ErrCopyFailed, src)
		}

		return "", err
	}

	defer in.Close()

	if err := writeFile(dst, in); err != nil {
		return "", err
	}

	plog.Info(plog.TypeAction, "file copied from vm", "exp", expName, "vm", vmName, "src", src, "dst", dst)

	return dst, nil
}

// writeFile writes the content of in to the local file p, which it makes or
// replaces.
func writeFile(p string, in io.Reader) error {
	out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("making %s: %w", p, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()

		return fmt.Errorf("writing %s: %w", p, err)
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", p, err)
	}

	return nil
}
