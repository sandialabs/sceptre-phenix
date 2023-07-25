package types

import (
	"fmt"
	"path/filepath"
	"strings"

	"phenix/store"
	ifaces "phenix/types/interfaces"
	"phenix/types/version"
	"phenix/util/common"
	"phenix/util/mm"

	"github.com/activeshadow/structs"
	"github.com/mitchellh/mapstructure"
)

type Experiment struct {
	Metadata store.ConfigMetadata    `json:"metadata" yaml:"metadata"` // experiment configuration metadata
	Spec     ifaces.ExperimentSpec   `json:"spec" yaml:"spec"`         // reference to latest versioned experiment spec
	Status   ifaces.ExperimentStatus `json:"status" yaml:"status"`     // reference to latest versioned experiment status

	// used for user apps
	Hosts mm.Hosts `json:"hosts,omitempty" yaml:"hosts,omitempty"` // cluster host details
}

func NewExperiment(md store.ConfigMetadata) *Experiment {
	ver := version.StoredVersion["Experiment"]

	spec, _ := version.GetVersionedSpecForKind("Experiment", ver)
	status, _ := version.GetVersionedStatusForKind("Experiment", ver)

	spec.(ifaces.ExperimentSpec).Init()
	status.(ifaces.ExperimentStatus).Init()

	return &Experiment{
		Metadata: md,
		Spec:     spec.(ifaces.ExperimentSpec),
		Status:   status.(ifaces.ExperimentStatus),
	}
}

func (this *Experiment) Reload() error {
	c, err := store.NewConfig("experiment/" + this.Metadata.Name)
	if err != nil {
		return fmt.Errorf("getting experiment: %w", err)
	}

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", this.Metadata.Name, err)
	}

	exp, err := DecodeExperimentFromConfig(*c)
	if err != nil {
		return fmt.Errorf("decoding experiment %s: %w", this.Metadata.Name, err)
	}

	this.Metadata = exp.Metadata
	this.Spec = exp.Spec
	this.Status = exp.Status

	return nil
}

func (this Experiment) WriteToStore(statusOnly bool) error {
	name := this.Metadata.Name

	c, _ := store.NewConfig("experiment/" + name)

	if err := store.Get(c); err != nil {
		return fmt.Errorf("getting experiment %s from store: %w", name, err)
	}

	// limit metadata updates to annotations so name doesn't accidentally get changed
	c.Metadata.Annotations = this.Metadata.Annotations

	if !statusOnly {
		c.Spec = structs.MapDefaultCase(this.Spec, structs.CASESNAKE)
	}

	c.Status = structs.MapDefaultCase(this.Status, structs.CASESNAKE)

	if err := store.Update(c); err != nil {
		return fmt.Errorf("saving experiment config: %w", err)
	}

	return nil
}

func (this *Experiment) SetSpec(spec ifaces.ExperimentSpec) {
	this.Spec = spec
}

func (this Experiment) Apps() []ifaces.ScenarioApp {
	if this.Spec.Scenario() != nil {
		return this.Spec.Scenario().Apps()
	}

	return nil
}

func (this Experiment) App(name string) ifaces.ScenarioApp {
	if this.Spec.Scenario() == nil {
		return nil
	}

	for _, app := range this.Spec.Scenario().Apps() {
		if app.Name() == name {
			return app
		}
	}

	return nil
}

func (this Experiment) Running() bool {
	if this.Status == nil {
		return false
	}

	if this.Status.StartTime() == "" {
		return false
	}

	return true
}

func (this Experiment) DryRun() bool {
	if this.Status == nil {
		return false
	}

	if this.Status.StartTime() == "" {
		return false
	}

	return strings.Contains(this.Status.StartTime(), "DRYRUN")
}

func (this Experiment) FilesDir() string {
	return filepath.Join(common.PhenixBase, "images", this.Metadata.Name, "files")
}

func RunningExperiments() ([]*Experiment, error) {
	configs, err := store.List("Experiment")
	if err != nil {
		return nil, fmt.Errorf("getting list of experiment configs from store: %w", err)
	}

	var experiments []*Experiment

	for _, c := range configs {
		exp, err := DecodeExperimentFromConfig(c)
		if err != nil {
			return nil, fmt.Errorf("decoding experiment %s from config: %w", c.Metadata.Name, err)
		}

		if exp.Running() {
			experiments = append(experiments, exp)
		}
	}

	return experiments, nil
}

func DecodeExperimentFromConfig(c store.Config) (*Experiment, error) {
	iface, err := version.GetVersionedSpecForKind(c.Kind, c.APIVersion())
	if err != nil {
		return nil, fmt.Errorf("getting versioned spec for config: %w", err)
	}

	if err := mapstructure.Decode(c.Spec, &iface); err != nil {
		// If we have a decoding error, it's likely due to the embedded topology or
		// scenario not being the lastest version.

		var (
			kbArticle = "EX-SC-UPG-01"
			kbLink    = "https://phenix.sceptre.dev/kb/#article-ex-sc-upg-01"
			kbError   = fmt.Errorf("decoding versioned spec for experiment %s: %w\n\nPlease see KB article %s at %s", c.Metadata.Name, err, kbArticle, kbLink)
		)

		tn, ok := c.Metadata.Annotations["topology"]
		if !ok {
			return nil, kbError
		}

		tc, _ := store.NewConfig("topology/" + tn)

		if err := store.Get(tc); err != nil {
			return nil, kbError
		}

		if tc.APIVersion() != version.StoredVersion["Topology"] {
			spec, err := DecodeTopologyFromConfig(*tc)
			if err != nil {
				return nil, kbError
			}

			c.Spec["topology"] = spec
		}

		sn, ok := c.Metadata.Annotations["scenario"]
		if ok {
			sc, _ := store.NewConfig("scenario/" + sn)

			if err := store.Get(sc); err != nil {
				return nil, kbError
			}

			if sc.APIVersion() != version.StoredVersion["Scenario"] {
				spec, err := DecodeScenarioFromConfig(*sc)
				if err != nil {
					return nil, kbError
				}

				c.Spec["scenario"] = spec
			}
		}

		if err := mapstructure.Decode(c.Spec, &iface); err != nil {
			return nil, kbError
		}
	}

	spec, ok := iface.(ifaces.ExperimentSpec)
	if !ok {
		return nil, fmt.Errorf("invalid spec in config")
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
		return nil, fmt.Errorf("invalid status in config")
	}

	exp := &Experiment{
		Metadata: c.Metadata,
		Spec:     spec,
		Status:   status,
	}

	return exp, nil
}
