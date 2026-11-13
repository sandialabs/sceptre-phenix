package types

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util/common"
	"phenix/util/mm"
)

type Experiment struct {
	Metadata store.ConfigMetadata    `json:"metadata" yaml:"metadata"` // experiment configuration metadata
	Spec     ifaces.ExperimentSpec   `json:"spec"     yaml:"spec"`     // reference to latest versioned experiment spec
	Status   ifaces.ExperimentStatus `json:"status"   yaml:"status"`   // reference to latest versioned experiment status

	// used for user apps
	Hosts mm.Hosts `json:"hosts,omitempty" yaml:"hosts,omitempty"` // cluster host details
	VMs   mm.VMs   `json:"vms,omitempty"   yaml:"vms,omitempty"`   // VM runtime details
}

func NewExperiment(md store.ConfigMetadata) *Experiment {
	ver := version.StoredVersion["Experiment"]

	spec, _ := version.GetVersionedSpecForKind("Experiment", ver)
	status, _ := version.GetVersionedStatusForKind("Experiment", ver)

	specSpec, _ := spec.(ifaces.ExperimentSpec)
	_ = specSpec.Init()
	statusStatus, _ := status.(ifaces.ExperimentStatus)
	_ = statusStatus.Init()

	return &Experiment{ //nolint:exhaustruct // partial initialization
		Metadata: md,
		Spec:     specSpec,
		Status:   statusStatus,
	}
}

func (e *Experiment) Reload() error {
	c, err := store.NewConfig("experiment/" + e.Metadata.Name)
	if err != nil {
		return fmt.Errorf("getting experiment: %w", err)
	}

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", e.Metadata.Name, err)
	}

	exp, err := DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment %s: %w", e.Metadata.Name, err)
	}

	e.Metadata = exp.Metadata
	e.Spec = exp.Spec
	e.Status = exp.Status

	return nil
}

// statusMu serializes the experiment writes of this process, so that the read,
// change and write of UpdateStatus cannot interleave with another write.
var statusMu sync.Mutex //nolint:gochecknoglobals // process-wide store guard

func (e Experiment) WriteToStore(statusOnly bool) error {
	statusMu.Lock()
	defer statusMu.Unlock()

	name := e.Metadata.Name

	c, _ := store.NewConfig("experiment/" + name)

	err := store.Get(c)
	if err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	// limit metadata updates to annotations so name doesn't accidentally get changed
	c.Metadata.Annotations = e.Metadata.Annotations

	if !statusOnly {
		c.Spec = structs.MapDefaultCase(e.Spec, structs.CASESNAKE)
	}

	c.Status = structs.MapDefaultCase(e.Status, structs.CASESNAKE)

	err = store.Update(c)
	if err != nil {
		return fmt.Errorf("saving experiment config: %w", err)
	}

	return nil
}

// UpdateStatus applies fn to the status that the store holds and writes the
// result back, so it keeps what other writers stored since e was read. When
// the write succeeds, the status of e becomes the written status. WriteToStore
// writes the whole status of e instead.
func (e *Experiment) UpdateStatus(fn func(status ifaces.ExperimentStatus)) error {
	statusMu.Lock()
	defer statusMu.Unlock()

	name := e.Metadata.Name

	c, err := store.NewConfig("experiment/" + name)
	if err != nil {
		return fmt.Errorf("getting experiment: %w", err)
	}

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	stored, err := DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment %s: %w", name, err)
	}

	fn(stored.Status)

	c.Status = structs.MapDefaultCase(stored.Status, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		return fmt.Errorf("saving experiment config: %w", err)
	}

	e.Status = stored.Status

	return nil
}

// UpdateAppStatus is UpdateStatus for the status of app only. fn gets the
// stored status of app, empty when there is none, and changes it in place.
func (e *Experiment) UpdateAppStatus(app string, fn func(status map[string]any)) error {
	return e.UpdateStatus(func(s ifaces.ExperimentStatus) {
		status := make(map[string]any)
		_ = s.ParseAppStatus(app, &status) // a missing status stays empty

		fn(status)

		s.SetAppStatus(app, status)
	})
}

func (e *Experiment) SetSpec(spec ifaces.ExperimentSpec) {
	e.Spec = spec
}

func (e Experiment) Apps() []ifaces.ScenarioApp {
	if e.Spec.Scenario() != nil {
		return e.Spec.Scenario().Apps()
	}

	return nil
}

func (e Experiment) App(name string) ifaces.ScenarioApp {
	if e.Spec.Scenario() == nil {
		return nil
	}

	for _, app := range e.Spec.Scenario().Apps() {
		if app.Name() == name {
			return app
		}
	}

	return nil
}

func (e Experiment) Running() bool {
	if e.Status == nil {
		return false
	}

	if e.Status.StartTime() == "" {
		return false
	}

	return true
}

func (e Experiment) DryRun() bool {
	if e.Status == nil {
		return false
	}

	if e.Status.StartTime() == "" {
		return false
	}

	return strings.Contains(e.Status.StartTime(), "DRYRUN")
}

func (e Experiment) FilesDir() string {
	return filepath.Join(common.PhenixBase, "images", e.Metadata.Name, "files")
}

// ExperimentDecodeError is the error for a stored experiment config that
// does not decode.
type ExperimentDecodeError struct {
	Name string // the name of the config
	Err  error
}

func (e *ExperimentDecodeError) Error() string {
	return fmt.Sprintf("decoding experiment %s from config: %v", e.Name, e.Err)
}

func (e *ExperimentDecodeError) Unwrap() error {
	return e.Err
}

// Experiments returns the stored experiments, or only the running ones when
// running is true. It returns an error when one stored config does not
// decode. Use DecodableExperiments to skip such configs.
func Experiments(running bool) ([]*Experiment, error) {
	experiments, failed, err := DecodableExperiments(running)
	if err != nil {
		return nil, err
	}

	if len(failed) > 0 {
		return nil, failed[0]
	}

	return experiments, nil
}

// DecodableExperiments returns the stored experiments that decode, or only
// the running ones when running is true. It also returns an error for each
// stored config that does not decode, in store order. The last error is set
// only when the store cannot list the configs.
func DecodableExperiments(running bool) ([]*Experiment, []*ExperimentDecodeError, error) {
	configs, err := store.List("Experiment")
	if err != nil {
		return nil, nil, fmt.Errorf("getting list of experiment configs from store: %w", err)
	}

	var (
		experiments []*Experiment
		failed      []*ExperimentDecodeError
	)

	for _, c := range configs {
		exp, err := DecodeExperimentFromConfig(c)
		if err != nil {
			failed = append(failed, &ExperimentDecodeError{Name: c.Metadata.Name, Err: err})

			continue
		}

		if running && !exp.Running() {
			continue
		}

		experiments = append(experiments, exp)
	}

	return experiments, failed, nil
}

func DecodeExperimentFromConfig(c store.Config) (*Experiment, error) {
	iface, err := version.GetVersionedSpecForKind(c.Kind, c.APIVersion())
	if err != nil {
		return nil, fmt.Errorf("getting versioned spec for config: %w", err)
	}

	if err := mapstructure.Decode(c.Spec, &iface); err != nil {
		return nil, fmt.Errorf("decoding versioned spec for experiment %s: %w", c.Metadata.Name, err)
	}

	spec, ok := iface.(ifaces.ExperimentSpec)
	if !ok {
		return nil, errors.New("invalid spec in config")
	}

	iface, err = version.GetVersionedStatusForKind(c.Kind, c.APIVersion())
	if err != nil {
		return nil, fmt.Errorf("getting versioned status for config: %w", err)
	}

	if err := mapstructure.Decode(c.Status, &iface); err != nil {
		return nil, fmt.Errorf("decoding versioned status: %w", err)
	}

	status, ok := iface.(ifaces.ExperimentStatus)
	if !ok {
		return nil, errors.New("invalid status in config")
	}

	exp := &Experiment{ //nolint:exhaustruct // partial initialization
		Metadata: c.Metadata,
		Spec:     spec,
		Status:   status,
	}

	return exp, nil
}
