package workflow

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"phenix/api/config"
	"phenix/api/experiment"
	"phenix/types"
)

// Action is what applying a workflow config does to the experiment mapped to
// a workflow branch.
type Action string

// The actions a [Plan] can hold. The values are part of the JSON API.
const (
	ActionNone           Action = "none"
	ActionCreate         Action = "create"
	ActionCreateAndStart Action = "createAndStart"
	ActionUpdate         Action = "update"
	ActionUpdateAndStart Action = "updateAndStart"
	ActionRestart        Action = "restart"
)

// Reasons given for an [ActionNone] plan. The values are part of the JSON API.
const (
	reasonNoCreate  = "no experiment mapped and auto.create not set"
	reasonNoUpdate  = "auto.update is false"
	reasonNoRestart = "running and auto.restart is false"
)

var (
	// ErrConflict is wrapped by the error [NewPlan] returns when it cannot tell
	// which experiment the workflow branch means.
	ErrConflict = errors.New("workflow conflict")

	// ErrPlanChanged is wrapped by the error [CheckExpected] returns when the
	// plan no longer has the expected action.
	ErrPlanChanged = errors.New("workflow plan changed")

	// ErrUnknownAction is wrapped by the error [ParseAction] returns.
	ErrUnknownAction = errors.New("unknown workflow action")
)

// ParseAction returns the [Action] whose value is s. The match is exact and
// case-sensitive.
func ParseAction(s string) (Action, error) {
	switch action := Action(s); action {
	case ActionNone, ActionCreate, ActionCreateAndStart, ActionUpdate, ActionUpdateAndStart, ActionRestart:
		return action, nil
	default:
		return "", fmt.Errorf("%w %q", ErrUnknownAction, s)
	}
}

// Plan is what applying a workflow config to a branch does. Experiment names
// the experiment acted on, if any, and Reason explains an [ActionNone] plan.
type Plan struct {
	Action     Action `json:"action"`
	Experiment string `json:"experiment"`
	Reason     string `json:"reason"`
}

// NewPlan decides what applying spec to branch does. mapped holds the
// experiments mapped to branch (see [Mapped]) and existing the name of every
// stored experiment. The error wraps [ErrConflict] when more than one
// experiment is mapped, or when none is and auto.create names an existing
// experiment, and [ErrInvalidSpec] when auto.create is not a name an
// experiment can be created with.
func NewPlan(spec Spec, branch string, mapped []types.Experiment, existing []string) (Plan, error) {
	switch len(mapped) {
	case 0:
		return planCreate(spec, branch, existing)
	case 1:
		return planUpdate(spec, mapped[0]), nil
	default:
		return Plan{}, conflictMapped(branch, mapped)
	}
}

// Result is the JSON body of an apply response. The fields of the embedded
// [Plan] are flattened into the same object as dryRun.
type Result struct {
	Plan

	DryRun bool `json:"dryRun"`
}

// ConfigResult is the JSON body of a config dry run response. Kind and Name
// are the config's after ${VAR} expansion, and Pending is its ref from
// [PendingRef], which a client passes on unchanged to an apply dry run.
type ConfigResult struct {
	Action  Action `json:"action"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	DryRun  bool   `json:"dryRun"`
	Pending string `json:"pending,omitempty"`
}

// Mapped returns the experiments in exps whose [BranchAnnotation] equals
// branch, in order.
func Mapped(exps []types.Experiment, branch string) []types.Experiment {
	mapped := make([]types.Experiment, 0, len(exps))

	for _, exp := range exps {
		if value, ok := exp.Metadata.Annotations[BranchAnnotation]; ok && value == branch {
			mapped = append(mapped, exp)
		}
	}

	return mapped
}

// CheckExpected compares p with the action a caller expects, normally that of
// an earlier dry run. An empty expect accepts any plan. The error wraps
// [ErrUnknownAction] when expect is not an [Action] and [ErrPlanChanged] when
// the plan's action differs from it.
func CheckExpected(p Plan, expect string) error {
	if expect == "" {
		return nil
	}

	want, err := ParseAction(expect)
	if err != nil {
		return err
	}

	if p.Action != want {
		return fmt.Errorf("%w: expected action %q, but the plan is now %q", ErrPlanChanged, want, p.Action)
	}

	return nil
}

// planCreate plans an apply to a branch with no mapped experiment. It refuses
// an auto.create that experiment.Create or config.Create would, so a dry run
// finds it before anything changes.
func planCreate(spec Spec, branch string, existing []string) (Plan, error) {
	name := spec.ExperimentName()

	switch {
	case name == "":
		return Plan{Action: ActionNone, Experiment: "", Reason: reasonNoCreate}, nil
	case !config.NameRegex.MatchString(name) || strings.EqualFold(name, experiment.ReservedName):
		return Plan{}, fmt.Errorf("%w: auto.create %q is not a valid experiment name", ErrInvalidSpec, name)
	case slices.Contains(existing, name):
		return Plan{}, fmt.Errorf(
			"%w: experiment %q already exists and is not mapped to workflow branch %q",
			ErrConflict, name, branch,
		)
	case spec.AutoRestart():
		return Plan{Action: ActionCreateAndStart, Experiment: name, Reason: ""}, nil
	default:
		return Plan{Action: ActionCreate, Experiment: name, Reason: ""}, nil
	}
}

// planUpdate plans an apply to a branch with exactly one mapped experiment.
func planUpdate(spec Spec, exp types.Experiment) Plan {
	name := exp.Metadata.Name

	switch {
	case !spec.AutoUpdate():
		return Plan{Action: ActionNone, Experiment: name, Reason: reasonNoUpdate}
	case exp.Running() && !spec.AutoRestart():
		return Plan{Action: ActionNone, Experiment: name, Reason: reasonNoRestart}
	case exp.Running():
		return Plan{Action: ActionRestart, Experiment: name, Reason: ""}
	case spec.AutoRestart():
		return Plan{Action: ActionUpdateAndStart, Experiment: name, Reason: ""}
	default:
		return Plan{Action: ActionUpdate, Experiment: name, Reason: ""}
	}
}

// conflictMapped describes a branch mapped to more than one experiment. Names
// are sorted because list order follows store key order.
func conflictMapped(branch string, mapped []types.Experiment) error {
	names := make([]string, 0, len(mapped))

	for _, exp := range mapped {
		names = append(names, exp.Metadata.Name)
	}

	slices.Sort(names)

	return fmt.Errorf(
		"%w: %d experiments are mapped to workflow branch %q (%s); at most one is allowed",
		ErrConflict, len(names), branch, strings.Join(names, ", "),
	)
}
