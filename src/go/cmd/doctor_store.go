package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"go.etcd.io/bbolt"

	"phenix/store"
)

const (
	// doctorStoreMode is the mode bbolt gets, which a read-only open never uses to create the file.
	doctorStoreMode = 0o600
	// doctorMinLockWait is the shortest wait for the store lock, because bbolt waits forever for a timeout of 0.
	doctorMinLockWait = time.Millisecond
)

// storedConfigs reads the stored configs of kind through in.storeConfigs, waiting at most the doctor timeout for the
// store lock. It reports missing when there is no store file, which holds no configs, and otherwise why it read
// nothing, or "".
func (in *doctorInput) storedConfigs(ctx context.Context, kind string) (store.Configs, bool, string) {
	path, err := store.BoltPath(in.text("store.endpoint"))
	if err != nil {
		return nil, false, "store.endpoint is not valid: " + err.Error()
	}

	info, err := in.env.stat(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, true, ""
	case err != nil:
		return nil, false, fmt.Sprintf("cannot read %s: %v", path, err)
	case info.Size() == 0:
		return nil, true, ""
	}

	wait := max(in.timeout, doctorMinLockWait)
	configs, err := in.storeConfigs(ctx, path, kind, wait)

	switch {
	case errors.Is(err, bbolt.ErrTimeout):
		return nil, false, fmt.Sprintf("another process held the lock of %s for %s", path, wait)
	case errors.Is(err, fs.ErrPermission):
		return nil, false, "this user cannot read " + path
	case err != nil:
		return nil, false, fmt.Sprintf("cannot read %s: %v", path, err)
	}

	return configs, false, ""
}

// readStoreConfigs reads the configs of kind from the BoltDB file at path. It opens the file read-only and waits at
// most timeout, and no longer than ctx, for the lock of a process that writes to it.
func readStoreConfigs(ctx context.Context, path, kind string, timeout time.Duration) (store.Configs, error) {
	db, err := openStoreReadOnly(ctx, path, timeout)
	if err != nil {
		return nil, err
	}

	defer func() { _ = db.Close() }()

	var configs store.Configs

	err = db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(kind))
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(_, v []byte) error {
			if err := ctx.Err(); err != nil {
				return err //nolint:wrapcheck // the context's own error
			}

			var c store.Config
			if err := json.Unmarshal(v, &c); err != nil {
				return fmt.Errorf("unmarshaling a %s config: %w", kind, err)
			}

			configs = append(configs, c)

			return nil
		})
	})

	return configs, err //nolint:wrapcheck // storedConfigs explains the error
}

// openStoreReadOnly opens the BoltDB file at path read-only. It waits at most timeout, and no longer than ctx, for the
// lock. When ctx ends first, the open continues in the background and closes the file when it gets the lock.
func openStoreReadOnly(ctx context.Context, path string, timeout time.Duration) (*bbolt.DB, error) {
	type opened struct {
		db  *bbolt.DB
		err error
	}

	opts := &bbolt.Options{ReadOnly: true, Timeout: max(timeout, doctorMinLockWait)} //nolint:exhaustruct // partial initialization
	result := make(chan opened, 1)

	go func() {
		db, err := bbolt.Open(path, doctorStoreMode, opts)
		result <- opened{db: db, err: err}
	}()

	select {
	case o := <-result:
		return o.db, o.err //nolint:wrapcheck // storedConfigs explains the error
	case <-ctx.Done():
		go func() {
			if o := <-result; o.err == nil {
				_ = o.db.Close()
			}
		}()

		return nil, ctx.Err() //nolint:wrapcheck // the context's own error
	}
}
