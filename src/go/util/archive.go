package util

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func CreateArchive(root, path string) error {
	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating output file %s: %w", path, err)
	}

	defer out.Close()

	gw := gzip.NewWriter(out)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	fsys := os.DirFS(root)

	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		file, err := os.Open(filepath.Join(root, path))
		if err != nil {
			return fmt.Errorf("opening file %s: %w", path, err)
		}

		defer file.Close()

		info, err := file.Stat()
		if err != nil {
			return fmt.Errorf("getting file stats for %s: %w", path, err)
		}

		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return fmt.Errorf("creating archive file info header for %s: %w", path, err)
		}

		// ensure file name includes relevant directory structure
		header.Name = path

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("writing archive header for %s: %w", path, err)
		}

		if _, err := io.Copy(tw, file); err != nil {
			return fmt.Errorf("writing contents of %s to archive: %w", path, err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("walking directories rooted at %s: %w", root, err)
	}

	return nil
}
