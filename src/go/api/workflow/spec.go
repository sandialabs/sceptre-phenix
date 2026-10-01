package workflow

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/mitchellh/mapstructure"

	"phenix/api/experiment"
	"phenix/util/common"
)

const (
	// BranchAnnotation is the experiment annotation that maps an experiment to
	// a workflow branch.
	BranchAnnotation = "phenix.workflow/branch"

	// TagsAnnotation is the experiment annotation that holds the tags passed to
	// the most recent apply, joined with commas.
	TagsAnnotation = "phenix.workflow/tags"

	// defaultBridgeName is the bridge used when a workflow config sets none.
	// Any number of experiments may share it.
	defaultBridgeName = "phenix"
)

var (
	// ErrInvalidSpec is wrapped by every error that [Decode] and
	// [Spec.Validate] return.
	ErrInvalidSpec = errors.New("invalid workflow config")

	// ErrInvalidName is wrapped by every error that [ValidateName] returns.
	ErrInvalidName = errors.New("invalid workflow name")
)

// validName matches a workflow branch name that is also safe to use as a
// single directory name.
var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Auto is the auto section of a workflow spec. It controls what an apply may
// do without being asked.
type Auto struct {
	Create  string `mapstructure:"create"`
	Update  *bool  `mapstructure:"update"`
	Restart *bool  `mapstructure:"restart"`
}

// VLANRange is the vlanRange section of a workflow spec.
type VLANRange struct {
	Min int `mapstructure:"min"`
	Max int `mapstructure:"max"`
}

// Spec is the spec section of a phenix workflow config.
type Spec struct {
	Auto *Auto `mapstructure:"auto"`

	Topology   string            `mapstructure:"topology"`
	Scenario   string            `mapstructure:"scenario"`
	VLANs      map[string]int    `mapstructure:"vlans"`
	Schedules  map[string]string `mapstructure:"schedules"`
	DeployMode string            `mapstructure:"deployMode"`
	UseGREMesh bool              `mapstructure:"useGREMesh"`

	VLANRange *VLANRange `mapstructure:"vlanRange"`

	DefaultBridge string `mapstructure:"defaultBridge"`
}

// Decode decodes the spec section of a workflow config. A null value leaves
// its field unset, a value of the wrong type is an error, and keys that no
// field uses are ignored; the Workflow schema is what rejects them. Errors
// wrap [ErrInvalidSpec].
func Decode(spec map[string]any) (Spec, error) {
	var decoded Spec

	if err := mapstructure.Decode(spec, &decoded); err != nil {
		return Spec{}, fmt.Errorf("%w: %w", ErrInvalidSpec, err)
	}

	return decoded, nil
}

// ValidateName checks that name can be used as a workflow branch name and as a
// single directory name: it must start with an ASCII letter or digit and hold
// only ASCII letters, digits, '.', '_' and '-'. Errors wrap [ErrInvalidName].
func ValidateName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf(
			"%w %q: must start with an ASCII letter or digit and contain only ASCII letters, digits, '.', '_' and '-'",
			ErrInvalidName, name,
		)
	}

	return nil
}

// Validate reports whether the server can apply the spec. Callers run it
// before anything is created, stopped or started. Errors wrap
// [ErrInvalidSpec].
func (s Spec) Validate() error {
	if name := s.DefaultBridgeName(); len(name) > experiment.MaxBridgeNameLength {
		return fmt.Errorf(
			"%w: default bridge name %q is longer than %d characters",
			ErrInvalidSpec, name, experiment.MaxBridgeNameLength,
		)
	}

	return nil
}

// AutoUpdate reports whether an apply may update an experiment that is already
// mapped to the branch. It defaults to true.
func (s Spec) AutoUpdate() bool {
	if s.Auto == nil || s.Auto.Update == nil {
		return true
	}

	return *s.Auto.Update
}

// AutoRestart reports whether an apply may start a new or updated experiment,
// and stop and restart a running one. It defaults to true.
func (s Spec) AutoRestart() bool {
	if s.Auto == nil || s.Auto.Restart == nil {
		return true
	}

	return *s.Auto.Restart
}

// ExperimentName returns the name of the experiment to create when none is
// mapped to the branch, or "" when auto.create is not set.
func (s Spec) ExperimentName() string {
	if s.Auto == nil {
		return ""
	}

	return s.Auto.Create
}

// ExperimentTopology returns the name of the topology config.
func (s Spec) ExperimentTopology() string {
	return s.Topology
}

// ExperimentScenario returns the name of the scenario config.
func (s Spec) ExperimentScenario() string {
	return s.Scenario
}

// VLANMappings returns the VLAN alias to ID mappings, or a new empty map when
// none are set.
func (s Spec) VLANMappings() map[string]int {
	if s.VLANs == nil {
		return make(map[string]int)
	}

	return s.VLANs
}

// ScheduleMappings returns the VM hostname to cluster host mappings, or a new
// empty map when none are set.
func (s Spec) ScheduleMappings() map[string]string {
	if s.Schedules == nil {
		return make(map[string]string)
	}

	return s.Schedules
}

// ExperimentDeployMode returns the deploy mode. An empty or unknown mode falls
// back to the server's deploy mode setting.
func (s Spec) ExperimentDeployMode() common.DeploymentMode {
	mode, err := common.ParseDeployMode(s.DeployMode)
	if err != nil {
		return common.DeployMode
	}

	return mode
}

// VLANMin returns the lowest VLAN ID to use, or 0 when vlanRange is not set.
func (s Spec) VLANMin() int {
	if s.VLANRange == nil {
		return 0
	}

	return s.VLANRange.Min
}

// VLANMax returns the highest VLAN ID to use, or 0 when vlanRange is not set.
func (s Spec) VLANMax() int {
	if s.VLANRange == nil {
		return 0
	}

	return s.VLANRange.Max
}

// DefaultBridgeName returns the default bridge name, or "phenix" when none is
// set.
func (s Spec) DefaultBridgeName() string {
	if s.DefaultBridge == "" {
		return defaultBridgeName
	}

	return s.DefaultBridge
}
