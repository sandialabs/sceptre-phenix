package scorchmd

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"phenix/store"
	"phenix/types"
	"phenix/util/tap"
)

const (
	StateStarting    = "starting"
	StateRunning     = "running"
	StateWaiting     = "waiting"
	StateCanceling   = "canceling"
	StateFinalizing  = "finalizing"
	StateSucceeded   = "succeeded"
	StateFailed      = "failed"
	StateCanceled    = "canceled"
	StateInterrupted = "interrupted"
)

var ErrStaleExecution = errors.New("scorch execution is no longer current")

// Execution identifies one invocation of a configured run. Terminal outcomes
// remain available after the invocation has released its resources.
type Execution struct {
	Resources       []string            `json:"resources"       mapstructure:"resources"`
	ID              string              `json:"executionID"     mapstructure:"executionID"`
	Run             int                 `json:"runID"           mapstructure:"runID"`
	Name            string              `json:"name"            mapstructure:"name"`
	State           string              `json:"state"           mapstructure:"state"`
	Owner           string              `json:"owner"           mapstructure:"owner"`
	PID             int                 `json:"pid"             mapstructure:"pid"`
	Started         string              `json:"started"         mapstructure:"started"`
	Updated         string              `json:"updated"         mapstructure:"updated"`
	Revision        uint64              `json:"revision"        mapstructure:"revision"`
	Stage           string              `json:"stage"           mapstructure:"stage"`
	Component       string              `json:"component"       mapstructure:"component"`
	Loop            int                 `json:"loop"            mapstructure:"loop"`
	Count           int                 `json:"count"           mapstructure:"count"`
	Wait            string              `json:"wait"            mapstructure:"wait"`
	Deadline        string              `json:"deadline"        mapstructure:"deadline"`
	Controller      string              `json:"controller"      mapstructure:"controller"`
	CancelRequested bool                `json:"cancelRequested" mapstructure:"cancelRequested"`
	Error           string              `json:"error"           mapstructure:"error"`
	Taps            map[string]*tap.Tap `json:"taps"            mapstructure:"taps"`
}

func (e Execution) Active() bool {
	switch e.State {
	case StateStarting, StateRunning, StateWaiting, StateCanceling, StateFinalizing:
		return true
	default:
		return false
	}
}

func (s ScorchStatus) ActiveRuns() []int {
	runs := make([]int, 0)
	for _, e := range s.Executions {
		if e.Active() {
			runs = append(runs, e.Run)
		}
	}
	sort.Ints(runs)
	return runs
}

func Status(exp *types.Experiment) (ScorchStatus, error) {
	s := ScorchStatus{Stopping: false,
		RunID:      -1,
		Taps:       make(map[string]*tap.Tap),
		Executions: make(map[string]*Execution),
	}
	if _, exists := exp.Status.AppStatus()["scorch"]; exists {
		if err := exp.Status.ParseAppStatus("scorch", &s); err != nil {
			return s, err
		}
	}
	if s.Taps == nil {
		s.Taps = make(map[string]*tap.Tap)
	}
	if s.Executions == nil {
		s.Executions = make(map[string]*Execution)
	}
	return s, nil
}

// Mutate changes only Scorch's state, preserving all unrelated experiment data.
func Mutate(name string, change func(*types.Experiment, *ScorchStatus) error) error {
	c, err := store.NewConfig("experiment/" + name)
	if err != nil {
		return err
	}
	return store.Mutate(c, func(current *store.Config) error {
		exp, err := types.DecodeExperimentFromConfig(*current)
		if err != nil {
			return err
		}
		s, err := Status(exp)
		if err != nil {
			return err
		}
		if err := change(exp, &s); err != nil {
			return err
		}
		runs := s.ActiveRuns()
		s.RunID = -1
		if len(runs) == 1 {
			s.RunID = runs[0]
		}
		// JSON tags define the public execution contract, independent of structs' case conversion.
		body, err := json.Marshal(map[string]any{"runID": s.RunID, "taps": s.Taps, "executions": s.Executions, "stopping": s.Stopping})
		if err != nil {
			return err
		}
		var state map[string]any
		if err := json.Unmarshal(body, &state); err != nil {
			return err
		}
		state["runID"] = s.RunID
		delete(state, "RunID")
		state["taps"] = s.Taps
		delete(state, "Taps")
		apps, _ := current.Status["apps"].(map[string]any)
		if apps == nil {
			apps = make(map[string]any)
			current.Status["apps"] = apps
		}
		apps["scorch"] = state
		running, _ := current.Status["appRunningStageStatus"].(map[string]any)
		if running == nil {
			running = make(map[string]any)
			current.Status["appRunningStageStatus"] = running
		}
		running["scorch"] = len(runs) > 0
		return nil
	})
}

func UpdateExecution(name string, run int, id string, change func(*Execution) error) error {
	return Mutate(name, func(_ *types.Experiment, s *ScorchStatus) error {
		e := s.Executions[strconv.Itoa(run)]
		if e == nil || e.ID != id {
			return ErrStaleExecution
		}
		return change(e)
	})
}
