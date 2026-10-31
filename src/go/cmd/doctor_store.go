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
	"phenix/types"
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

// doctorExperiment is what the doctor checks read of a stored experiment.
type doctorExperiment struct {
	name    string
	baseDir string // the experiment's base directory, from its spec
	running bool   // the store gives a start time
	vms     []doctorVM
}

// doctorVM is a VM that phenix launches for a stored experiment: not external, and not do-not-boot.
type doctorVM struct {
	name     string
	snapshot bool     // minimega runs the VM on a snapshot of its first drive, so it does not write to that image
	images   []string // the drive images, as the topology names them
}

// doctorExperiments is one read of the stored experiments, which a run shares.
type doctorExperiments struct {
	list []doctorExperiment
	why  string // why doctor read no experiments, or ""
}

// storedExperiments reads the stored experiments once in a run, by the rules of storedConfigs, and reports why it read
// none. A store file that is not there is a reason, because it can be another store than the one phenix ui uses. An
// experiment that does not decode keeps only its name.
func (in *doctorInput) storedExperiments(ctx context.Context) ([]doctorExperiment, string) {
	if in.found != nil && in.found.experiments != nil {
		return in.found.experiments.list, in.found.experiments.why
	}

	configs, missing, why := in.storedConfigs(ctx, "Experiment")
	if missing {
		path, _ := store.BoltPath(in.text("store.endpoint"))
		why = fmt.Sprintf("the store file %s does not exist or is empty", path)
	}

	read := &doctorExperiments{list: nil, why: why}

	for _, c := range configs {
		read.list = append(read.list, decodeDoctorExperiment(c))
	}

	if in.found != nil {
		in.found.experiments = read
	}

	return read.list, read.why
}

// decodeDoctorExperiment is what doctor reads of the stored experiment config c.
func decodeDoctorExperiment(c store.Config) doctorExperiment {
	exp := doctorExperiment{name: c.Metadata.Name, baseDir: "", running: false, vms: nil}

	decoded, err := types.DecodeExperimentFromConfig(c)
	if err != nil || decoded.Spec == nil {
		return exp
	}

	exp.baseDir = decoded.Spec.BaseDir()
	exp.running = decoded.Running()

	if decoded.Spec.Topology() == nil {
		return exp
	}

	for _, node := range decoded.Spec.Topology().Nodes() {
		if node == nil || node.External() || node.General() == nil || derefDoctorBool(node.General().DoNotBoot()) {
			continue
		}

		vm := doctorVM{name: node.General().Hostname(), snapshot: derefDoctorBool(node.General().Snapshot()), images: nil}

		if node.Hardware() != nil {
			for _, drive := range node.Hardware().Drives() {
				if drive.Image() != "" {
					vm.images = append(vm.images, drive.Image())
				}
			}
		}

		exp.vms = append(exp.vms, vm)
	}

	return exp
}

// derefDoctorBool is the value of b, or false for nil, as the minimega script template reads it.
func derefDoctorBool(b *bool) bool {
	return b != nil && *b
}

// experimentNamed returns the stored experiment name, if there is one.
func experimentNamed(experiments []doctorExperiment, name string) (doctorExperiment, bool) {
	for _, exp := range experiments {
		if exp.name == name {
			return exp, true
		}
	}

	return doctorExperiment{name: "", baseDir: "", running: false, vms: nil}, false
}
