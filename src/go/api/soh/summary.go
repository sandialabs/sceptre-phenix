package soh

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/mitchellh/mapstructure"

	"phenix/types"
)

// Keys the soh app keeps in its app status besides `hosts`, `initialized` and
// `packetCapture`.
const (
	statusLastRun         = "lastRun"
	statusLastRunDuration = "lastRunDuration"
	statusHistory         = "history"

	// MaxRunHistory is how many run summaries the app status keeps.
	MaxRunHistory = 20
)

// minimega's state for a running VM, and the reachability check's kind.
const (
	mmRunning = "RUNNING"

	checkReachability = "reachability"
)

// RunSummary summarizes one completed run of the soh checks. The app status
// keeps the last MaxRunHistory of them under `history`, with the mapstructure
// (camelCase) keys.
type RunSummary struct {
	Time         string  `json:"time"          mapstructure:"time"`
	Duration     float64 `json:"duration"      mapstructure:"duration"`
	Total        int     `json:"total"         mapstructure:"total"`
	Failing      int     `json:"failing"       mapstructure:"failing"`
	HostsFailing int     `json:"hosts_failing" mapstructure:"hostsFailing"`
	HostsDown    int     `json:"hosts_down"    mapstructure:"hostsDown"`
}

// Tally counts checks: Passing plus Failing is Total.
type Tally struct {
	Total   int `json:"total"`
	Passing int `json:"passing"`
	Failing int `json:"failing"`
}

// VMCounts counts an experiment's VMs by state. For a running experiment
// Running + NotRunning + NotBoot + NotDeploy + External + Delayed is Total;
// Failing counts the running VMs with a failing check. Known is false when
// minimega could not be asked, and a stopped experiment only has a Total.
type VMCounts struct {
	Known      bool `json:"known"`
	Total      int  `json:"total"`
	Running    int  `json:"running"`
	Failing    int  `json:"failing"`
	NotRunning int  `json:"notrunning"`
	NotBoot    int  `json:"notboot"`
	NotDeploy  int  `json:"notdeploy"`
	External   int  `json:"external"`
	Delayed    int  `json:"delayed"`
}

// FailingCheck is one failing check from the latest run.
type FailingCheck struct {
	Host   string `json:"host"`
	Check  string `json:"check"`
	Target string `json:"target"`
	Error  string `json:"error"`
	Time   string `json:"time"`
}

// Summary is one experiment's state of health at a glance. Everything but
// Configured and VMs.Total is only reported for a running experiment.
type Summary struct {
	Name     string  `json:"name"`
	Scenario string  `json:"scenario"`
	Status   string  `json:"status"`
	Percent  float64 `json:"percent"`
	Running  bool    `json:"running"`

	Configured     bool `json:"configured"`
	ExpStarted     bool `json:"started"`
	SOHInitialized bool `json:"soh_initialized"`
	SOHRunning     bool `json:"soh_running"`

	VMs      VMCounts `json:"vms"`
	DownVMs  []string `json:"down_vms"`
	VMsError string   `json:"vms_error,omitempty"`

	Hosts           int   `json:"hosts"`
	HostsWithErrors int   `json:"hosts_with_errors"`
	Checks          Tally `json:"checks"`
	Reachability    Tally `json:"reachability"`

	LastRun         string       `json:"last_run,omitempty"`
	LastRunDuration float64      `json:"last_run_duration,omitempty"`
	History         []RunSummary `json:"history"`

	FailingChecks []FailingCheck `json:"failing_checks"`

	// Error says why the experiment's results could not be read.
	Error string `json:"error,omitempty"`
}

// checkKinds names each HostState check list, in AllStates order.
func checkKinds(h HostState) []struct {
	kind   string
	states []State
} {
	return []struct {
		kind   string
		states []State
	}{
		{"network", h.Networking},
		{checkReachability, h.Reachability},
		{"file", h.Files},
		{"file absent", h.FilesAbsent},
		{"service", h.Services},
		{"process", h.Processes},
		{"listener", h.Listeners},
		{"container", h.Containers},
		{"custom test", h.CustomTests},
	}
}

// Summarize summarizes the experiment's state of health from its app status
// and, for a running experiment, vmStates: the state minimega reports for each
// VM by name, or nil when minimega could not be asked. At most maxFailing
// failing checks and down VMs are listed, newest check first.
func Summarize(exp *types.Experiment, vmStates map[string]string, maxFailing int) (Summary, error) {
	sum := Summary{ //nolint:exhaustruct // filled in below
		Name:          exp.Metadata.Name,
		Scenario:      exp.Metadata.Annotations["scenario"],
		Running:       exp.Running(),
		Configured:    Configured(exp),
		DownVMs:       []string{},
		History:       []RunSummary{},
		FailingChecks: []FailingCheck{},
	}

	var states []*HostState

	if sum.Running {
		sum.ExpStarted = true
		sum.SOHInitialized = Initialized(exp)
		sum.SOHRunning = Running(exp)

		var err error

		if states, err = hostStates(exp); err != nil {
			return sum, err
		}

		if err = sum.addRunStatus(exp, states); err != nil {
			return sum, err
		}
	}

	failing := sum.addChecks(states, maxFailing)
	sum.addVMs(exp, vmStates, failing, maxFailing)

	return sum, nil
}

// hostStates decodes the latest run's per-host results from the app status.
func hostStates(exp *types.Experiment) ([]*HostState, error) {
	app, ok := exp.Status.AppStatus()["soh"]
	if !ok {
		return nil, nil
	}

	data, ok := app.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("experiment %s: unexpected soh app status type %T", exp.Metadata.Name, app)
	}

	var states []*HostState

	if err := mapstructure.Decode(data["hosts"], &states); err != nil {
		return nil, fmt.Errorf("experiment %s: decoding soh host results: %w", exp.Metadata.Name, err)
	}

	return states, nil
}

// addRunStatus sets the last run time, duration and history. Results written
// before runs recorded a time use the newest check's time instead.
func (s *Summary) addRunStatus(exp *types.Experiment, states []*HostState) error {
	status, _ := exp.Status.AppStatus()["soh"].(map[string]any)

	if last, ok := status[statusLastRun].(string); ok {
		s.LastRun = last
	} else {
		s.LastRun = newestCheckTime(states)
	}

	switch d := status[statusLastRunDuration].(type) {
	case float64:
		s.LastRunDuration = d
	case int:
		s.LastRunDuration = float64(d)
	}

	if raw, ok := status[statusHistory]; ok && raw != nil {
		if err := mapstructure.Decode(raw, &s.History); err != nil {
			return fmt.Errorf("experiment %s: decoding soh run history: %w", exp.Metadata.Name, err)
		}
	}

	return nil
}

func newestCheckTime(states []*HostState) string {
	var (
		newest time.Time
		raw    string
	)

	for _, state := range states {
		for _, st := range state.AllStates() {
			if t, err := time.Parse(time.RFC3339, st.Timestamp); err == nil && t.After(newest) {
				newest, raw = t, st.Timestamp
			}
		}
	}

	return raw
}

// addChecks tallies the checks and lists the failing ones, returning the hosts
// with a failing check.
func (s *Summary) addChecks(states []*HostState, maxFailing int) map[string]bool {
	failingHosts := make(map[string]bool)

	var all []FailingCheck

	for _, state := range states {
		s.Hosts++

		for _, kind := range checkKinds(*state) {
			for _, st := range kind.states {
				s.Checks.Total++

				if kind.kind == checkReachability {
					s.Reachability.Total++
				}

				if st.Error == "" {
					continue
				}

				s.Checks.Failing++

				if kind.kind == checkReachability {
					s.Reachability.Failing++
				}

				failingHosts[state.Hostname] = true

				all = append(all, FailingCheck{
					Host:   state.Hostname,
					Check:  kind.kind,
					Target: checkTarget(kind.kind, st.Metadata),
					Error:  st.Error,
					Time:   st.Timestamp,
				})
			}
		}
	}

	s.Checks.Passing = s.Checks.Total - s.Checks.Failing
	s.Reachability.Passing = s.Reachability.Total - s.Reachability.Failing
	s.HostsWithErrors = len(failingHosts)

	// newest first; RFC 3339 times in one zone sort as strings
	slices.SortStableFunc(all, func(a, b FailingCheck) int {
		if c := cmp.Compare(b.Time, a.Time); c != 0 {
			return c
		}

		return cmp.Compare(a.Host, b.Host)
	})

	if len(all) > maxFailing {
		all = all[:maxFailing]
	}

	if all != nil {
		s.FailingChecks = all
	}

	return failingHosts
}

// checkTarget describes what a check looked at, from the metadata the soh app
// records with it.
func checkTarget(kind string, md map[string]any) string {
	str := func(key string) string {
		if v, ok := md[key]; ok && v != nil {
			return fmt.Sprint(v)
		}

		return ""
	}

	if kind == checkReachability {
		target, ip := str("target"), str("ip")

		switch {
		case target != "" && ip != "" && target != ip:
			target = fmt.Sprintf("%s (%s)", target, ip)
		case target == "":
			target = ip
		}

		if port := str("port"); port != "" {
			target = fmt.Sprintf("%s %s/%s", target, str("proto"), port)
		}

		return target
	}

	for _, key := range []string{"target", "path", "service", "container", "proc", "port", "test", "ip"} {
		if v := str(key); v != "" {
			return v
		}
	}

	return ""
}

// addVMs counts the experiment's VMs by state as GetFor classifies them,
// except that a VM waiting for its delayed start counts as Delayed rather than
// not running.
func (s *Summary) addVMs(exp *types.Experiment, vmStates map[string]string, failing map[string]bool, maxDown int) {
	if exp.Spec.Topology() == nil {
		return
	}

	nodes := exp.Spec.Topology().Nodes()
	s.VMs.Total = len(nodes)

	if !s.Running || vmStates == nil {
		return
	}

	s.VMs.Known = true

	for _, node := range nodes {
		name := node.General().Hostname()
		dnb := node.General().DoNotBoot() != nil && *node.General().DoNotBoot()
		state, deployed := vmStates[name]

		switch {
		case node.External():
			s.VMs.External++
		case !deployed && dnb:
			s.VMs.NotBoot++
		case !deployed:
			s.VMs.NotDeploy++
			s.addDown(name, maxDown)
		case state == mmRunning:
			s.VMs.Running++

			if failing[name] {
				s.VMs.Failing++
			}
		case node.Delayed() != "":
			// launched, waiting for its delayed start
			s.VMs.Delayed++
		default:
			s.VMs.NotRunning++
			s.addDown(name, maxDown)
		}
	}
}

func (s *Summary) addDown(name string, maxDown int) {
	if len(s.DownVMs) < maxDown {
		s.DownVMs = append(s.DownVMs, name)
	}
}

// summarizeRun summarizes a finished run's results.
func summarizeRun(status map[string]HostState, finished time.Time, took time.Duration) RunSummary {
	run := RunSummary{ //nolint:exhaustruct // counted below
		Time:     finished.Format(time.RFC3339),
		Duration: took.Round(time.Millisecond).Seconds(),
	}

	for _, host := range status {
		var hostFailing bool

		for _, st := range host.AllStates() {
			run.Total++

			if st.Error != "" {
				run.Failing++
				hostFailing = true
			}
		}

		if hostFailing {
			run.HostsFailing++
		}

		// a host whose networking could not be confirmed (no miniccc, no
		// address) is treated as down
		for _, st := range host.Networking {
			if st.Error != "" {
				run.HostsDown++

				break
			}
		}
	}

	return run
}

// appendRunHistory appends run to the history kept in the app status, keeping
// the newest MaxRunHistory entries. Existing entries are kept as they are, so
// fields this version does not know survive.
func appendRunHistory(existing any, run RunSummary) ([]any, error) {
	var history []any

	switch h := existing.(type) {
	case nil:
	case []any:
		history = append(history, h...)
	case []map[string]any:
		for _, entry := range h {
			history = append(history, entry)
		}
	default:
		return nil, fmt.Errorf("unexpected soh run history type %T", existing)
	}

	history = append(history, map[string]any{
		"time":         run.Time,
		"duration":     run.Duration,
		"total":        run.Total,
		"failing":      run.Failing,
		"hostsFailing": run.HostsFailing,
		"hostsDown":    run.HostsDown,
	})

	if len(history) > MaxRunHistory {
		history = history[len(history)-MaxRunHistory:]
	}

	return history, nil
}
