package experiment

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"phenix/api/config"
	"phenix/app"
	"phenix/internal/common"
	"phenix/internal/file"
	"phenix/internal/mm"
	"phenix/scheduler"
	"phenix/store"
	"phenix/tmpl"
	"phenix/types"
	"phenix/types/version"
	v1 "phenix/types/version/v1"

	"github.com/activeshadow/structs"
	"github.com/hashicorp/go-multierror"
	"github.com/mitchellh/mapstructure"
)

func init() {
	config.RegisterConfigHook("Experiment", func(stage string, c *store.Config) error {
		switch stage {
		case "create":
			exp, err := types.DecodeExperimentFromConfig(*c)
			if err != nil {
				return fmt.Errorf("decoding experiment from config: %w", err)
			}

			exp.Spec.Init()

			if err := exp.Spec.VerifyScenario(context.TODO()); err != nil {
				return fmt.Errorf("verifying experiment scenario: %w", err)
			}

			if err := app.ApplyApps(context.TODO(), exp, app.Stage(app.ACTIONCONFIG)); err != nil {
				return fmt.Errorf("applying apps to experiment: %w", err)
			}

			c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)

			if err := types.ValidateConfigSpec(*c); err != nil {
				return fmt.Errorf("validating experiment config: %w", err)
			}
		case "delete":
			var errors error

			exp, err := types.DecodeExperimentFromConfig(*c)
			if err != nil {
				return fmt.Errorf("decoding experiment from config: %w", err)
			}

			// Delete any snapshot files created by this headnode for this experiment
			// after deleting the experiment.
			if err := deleteSnapshots(exp); err != nil {
				errors = multierror.Append(errors, fmt.Errorf("deleting experiment snapshots: %w", err))
			}

			if err := os.RemoveAll(exp.Spec.BaseDir()); err != nil {
				errors = multierror.Append(errors, fmt.Errorf("deleting experiment base directory: %w", err))
			}

			return errors
		}

		return nil
	})
}

// List collects experiments, each in a struct that references the latest
// versioned experiment spec and status. It returns a slice of experiments and
// any errors encountered while gathering and decoding them.
func List() ([]types.Experiment, error) {
	configs, err := store.List("Experiment")
	if err != nil {
		return nil, fmt.Errorf("getting list of experiment configs from store: %w", err)
	}

	var experiments []types.Experiment

	for _, c := range configs {
		exp, err := types.DecodeExperimentFromConfig(c)
		if err != nil {
			return nil, fmt.Errorf("decoding experiment from config: %w", err)
		}

		experiments = append(experiments, *exp)
	}

	return experiments, nil
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

	if o.scenario != "" {
		scenarioC, _ := store.NewConfig("scenario/" + o.scenario)

		if err := store.Get(scenarioC); err != nil {
			return fmt.Errorf("scenario doesn't exist")
		}

		topo, ok := scenarioC.Metadata.Annotations["topology"]
		if !ok {
			return fmt.Errorf("topology annotation missing from scenario")
		}

		var found bool

		for _, t := range strings.Split(topo, ",") {
			if t == o.topology {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("experiment/scenario topology mismatch")
		}

		// This will upgrade the scenario to the latest known version if needed.
		scenario, err := types.DecodeScenarioFromConfig(*scenarioC)
		if err != nil {
			return fmt.Errorf("decoding scenario from config: %w", err)
		}

		// This will look for `fromScenario` keys in the provided scenario and, if
		// present, replace the config from the specified scenario.
		for _, app := range scenario.Apps() {
			if app.FromScenario() != "" {
				fromScenarioC, _ := store.NewConfig("scenario/" + app.FromScenario())

				if err := store.Get(fromScenarioC); err != nil {
					return fmt.Errorf("scenario %s doesn't exist", app.FromScenario())
				}

				topo, ok := scenarioC.Metadata.Annotations["topology"]
				if !ok {
					return fmt.Errorf("topology annotation missing from scenario %s", app.FromScenario())
				}

				if topo != o.topology {
					return fmt.Errorf("experiment/scenario topology mismatch")
				}

				// This will upgrade the scenario to the latest known version if needed.
				fromScenario, err := types.DecodeScenarioFromConfig(*fromScenarioC)
				if err != nil {
					return fmt.Errorf("decoding scenario %s from config: %w", app.FromScenario(), err)
				}

				var found bool

				for _, fromApp := range fromScenario.Apps() {
					if fromApp.Name() == app.Name() {
						app.SetAssetDir(fromApp.AssetDir())
						app.SetMetadata(fromApp.Metadata())
						app.SetHosts(fromApp.Hosts())

						found = true
						break
					}
				}

				if !found {
					return fmt.Errorf("no app named %s in scenario %s", app.Name(), app.FromScenario())
				}

			}
		}

		meta.Annotations["scenario"] = o.scenario
		specMap["scenario"] = scenario
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

	exp.Spec.Init()

	if err := exp.Spec.VerifyScenario(ctx); err != nil {
		return fmt.Errorf("verifying experiment scenario: %w", err)
	}

	if err := app.ApplyApps(context.TODO(), exp, app.Stage(app.ACTIONCONFIG)); err != nil {
		return fmt.Errorf("applying apps to experiment: %w", err)
	}

	c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)

	if err := types.ValidateConfigSpec(*c); err != nil {
		return fmt.Errorf("validating experiment config: %w", err)
	}

	if err := store.Create(c); err != nil {
		return fmt.Errorf("storing experiment config: %w", err)
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

	filename := fmt.Sprintf("%s/mm_files/%s.mm", exp.Spec.BaseDir(), exp.Spec.ExperimentName())

	if err := tmpl.CreateFileFromTemplate("minimega_script.tmpl", exp.Spec, filename); err != nil {
		return fmt.Errorf("generating minimega script: %w", err)
	}

	if o.dryrun {
		exp.Status.SetVLANs(exp.Spec.VLANs().Aliases())
	} else {
		// Delete any snapshot files created by this headnode for this experiment
		// previously before starting the experiment. This way, cluster nodes that VMs
		// get scheduled on will pull the most up-to-date snapshot files. We don't do
		// this after stopping an experiment just in case users need to access the
		// snapshots for any reason, but we do clean them up when an experiment is
		// deleted.
		if err := deleteSnapshots(exp); err != nil {
			return fmt.Errorf("deleting experiment snapshots: %w", err)
		}

		if err := mm.ReadScriptFromFile(filename); err != nil {
			mm.ClearNamespace(exp.Spec.ExperimentName())
			return fmt.Errorf("reading minimega script: %w", err)
		}

		if err := mm.LaunchVMs(exp.Spec.ExperimentName()); err != nil {
			mm.ClearNamespace(exp.Spec.ExperimentName())
			return fmt.Errorf("launching experiment VMs: %w", err)
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

	if o.dryrun {
		exp.Status.SetStartTime(time.Now().Format(time.RFC3339) + "-DRYRUN")
	} else {
		exp.Status.SetStartTime(time.Now().Format(time.RFC3339))
	}

	if o.errChan == nil {
		if err := app.ApplyApps(ctx, exp, app.Stage(app.ACTIONPOSTSTART), app.DryRun(o.dryrun)); err != nil {
			mm.ClearNamespace(exp.Spec.ExperimentName())
			return fmt.Errorf("applying apps to experiment: %w", err)
		}

		c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
		c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)

		if err := store.Update(c); err != nil {
			mm.ClearNamespace(exp.Spec.ExperimentName())
			return fmt.Errorf("updating experiment config: %w", err)
		}
	} else {
		go func() {
			if err := app.ApplyApps(ctx, exp, app.Stage(app.ACTIONPOSTSTART), app.DryRun(o.dryrun)); err != nil {
				mm.ClearNamespace(exp.Spec.ExperimentName())
				o.errChan <- fmt.Errorf("applying apps to experiment: %w", err)
			}

			c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
			c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)

			if err := store.Update(c); err != nil {
				mm.ClearNamespace(exp.Spec.ExperimentName())
				o.errChan <- fmt.Errorf("updating experiment config: %w", err)
			}

			close(o.errChan)
		}()

		c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
		c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)

		if err := store.Update(c); err != nil {
			mm.ClearNamespace(exp.Spec.ExperimentName())
			return fmt.Errorf("updating experiment config: %w", err)
		}
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

	if err := app.ApplyApps(context.TODO(), exp, app.Stage(app.ACTIONCLEANUP)); err != nil {
		return fmt.Errorf("applying apps to experiment: %w", err)
	}

	if !dryrun {
		if err := mm.ClearNamespace(exp.Spec.ExperimentName()); err != nil {
			return fmt.Errorf("killing experiment VMs: %w", err)
		}
	}

	exp.Status.SetStartTime("")

	c.Spec = structs.MapDefaultCase(exp.Spec, structs.CASESNAKE)
	c.Status = structs.MapDefaultCase(exp.Status, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		return fmt.Errorf("updating experiment config: %w", err)
	}

	return nil
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

	if err := store.Update(c); err != nil {
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
		return fmt.Errorf("configuring apps for experiment: %w", err)
	}

	return nil
}

func Delete(name string) error {
	if Running(name) {
		return fmt.Errorf("cannot delete a running experiment")
	}

	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s: %w", name, err)
	}

	if err := store.Delete(c); err != nil {
		return fmt.Errorf("deleting experiment %s: %w", name, err)
	}

	exp, err := types.DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment from config: %w", err)
	}

	var errors error

	// Delete any snapshot files created by this headnode for this experiment
	// after deleting the experiment.
	if err := deleteSnapshots(exp); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("deleting experiment snapshots: %w", err))
	}

	if err := os.RemoveAll(exp.Spec.BaseDir()); err != nil {
		errors = multierror.Append(errors, fmt.Errorf("deleting experiment base directory: %w", err))
	}

	return errors
}

func Files(name string) ([]string, error) {
	return file.GetExperimentFileNames(name)
}

func File(name, fileName string) ([]byte, error) {
	files, err := file.GetExperimentFileNames(name)
	if err != nil {
		return nil, fmt.Errorf("getting list of experiment files: %w", err)
	}

	for _, c := range mm.GetExperimentCaptures(mm.NS(name)) {
		if strings.Contains(c.Filepath, fileName) {
			return nil, mm.ErrCaptureExists
		}
	}

	for _, f := range files {
		if fileName == f {
			headnode, _ := os.Hostname()

			file.CopyFile(headnode, fmt.Sprintf("/%s/files/%s", name, f), nil)

			path := fmt.Sprintf("%s/images/%s/files/%s", common.PhenixBase, name, f)

			data, err := ioutil.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("reading contents of file: %w", err)
			}

			return data, nil
		}
	}

	return nil, fmt.Errorf("file not found")
}

func deleteSnapshots(exp *types.Experiment) error {
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

	for _, node := range exp.Spec.Topology().Nodes() {
		hostname := node.General().Hostname()
		snapshot := fmt.Sprintf("%s_%s_%s_snapshot", headnode, expName, hostname)

		if err := file.DeleteFile(snapshot); err != nil {
			return fmt.Errorf("deleting snapshot file for VM %s in experiment %s: %w", hostname, expName, err)
		}
	}

	return nil
}
