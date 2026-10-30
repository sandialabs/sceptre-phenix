package disk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// cloneChunk is how much a clone copies between checks of its context and
// progress reports.
const cloneChunk = 4 << 20 // 4 MiB

// partialSuffixBytes is the random part of a clone's temporary file name.
const partialSuffixBytes = 8

// partialName matches the temporary files clones write: hidden, so the disk
// list leaves them out, and named after the image they become.
var partialName = regexp.MustCompile(`^\..+\.partial-[0-9a-f]{16}$`)

// cloneFile copies src to dst, a file it creates, through a hidden temporary file beside dst that becomes dst only once
// complete and synced, so an interrupted clone never leaves a whole-looking image (RemoveStalePartials deletes what a
// crash leaves). progress, if not nil, is told of each chunk copied. The copy is not sparse.
func cloneFile(ctx context.Context, src, dst string, progress func(n int64)) error {
	in, err := openRegular(src)
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("cloning %s: %w", src, err)
	}

	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, dst)
	}

	suffix := make([]byte, partialSuffixBytes)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("cloning %s: %w", src, err)
	}

	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".partial-"+hex.EncodeToString(suffix))

	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()) //nolint:gosec // G703 a sibling of dst
	if err != nil {
		return fmt.Errorf("cloning %s: %w", src, err)
	}

	err = copyChunks(ctx, out, in, progress)

	if err == nil {
		err = out.Sync()
	}

	if closeErr := out.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = renameNoReplace(tmp, dst)
		if errors.Is(err, fs.ErrExist) {
			err = fmt.Errorf("%w: %s", ErrExists, dst)
		}
	}

	if err != nil {
		_ = os.Remove(tmp)

		if errors.Is(err, ErrExists) || errors.Is(err, context.Canceled) {
			return err
		}

		return fmt.Errorf("cloning %s to %s: %w", src, dst, err)
	}

	return nil
}

// copyChunks copies in to out a chunk at a time until in ends, telling
// progress of each chunk, and stops with ctx's error once ctx is done.
func copyChunks(ctx context.Context, out io.Writer, in io.Reader, progress func(n int64)) error {
	buf := make([]byte, cloneChunk)

	for {
		if err := ctx.Err(); err != nil {
			return err //nolint:wrapcheck // the caller tells cancellation apart
		}

		n, readErr := io.ReadFull(in, buf)

		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return fmt.Errorf("writing: %w", err)
			}

			if progress != nil {
				progress(int64(n))
			}
		}

		switch {
		case readErr == nil:
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			return nil
		default:
			return fmt.Errorf("reading: %w", readErr)
		}
	}
}

// RemoveStalePartials deletes the temporary files of clones that never
// finished, as a crash or restart leaves, from the minimega files directory
// dir and its folders (as far down as the disk list looks), and returns how
// many it deleted. It keeps those changed since before, which clones still
// running may be writing.
func RemoveStalePartials(dir string, before time.Time) (int, error) {
	root, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("removing partial clones from %s: %w", dir, err)
	}

	var removed int

	err = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			// an unreadable folder holds nothing this can remove
			if p != root && entry != nil && entry.IsDir() {
				return fs.SkipDir
			}

			return err
		}

		if p == root {
			return nil
		}

		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		if entry.IsDir() {
			// the folders clones never write to (see Resolve)
			if skipped(rel) || tooDeep(dir, rel) {
				return fs.SkipDir
			}

			return nil
		}

		if !entry.Type().IsRegular() || !partialName.MatchString(entry.Name()) {
			return nil
		}

		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(before) {
			return nil //nolint:nilerr // a file gone or in use is not stale
		}

		if err := os.Remove(p); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		removed++

		return nil
	})
	if err != nil {
		return removed, fmt.Errorf("removing partial clones from %s: %w", dir, err)
	}

	return removed, nil
}
