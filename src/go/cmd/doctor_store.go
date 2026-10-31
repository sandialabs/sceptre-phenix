package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/mitchellh/mapstructure"
	"go.etcd.io/bbolt"

	"phenix/api/scorch/scorchmd"
	"phenix/app"
	"phenix/store"
	"phenix/types"
	ifaces "phenix/types/interfaces"
)

const (
	// doctorStoreMode is the mode bbolt gets, which a read-only open never uses to create the file.
	doctorStoreMode = 0o600
	// doctorMinLockWait is the shortest wait for the store lock, because bbolt waits forever for a timeout of 0.
	doctorMinLockWait = time.Millisecond
)

// doctorStored is one read of the stored configs of a kind, which a run shares.
type doctorStored struct {
	configs store.Configs
	missing bool
	why     string
}

// storedConfigs reads the stored configs of kind through in.storeConfigs, once in a run, waiting at most the doctor
// timeout for the store lock. It reports missing when there is no store file, which holds no configs, and otherwise
// why it read nothing, or "". After one read waits for the lock in vain, the reads of other kinds give its reason.
func (in *doctorInput) storedConfigs(ctx context.Context, kind string) (store.Configs, bool, string) {
	if in.found == nil {
		configs, missing, why, _ := in.readStored(ctx, kind)

		return configs, missing, why
	}

	if s, ok := in.found.stored[kind]; ok {
		return s.configs, s.missing, s.why
	}

	if in.found.storeBusy != "" {
		return nil, false, in.found.storeBusy
	}

	configs, missing, why, busy := in.readStored(ctx, kind)

	if in.found.stored == nil {
		in.found.stored = make(map[string]*doctorStored)
	}

	in.found.stored[kind] = &doctorStored{configs: configs, missing: missing, why: why}

	if busy {
		in.found.storeBusy = why
	}

	return configs, missing, why
}

// readStored reads the stored configs of kind for storedConfigs. It reports busy when the store lock or the context
// stopped the read.
func (in *doctorInput) readStored(ctx context.Context, kind string) (store.Configs, bool, string, bool) {
	path, err := store.BoltPath(in.text("store.endpoint"))
	if err != nil {
		return nil, false, "store.endpoint is not valid: " + err.Error(), false
	}

	info, err := in.env.stat(path)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, true, "", false
	case err != nil:
		return nil, false, fmt.Sprintf("cannot read %s: %v", path, err), false
	case info.Size() == 0:
		return nil, true, "", false
	}

	wait := max(in.timeout, doctorMinLockWait)
	configs, err := in.storeConfigs(ctx, path, kind, wait)

	switch {
	case errors.Is(err, bbolt.ErrTimeout):
		return nil, false, fmt.Sprintf("another process held the lock of %s for %s", path, wait), true
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return nil, false, fmt.Sprintf("cannot read %s: %v", path, err), true
	case errors.Is(err, fs.ErrPermission):
		return nil, false, "this user cannot read " + path, true
	case err != nil:
		return nil, false, fmt.Sprintf("cannot read %s: %v", path, err), false
	}

	return configs, false, "", false
}

// storedKind reads the stored configs of kind by the rules of storedConfigs, and gives a reason for a store file that
// is not there, as storedExperiments does.
func (in *doctorInput) storedKind(ctx context.Context, kind string) (store.Configs, string) {
	configs, missing, why := in.storedConfigs(ctx, kind)
	if missing {
		path, _ := store.BoltPath(in.text("store.endpoint"))
		why = fmt.Sprintf("the store file %s does not exist or is empty", path)
	}

	return configs, why
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

	vlans     doctorVLANs
	schedules map[string]string // the cluster node of each VM that the spec schedules

	nodes      []string    // the host name of each node of the topology
	apps       []string    // the apps of the scenario
	components []string    // the types of the SCORCH components of the scenario
	appHosts   [][2]string // each app of the scenario and a host it names

	stuck doctorStuck // what the stored status marks as in progress
}

// doctorVLANs is the VLAN part of a stored experiment's spec.
type doctorVLANs struct {
	min, max int            // the experiment's own VLAN range, or 0 and 0
	pinned   map[string]int // the VLAN ID of each alias that the spec fixes
	count    int            // the VLAN aliases of the experiment, from vlans.aliases and the node interfaces
}

// doctorVM is a VM that phenix launches for a stored experiment: not external, and not do-not-boot.
type doctorVM struct {
	name     string
	snapshot bool     // minimega runs the VM on a snapshot of its first drive, so it does not write to that image
	images   []string // the drive images, as the topology names them
	cc       string   // why phenix needs a miniccc client in the VM, or ""
	injects  []string // the sources of the files minimega injects into the snapshot, as the topology gives them
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

	configs, why := in.storedKind(ctx, "Experiment")
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
	exp := doctorExperiment{
		name: c.Metadata.Name, baseDir: "", running: false, vms: nil,
		vlans: doctorVLANs{min: 0, max: 0, pinned: nil, count: 0}, schedules: nil,
		nodes: nil, apps: nil, components: nil, appHosts: nil,
		stuck: doctorStuck{apps: nil, scorch: false, scorchRun: -1, pending: false, interrupted: false},
	}

	decoded, err := types.DecodeExperimentFromConfig(c)
	if err != nil || decoded.Spec == nil {
		return exp
	}

	exp.baseDir = decoded.Spec.BaseDir()
	exp.running = decoded.Running()
	exp.stuck = decodeDoctorStuck(decoded.Status)
	exp.apps, exp.components, exp.appHosts = doctorScenarioApps(decoded.Spec.Scenario())

	if schedules := decoded.Spec.Schedules(); len(schedules) > 0 {
		exp.schedules = maps.Clone(schedules)
	}

	aliases := make(map[string]bool)

	if vlans := decoded.Spec.VLANs(); vlans != nil {
		exp.vlans.min, exp.vlans.max = vlans.Min(), vlans.Max()

		for alias, id := range vlans.Aliases() {
			aliases[alias] = true

			if id != 0 {
				if exp.vlans.pinned == nil {
					exp.vlans.pinned = make(map[string]int)
				}

				exp.vlans.pinned[alias] = id
			}
		}
	}

	if decoded.Spec.Topology() == nil {
		exp.vlans.count = len(aliases)

		return exp
	}

	needCC := doctorCCNeeds(decoded.Spec)

	for _, node := range decoded.Spec.Topology().Nodes() {
		if node == nil {
			continue
		}

		if node.General() != nil {
			exp.nodes = append(exp.nodes, node.General().Hostname())
		}

		for _, iface := range node.Network().Interfaces() {
			if iface.VLAN() != "" {
				aliases[iface.VLAN()] = true
			}
		}

		if node.External() || node.General() == nil || derefDoctorBool(node.General().DoNotBoot()) {
			continue
		}

		vm := doctorVM{
			name: node.General().Hostname(), snapshot: derefDoctorBool(node.General().Snapshot()), images: nil,
			cc: needCC(node), injects: nil,
		}

		if node.Hardware() != nil {
			for _, drive := range node.Hardware().Drives() {
				if drive.Image() != "" {
					vm.images = append(vm.images, drive.Image())
				}
			}

			// as the minimega script template does: injects go into the snapshot of the first drive, partition 1 by default
			if drives := node.Hardware().Drives(); vm.snapshot && len(drives) > 0 &&
				(drives[0].InjectPartition() == nil || *drives[0].InjectPartition() != 0) {
				for _, inject := range node.Injections() {
					vm.injects = append(vm.injects, inject.Src())
				}
			}
		}

		exp.vms = append(exp.vms, vm)
	}

	exp.vlans.count = len(aliases)

	return exp
}

// doctorCCNeeds returns a function that says why phenix needs a miniccc client in a node of spec, or "": the startup
// app sends its scripts through miniccc, another VM waits for its client at the start, or the soh app checks it.
func doctorCCNeeds(spec ifaces.ExperimentSpec) func(ifaces.NodeSpec) string {
	waitedFor := make(map[string]string)

	for _, node := range spec.Topology().Nodes() {
		if node == nil || node.General() == nil || node.Delay() == nil {
			continue
		}

		for _, c2 := range node.Delay().C2() {
			if _, ok := waitedFor[c2.Hostname()]; !ok {
				waitedFor[c2.Hostname()] = node.General().Hostname()
			}
		}
	}

	var (
		soh       bool
		sohSkips  []string
		scenarios = spec.Scenario()
	)

	if scenarios != nil {
		for _, a := range scenarios.Apps() {
			if a == nil || a.Name() != "soh" {
				continue
			}

			soh = true

			if skips, ok := a.Metadata()["skipHosts"].([]any); ok {
				for _, s := range skips {
					sohSkips = append(sohSkips, fmt.Sprint(s))
				}
			}
		}
	}

	return func(node ifaces.NodeSpec) string {
		name := node.General().Hostname()

		switch {
		case app.StartupUsesC2(node):
			return "the startup app sends its scripts through miniccc"
		case waitedFor[name] != "":
			return fmt.Sprintf("the start of %s waits for its miniccc client", waitedFor[name])
		case soh && !sohSkipsNode(node, sohSkips):
			return "the soh app checks it through miniccc"
		}

		return ""
	}
}

// sohSkipsNode reports whether the soh app skips node, by its skipHosts: a host name, or the base name of the image of
// the first drive.
func sohSkipsNode(node ifaces.NodeSpec, skips []string) bool {
	var image string
	if node.Hardware() != nil && len(node.Hardware().Drives()) > 0 {
		image = filepath.Base(node.Hardware().Drives()[0].Image())
	}

	return slices.ContainsFunc(skips, func(skip string) bool {
		ext := filepath.Ext(skip)

		return skip == node.General().Hostname() || (ext == ".qc2" || ext == ".qcow" || ext == ".qcow2") && skip == image
	})
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

	return doctorExperiment{
		name: "", baseDir: "", running: false, vms: nil, vlans: doctorVLANs{min: 0, max: 0, pinned: nil, count: 0},
		schedules: nil, nodes: nil, apps: nil, components: nil, appHosts: nil,
		stuck: doctorStuck{apps: nil, scorch: false, scorchRun: -1, pending: false, interrupted: false},
	}, false
}

// decodeDoctorStuck is what status marks as in progress: the app running flags, which phenix sets while an app stage
// or a SCORCH run is in progress, the delayed starts that phenix ui resumes, and an interrupted start.
func decodeDoctorStuck(status ifaces.ExperimentStatus) doctorStuck {
	stuck := doctorStuck{apps: nil, scorch: false, scorchRun: -1, pending: false, interrupted: false}
	if status == nil {
		return stuck
	}

	for _, name := range slices.Sorted(maps.Keys(status.AppRunning())) {
		switch {
		case !status.AppRunning()[name]:
		case name == "scorch":
			stuck.scorch = true

			var scorch scorchmd.ScorchStatus
			if err := status.ParseAppStatus(name, &scorch); err == nil {
				stuck.scorchRun = scorch.RunID
			}
		default:
			stuck.apps = append(stuck.apps, name)
		}
	}

	stuck.pending = len(status.DelayedTimers()) > 0 || len(status.DelayedC2()) > 0 || status.PostStartPending()
	stuck.interrupted = status.Interrupted() != ""

	return stuck
}

// doctorScenarioApps returns the apps of scenario, the types of its SCORCH components, and each app with a host it
// names. SCORCH uses the name of a component as its type when it gives none.
func doctorScenarioApps(scenario ifaces.ScenarioSpec) ([]string, []string, [][2]string) {
	if scenario == nil {
		return nil, nil, nil
	}

	var (
		apps, components []string
		hosts            [][2]string
	)

	for _, a := range scenario.Apps() {
		if a == nil {
			continue
		}

		apps = append(apps, a.Name())

		for _, h := range a.Hosts() {
			if h != nil {
				hosts = append(hosts, [2]string{a.Name(), h.Hostname()})
			}
		}

		if a.Name() != "scorch" {
			continue
		}

		var md scorchmd.ScorchMetadata
		if err := mapstructure.Decode(a.Metadata(), &md); err != nil {
			continue
		}

		for _, c := range md.Components {
			typ := c.Type
			if typ == "" {
				typ = c.Name
			}

			if typ != "" {
				components = append(components, typ)
			}
		}
	}

	return apps, components, hosts
}
