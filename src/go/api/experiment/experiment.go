package experiment

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"phenix/api/config"
	"phenix/app"
	"phenix/scheduler"
	"phenix/store"
	"phenix/tmpl"
	"phenix/types"
	"phenix/types/version"
	v1 "phenix/types/version/v1"
	"phenix/util/common"
	"phenix/util/file"
	"phenix/util/mm"
	"phenix/util/mm/mmcli"
	"phenix/util/notes"
	"phenix/util/plog"
	"phenix/util/pubsub"

	"github.com/activeshadow/structs"
	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/mapstructure"
)

func init() {
	config.RegisterConfigHook("Experiment", func(stage string, c *store.Config) error {
		exp, err := types.DecodeExperimentFromConfig(*c)
		if err != nil {
			return fmt.Errorf("decoding experiment from config: %w", err)
		}

		switch stage {
		case "create":
			exp.Spec.SetExperimentName(c.Metadata.Name)

			if err := exp.Spec.Init(); err != nil {
				return fmt.Errorf("initializing experiment: %w", err)
			}

			if err := exp.Spec.VerifyScenario(context.TODO()); err != nil {
				return fmt.Errorf("verifying experiment scenario: %w", err)
			}

			if err := app.ApplyApps(context.TODO(), exp, app.Stage(app.ACTIONCONFIG)); err != nil {
				return fmt.Errorf("applying apps to experiment: %w", err)
			}

			c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
		case "update":
			if exp.Running() {
				// Halt this update if the experiment is running.
				return fmt.Errorf("cannot update running experiment")
			}

			if err := exp.Spec.Init(); err != nil {
				return fmt.Errorf("re-initializing experiment (after update): %w", err)
			}

			if exp.Spec.ExperimentName() != c.Metadata.Name {
				if strings.Contains(exp.Spec.BaseDir(), exp.Spec.ExperimentName()) {
					// If the experiment's base directory contains the current experiment
					// name, replace it with the new name.
					dir := strings.ReplaceAll(exp.Spec.BaseDir(), exp.Spec.ExperimentName(), c.Metadata.Name)
					exp.Spec.SetBaseDir(dir)
				}

				exp.Spec.SetExperimentName(c.Metadata.Name)
			}

			c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
		case "delete":
			var errors error

			// Delete any snapshot files created by this headnode for this experiment
			// after deleting the experiment.
			if err := deleteC2AndSnapshots(exp); err != nil {
				errors = multierror.Append(errors, fmt.Errorf("deleting experiment snapshots and CC responses: %w", err))
			}

			if err := os.RemoveAll(exp.Spec.BaseDir()); err != nil {
				errors = multierror.Append(errors, fmt.Errorf("deleting experiment base directory: %w", err))
			}

			return errors
		}

		return nil
	})
}

// Hook is a function to be called during the different lifecycle stages of an
// experiment. The first argument is the experiment stage (create, start, stop,
// delete), and the second argument is the experiment, name.
type Hook func(string, string)

var hooks = make(map[string][]Hook)

// RegisterHook registers a Hook for the given experiment stage.
func RegisterHook(stage string, hook Hook) {
	hooks[stage] = append(hooks[stage], hook)
}

// List collects experiments, each in a struct that references the latest
// versioned experiment spec and status. It returns a slice of experiments and
// any errors encountered while gathering and decoding them.
func List() ([]types.Experiment, error) {
	configs, err := store.List("Experiment")
	if err != nil {
		return nil, fmt.Errorf("getting list of experiment configs from store: %w", err)
	}

	var (
		experiments []types.Experiment
		errs        error
	)

	for _, c := range configs {
		exp, err := types.DecodeExperimentFromConfig(c)
		if err != nil {
			errs = multierror.Append(err, fmt.Errorf("decoding experiment %s from config: %w", c.Metadata.Name, err))
			continue
		}

		experiments = append(experiments, *exp)
	}

	return experiments, errs
}

// Get retrieves the experiment with the given name. It returns a pointer to a
// struct that references the latest versioned experiment spec and status for
// the given experiment, and any errors encountered while retrieving the
// experiment.
func Get(name string) (*types.Experiment, error) {
	if name == "" {
		return nil, fmt.Errorf("no experiment name provided")
	}

	c, err := store.NewConfig("experiment/" + name)
	if err != nil {
		return nil, fmt.Errorf("getting experiment: %w", err)
	}

	if err := store.Get(c); err != nil {
		return nil, fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return nil, fmt.Errorf("decoding experiment from config: %w", err)
	}

	return exp, nil
}

// Create uses the provided arguments to create a new experiment. The
// `scenarioName` argument can be an empty string, in which case no scenario is
// used for the experiment. The `baseDir` argument can be an empty string, in
// which case the default value of `/phenix/experiments/{name}` is used for the
// experiment base directory. It returns any errors encountered while creating
// the experiment.
func Create(ctx context.Context, opts ...CreateOption) error {
	o := newCreateOptions(opts...)

	if o.name == "" {
		return fmt.Errorf("no experiment name provided")
	}

	if strings.ToLower(o.name) == "all" {
		return fmt.Errorf("cannot use 'all' for experiment name")
	}

	if o.topology == "" {
		return fmt.Errorf("no topology name provided")
	}

	var (
		kind       = "Experiment"
		apiVersion = version.StoredVersion[kind]
	)

	topoC, _ := store.NewConfig("topology/" + o.topology)

	if err := store.Get(topoC); err != nil {
		return fmt.Errorf("topology doesn't exist")
	}

	// This will upgrade the toplogy to the latest known version if needed.
	topo, err := types.DecodeTopologyFromConfig(*topoC)
	if err != nil {
		return fmt.Errorf("decoding topology from config: %w", err)
	}

	meta := store.ConfigMetadata{
		Name: o.name,
		Annotations: map[string]string{
			"topology": o.topology,
		},
	}

	specMap := map[string]interface{}{
		"experimentName": o.name,
		"baseDir":        o.baseDir,
		"topology":       topo,
	}

	if o.disabledApps != nil {
		plog.Info(fmt.Sprintf("Got disabled applications: %v", o.disabledApps))
	}

	if o.scenario != "" {
		scenarioC, _ := store.NewConfig("scenario/" + o.scenario)

		if err := store.Get(scenarioC); err != nil {
			return fmt.Errorf("scenario doesn't exist")
		}

		topo, ok := scenarioC.Metadata.Annotations["topology"]
		if !ok {
			return fmt.Errorf("topology annotation missing from scenario")
		}

		if !strings.Contains(topo, o.topology) {
			return fmt.Errorf("experiment/scenario topology mismatch for scenario %s", o.scenario)
		}

		// This will upgrade the scenario to the latest known version if needed.
		plog.Info("Creating custom scenario")
		scenario, err := types.MakeCustomScenarioFromConfig(*scenarioC, o.disabledApps)
		if err != nil {
			return fmt.Errorf("decoding scenario from config: %w", err)
		}

		if err := types.MergeScenariosForTopology(scenario, o.topology); err != nil {
			return fmt.Errorf("merging scenerios: %w", err)
		}

		meta.Annotations["scenario"] = o.scenario
		specMap["scenario"] = scenario
	}

	for k, v := range o.annotations {
		if _, ok := meta.Annotations[k]; !ok {
			meta.Annotations[k] = v
		}
	}

	c := &store.Config{
		Version:  store.API_GROUP + "/" + apiVersion,
		Kind:     kind,
		Metadata: meta,
		Spec:     specMap,
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	exp.Spec.SetVLANRange(o.vlanMin, o.vlanMax, false)
	exp.Spec.VLANs().SetAliases(o.vlanAliases)
	exp.Spec.SetSchedule(o.schedules)

	if _, err := config.Create(config.CreateFromConfig(c), config.CreateWithValidation()); err != nil {
		return fmt.Errorf("creating experiment config: %w", err)
	}

	for _, hook := range hooks["create"] {
		hook("create", o.name)
	}

	return nil
}

// Schedule applies the given scheduling algorithm to the experiment with the
// given name. It returns any errors encountered while scheduling the
// experiment.
func Schedule(opts ...ScheduleOption) error {
	o := newScheduleOptions(opts...)

	c, _ := store.NewConfig("experiment/" + o.name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", o.name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	if exp.Running() {
		return fmt.Errorf("experiment already running (started at: %s)", exp.Status.StartTime())
	}

	if err := scheduler.Schedule(o.algorithm, exp.Spec); err != nil {
		return fmt.Errorf("running scheduler algorithm: %w", err)
	}

	c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		return fmt.Errorf("updating experiment config: %w", err)
	}

	return nil
}

// Start starts the experiment with the given name. It returns any errors
// encountered while starting the experiment.
func Start(ctx context.Context, opts ...StartOption) error {
	o := newStartOptions(opts...)

	c, _ := store.NewConfig("experiment/" + o.name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", o.name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	if exp.Running() {
		if !strings.HasSuffix(exp.Status.StartTime(), "-DRYRUN") {
			return fmt.Errorf("experiment already running (started at: %s)", exp.Status.StartTime())
		}
	}

	if o.vlanMin != 0 {
		exp.Spec.VLANs().SetMin(o.vlanMin)
	}

	if o.vlanMax != 0 {
		exp.Spec.VLANs().SetMax(o.vlanMax)
	}

	if err := app.ApplyApps(ctx, exp, app.Stage(app.ACTIONPRESTART), app.DryRun(o.dryrun)); err != nil {
		return fmt.Errorf("applying apps to experiment: %w", err)
	}

	var (
		mmScript = fmt.Sprintf("%s/mm_files/%s.mm", exp.Spec.BaseDir(), exp.Spec.ExperimentName())
		ccScript = fmt.Sprintf("%s/mm_files/%s-cc.mm", exp.Spec.BaseDir(), exp.Spec.ExperimentName())
	)

	if err := tmpl.CreateFileFromTemplate("minimega_script.tmpl", exp.Spec, mmScript); err != nil {
		return fmt.Errorf("generating minimega script: %w", err)
	}

	if exp.Spec.Topology().HasCommands() {
		if err := tmpl.CreateFileFromTemplate("minimega_cc_script.tmpl", exp.Spec.Topology().Nodes(), ccScript); err != nil {
			return fmt.Errorf("generating minimega cc script: %w", err)
		}
	}

	var (
		delays = make(map[string]time.Duration)
		c2s    = make(map[string]map[string]bool)
	)

	if o.dryrun {
		exp.Status.SetVLANs(exp.Spec.VLANs().Aliases())
	} else {
		// Delete any snapshot files created by this headnode for this experiment
		// previously before starting the experiment. This way, cluster nodes that VMs
		// get scheduled on will pull the most up-to-date snapshot files. We don't do
		// this after stopping an experiment just in case users need to access the
		// snapshots for any reason, but we do clean them up when an experiment is
		// deleted.
		if err := deleteC2AndSnapshots(exp); err != nil {
			return fmt.Errorf("deleting experiment snapshots and CC responses: %w", err)
		}

		if err := mm.ReadScriptFromFile(mmScript); err != nil {
			if !o.mmErrAsWarn {
				mm.ClearNamespace(exp.Spec.ExperimentName())
				return fmt.Errorf("reading minimega script: %w", err)
			}

			if merr, ok := err.(*multierror.Error); ok {
				notes.AddWarnings(ctx, false, merr.Errors...)
			} else {
				notes.AddWarnings(ctx, false, err)
			}
		}

		var (
			bootable = exp.Spec.Topology().BootableNodes()
			start    = make([]string, 0) // nil vs. slice makes a difference here
		)

		for _, node := range bootable {
			if node.External() {
				continue
			}

			hostname := node.General().Hostname()

			if node.Delay().User() {
				notes.AddInfo(ctx, true, fmt.Sprintf("VM %s delayed - to be started by user", hostname))
				continue
			}

			if others := node.Delay().C2(); len(others) > 0 {
				var (
					c2    = make(map[string]bool)
					hosts = make([]string, len(others))
				)

				for i, other := range others {
					if other.Hostname() == "" {
						continue
					}

					c2[other.Hostname()] = other.UseUUID()
					hosts[i] = other.Hostname()
				}

				if len(c2) > 0 {
					c2s[hostname] = c2
					notes.AddInfo(ctx, true, fmt.Sprintf("VM %s delayed - will be started after C2 for %v is active", hostname, hosts))

					continue
				}
			}

			if d := node.Delay().Timer(); d != 0 {
				delays[hostname] = d

				notes.AddInfo(ctx, true, fmt.Sprintf("VM %s delayed - will be started after %v", hostname, d))

				continue
			}

			start = append(start, hostname)
		}

		if len(start) == len(bootable) {
			// Reset start slice so the call to mm.LaunchVMs results in `vm start all`
			// being used (to reduce calls to minimega). A nil slice vs. an empty
			// slice makes a difference here.
			start = nil
		}

		if err := mm.LaunchVMs(exp.Spec.ExperimentName(), start...); err != nil {
			if !o.mmErrAsWarn {
				mm.ClearNamespace(exp.Spec.ExperimentName())
				return fmt.Errorf("launching experiment VMs: %w", err)
			}

			if merr, ok := err.(*multierror.Error); ok {
				notes.AddWarnings(ctx, false, merr.Errors...)
			} else {
				notes.AddWarnings(ctx, false, err)
			}
		}

		schedule := make(map[string]string)

		for _, vm := range mm.GetVMInfo(mm.NS(exp.Spec.ExperimentName())) {
			schedule[vm.Name] = vm.Host
		}

		exp.Status.SetSchedule(schedule)

		vlans, err := mm.GetVLANs(mm.NS(exp.Spec.ExperimentName()))
		if err != nil {
			mm.ClearNamespace(exp.Spec.ExperimentName())
			return fmt.Errorf("processing experiment VLANs: %w", err)
		}

		exp.Status.SetVLANs(vlans)
	}

	start := time.Now().Format(time.RFC3339)

	if o.dryrun {
		start += "-DRYRUN"
	}

	if o.errChan == nil {
		if !o.dryrun {
			if exp.Spec.Topology().HasCommands() {
				if err := mm.ReadScriptFromFile(ccScript); err != nil {
					errors := multierror.Append(nil, fmt.Errorf("reading minimega cc script: %w", err))

					if err := mm.ClearNamespace(exp.Spec.ExperimentName()); err != nil {
						errors = multierror.Append(errors, fmt.Errorf("killing experiment VMs: %w", err))
					}

					return errors
				}
			}

			if err := handleDelayedVMs(ctx, exp.Spec.ExperimentName(), delays, c2s); err != nil {
				errors := multierror.Append(nil, fmt.Errorf("handling delayed VMs: %w", err))

				if err := mm.ClearNamespace(exp.Spec.ExperimentName()); err != nil {
					errors = multierror.Append(errors, fmt.Errorf("killing experiment VMs: %w", err))
				}

				return errors
			}
		}

		if err := app.ApplyApps(ctx, exp, app.Stage(app.ACTIONPOSTSTART), app.DryRun(o.dryrun)); err != nil {
			errors := multierror.Append(nil, fmt.Errorf("applying apps to experiment: %w", err))

			if err := app.ApplyApps(context.TODO(), exp, app.Stage(app.ACTIONCLEANUP), app.DryRun(o.dryrun)); err != nil {
				errors = multierror.Append(errors, fmt.Errorf("cleaning up app experiments: %w", err))
			}

			if err := mm.ClearNamespace(exp.Spec.ExperimentName()); err != nil {
				errors = multierror.Append(errors, fmt.Errorf("killing experiment VMs: %w", err))
			}

			return errors
		}
	} else {
		go func() {
			defer close(o.errChan)

			if !o.dryrun {
				if exp.Spec.Topology().HasCommands() {
					if err := mm.ReadScriptFromFile(ccScript); err != nil {
						o.errChan <- fmt.Errorf("reading minimega cc script: %w", err)

						if err := Stop(exp.Spec.ExperimentName()); err != nil {
							o.errChan <- fmt.Errorf("stopping experiment: %w", err)
						}

						return
					}
				}

				if err := handleDelayedVMs(ctx, exp.Spec.ExperimentName(), delays, c2s); err != nil {
					o.errChan <- fmt.Errorf("handling delayed VMs: %w", err)

					if err := Stop(exp.Spec.ExperimentName()); err != nil {
						o.errChan <- fmt.Errorf("stopping experiment: %w", err)
					}

					return
				}
			}

			if err := app.ApplyApps(ctx, exp, app.Stage(app.ACTIONPOSTSTART), app.DryRun(o.dryrun)); err != nil {
				o.errChan <- fmt.Errorf("applying apps to experiment: %w", err)

				if err := Stop(exp.Spec.ExperimentName()); err != nil {
					o.errChan <- fmt.Errorf("stopping experiment: %w", err)
				}
			}
		}()
	}

	exp.Status.SetStartTime(start)

	c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
	c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		mm.ClearNamespace(exp.Spec.ExperimentName())
		return fmt.Errorf("updating experiment config: %w", err)
	}

	for _, hook := range hooks["start"] {
		hook("start", o.name)
	}

	return nil
}

// Stop stops the experiment with the given name. It returns any errors
// encountered while stopping the experiment.
func Stop(name string) error {
	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	if !exp.Running() {
		return fmt.Errorf("experiment isn't running")
	}

	dryrun := strings.HasSuffix(exp.Status.StartTime(), "-DRYRUN")

	var errors error

	if err := app.ApplyApps(context.TODO(), exp, app.Stage(app.ACTIONCLEANUP), app.DryRun(dryrun)); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("cleaning up app experiments: %w", err))
	}

	if !dryrun {
		if err := mm.ClearNamespace(exp.Spec.ExperimentName()); err != nil {
			errors = multierror.Append(errors, fmt.Errorf("killing experiment VMs: %w", err))
		}
	}

	exp.Status.SetStartTime("")

	c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
	c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("updating experiment config: %w", err))
	}

	for _, hook := range hooks["stop"] {
		hook("stop", name)
	}

	return errors
}

func Status(name string) (*v1.ExperimentStatus, error) {
	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return nil, fmt.Errorf("unable to get experiment status from store: %w", err)
	}

	var status v1.ExperimentStatus

	if err := mapstructure.Decode(c.Status, &status); err != nil {
		return nil, fmt.Errorf("unable to decode experiment status: %w", err)
	}

	return &status, nil
}

func Running(name string) bool {
	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return false
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return false
	}

	return exp.Running()
}

func Save(opts ...SaveOption) error {
	o := newSaveOptions(opts...)

	if o.name == "" {
		return fmt.Errorf("experiment name required")
	}

	c, _ := store.NewConfig("experiment/" + o.name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", o.name, err)
	}

	if o.spec == nil {
		if o.saveNilSpec {
			c.Spec = nil
		}
	} else {
		c.Spec = structs.MapDefaultCase(o.spec, structs.CASESNAKE)
	}

	if o.status == nil {
		if o.saveNilStatus {
			c.Status = nil
		}
	} else {
		c.Status = structs.MapDefaultCase(o.status, structs.CASESNAKE)
	}

	if err := store.Update(c); err != nil {
		return fmt.Errorf("saving experiment config: %w", err)
	}

	return nil
}

// Reconfigure executes the 'configure' stage for all apps the given experiment
// is configured to use. It returns any errors encountered while reconfiguring
// the experiment.
func Reconfigure(name string) error {
	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	if exp.Running() {
		return fmt.Errorf("experiment is running")
	}

	if err := app.ApplyApps(context.TODO(), exp, app.Stage(app.ACTIONCONFIG)); err != nil {
		return fmt.Errorf("configuring apps for experiment: %w", err)
	}

	c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)

	if err := config.Update(c.FullName(), c); err != nil {
		return fmt.Errorf("updating experiment config: %w", err)
	}

	return nil
}

// TriggerRunning executes the 'running' stage for the given apps in the given
// experiment. If no apps are passed, then all experiment apps will have their
// 'running' stage triggered.
func TriggerRunning(ctx context.Context, name string, apps ...string) error {
	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	if !exp.Running() {
		return fmt.Errorf("experiment is not running")
	}

	if err := app.ApplyApps(ctx, exp, app.Stage(app.ACTIONRUNNING), app.FilterApp(apps...)); err != nil {
		return fmt.Errorf("triggering apps for experiment: %w", err)
	}

	return nil
}

func Delete(name string) error {
	if Running(name) {
		return fmt.Errorf("cannot delete a running experiment")
	}

	c, err := config.Get("experiment/"+name, true)
	if err != nil {
		return fmt.Errorf("getting experiment %s: %w", name, err)
	}

	if err := config.Delete("experiment/" + name); err != nil {
		return fmt.Errorf("deleting experiment %s: %w", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	var errors error

	// Delete any snapshot files created by this headnode for this experiment
	// after deleting the experiment.
	if err := deleteC2AndSnapshots(exp); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("deleting experiment snapshots and CC responses: %w", err))
	}

	if err := os.RemoveAll(exp.Spec.BaseDir()); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("deleting experiment base directory: %w", err))
	}

	for _, hook := range hooks["delete"] {
		hook("delete", name)
	}

	return errors
}

func Files(name, filter string) (file.Files, error) {
	return file.GetExperimentFiles(name, filter)
}

func File(name, filePath string) ([]byte, error) {
	files, err := file.GetExperimentFiles(name, "")
	if err != nil {
		return nil, fmt.Errorf("getting list of experiment files: %w", err)
	}

	for _, c := range mm.GetExperimentCaptures(mm.NS(name)) {
		if strings.Contains(c.Filepath, path.Base(filePath)) {
			return nil, mm.ErrCaptureExists
		}
	}

	for _, f := range files {
		if filePath == f.Path {
			headnode, _ := os.Hostname()

			file.CopyFile(fmt.Sprintf("/%s/files/%s", name, f.Path), headnode, nil)

			path := fmt.Sprintf("%s/images/%s/files/%s", common.PhenixBase, name, f.Path)

			data, err := ioutil.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading contents of file: %w", err)
			}

			return data, nil
		}
	}

	return nil, fmt.Errorf("file not found")
}

func deleteC2AndSnapshots(exp *types.Experiment) error {
	// Snapshot naming convention is as follows:
	//   {hostname}_{experiment_name}_{vm_name}_snapshot
	// Now, we *could* use {hostname}_{experiment_name}_*_snapshot as the deletion
	// filter to delete all the VMs for a given experiment on a given headnode,
	// but... if another experiment has a similar name but with an underscore in
	// it, we may accidentally end up deleting those snapshots as well. For
	// example, lets say we have two experiments in phenix, one named "foo" and
	// the other named "foo_bar". Using the deletion filter above, if we went to
	// delete snapshots for experiment "foo" we would also delete the snapshots
	// for experiment "foo_bar". As such, the safest bet is to loop through all
	// the VMs in an experiment and delete the snapshots one by one. Note that by
	// including the hostname we ensure that only snapshots created for
	// experiments by this headnode get deleted. This is important when multiple
	// headnodes exist for a single minimega cluster.

	var (
		expName  = exp.Metadata.Name
		headnode = mm.Headnode()
	)

	c2Path := expName + "/miniccc_responses"

	if err := file.DeleteFile(c2Path); err != nil {
		return fmt.Errorf("deleting CC responses for experiment %s: %w", expName, err)
	}

	for _, node := range exp.Spec.Topology().Nodes() {
		if node.External() {
			continue
		}

		hostname := node.General().Hostname()
		snapshot := fmt.Sprintf("%s_%s_%s_snapshot", headnode, expName, hostname)

		if err := file.DeleteFile(snapshot); err != nil {
			return fmt.Errorf("deleting snapshot file for VM %s in experiment %s: %w", hostname, expName, err)
		}
	}

	return nil
}

func handleDelayedVMs(ctx context.Context, ns string, delays map[string]time.Duration, c2s map[string]map[string]bool) error {
	if len(delays) == 0 && len(c2s) == 0 {
		return nil
	}

	notes.AddInfo(ctx, true, "Waiting for delayed VMs to be started...")

	var (
		wg     sync.WaitGroup
		errors error
	)

	for host, delay := range delays {
		wg.Add(1)

		go func(host string, delay time.Duration) {
			defer wg.Done()

			select {
			case <-ctx.Done():
				errors = multierror.Append(errors, ctx.Err())
				return
			case <-time.After(delay):
				cmd := mmcli.NewNamespacedCommand(ns)
				cmd.Command = "vm start " + host

				if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
					errors = multierror.Append(errors, NewDelayedVMError(host, err, "starting VM %s", host))
					return
				}

				notes.AddInfo(ctx, true, fmt.Sprintf("Time delayed VM %s started", host))
				pubsub.Publish("delayed-start", fmt.Sprintf("%s/%s", ns, host))
			}
		}(host, delay)
	}

	for host, others := range c2s {
		wg.Add(1)

		go func(host string, others map[string]bool) {
			defer wg.Done()

			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					errors = multierror.Append(errors, ctx.Err())
					return
				case <-ticker.C:
					done := true

					for other, useUUID := range others {
						opts := []mm.C2Option{mm.C2NS(ns), mm.C2VM(other), mm.C2Timeout(1 * time.Second)}

						if useUUID {
							opts = append(opts, mm.C2IDClientsByUUID())
						}

						if mm.IsC2ClientActive(opts...) != nil {
							done = false
							break
						}
					}

					if done {
						cmd := mmcli.NewNamespacedCommand(ns)
						cmd.Command = "vm start " + host

						if err := mmcli.ErrorResponse(mmcli.Run(cmd)); err != nil {
							errors = multierror.Append(errors, NewDelayedVMError(host, err, "starting VM %s", host))
							return
						}

						notes.AddInfo(ctx, true, fmt.Sprintf("C2 delayed VM %s started", host))
						pubsub.Publish("delayed-start", fmt.Sprintf("%s/%s", ns, host))

						return
					}
				}
			}
		}(host, others)
	}

	wg.Wait()

	if errors != nil {
		if err := mm.ClearNamespace(ns); err != nil {
			errors = multierror.Append(errors, fmt.Errorf("killing experiment VMs: %w", err))
		}

		return errors
	}

	return nil
}
