package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/disk"
	"phenix/app"
	"phenix/store"
	bdoc "phenix/types/builder"
	"phenix/util/mm"
	"phenix/util/plog"
	"phenix/web/weberror"
)

const (
	// builderPreflightMaxBytes bounds the body of a preflight request, which
	// holds the names of the checks and of an experiment.
	builderPreflightMaxBytes = 4096

	// builderExperimentVLANs is the key of an experiment's spec that holds
	// its VLAN range, and of its status that holds the VLANs it was given.
	builderExperimentVLANs = "vlans"
)

// builderPreflightRequest asks for preflight checks of a draft's current
// document (see [bapi.RunPreflight]).
type builderPreflightRequest struct {
	// Checks names the checks to make, in the order they are reported: at
	// least one, each one of capacity, network, disks and apps, none twice.
	Checks []string `json:"checks"`
	// Experiment names the experiment whose VLAN range and default bridge
	// the network check goes by; empty for none.
	Experiment string `json:"experiment"`
}

// builderPreflightSources is where the preflight checks read the cluster and
// the server from: minimega's schedulable hosts and their bridges, the disk
// images, and the apps. Tests replace them, so no test needs minimega.
type builderPreflightSources struct {
	clusterHosts func() (mm.Hosts, error)
	bridges      func(hosts ...string) (map[string][]string, error)
	diskImages   func() ([]disk.Details, error)
	// defaultApps are the apps every experiment runs; apps are the others
	// the server can run, built in or on its PATH.
	defaultApps func() []string
	apps        func() []string
	// timeout bounds each check.
	timeout time.Duration
}

// defaultBuilderPreflightSources reads the cluster through minimega, the
// disk images as GET /disks does and the apps as GET /applications does.
func defaultBuilderPreflightSources() builderPreflightSources {
	return builderPreflightSources{
		clusterHosts: func() (mm.Hosts, error) { return mm.GetClusterHosts(true) },
		bridges:      mm.GetBridges,
		diskImages:   func() ([]disk.Details, error) { return disk.GetImages("") },
		defaultApps:  app.DefaultApps,
		apps:         app.List,
		timeout:      bapi.PreflightTimeout,
	}
}

// withBuilderPreflightSources sets where the preflight checks read the
// cluster and the server from.
func withBuilderPreflightSources(sources builderPreflightSources) builderOption {
	return func(api *builderAPI) { api.preflight = sources }
}

// preflightDraft - POST /builder/drafts/{owner}/{draft}/preflight.
//
// Puts the draft's current document through the preflight checks the request
// names (see [bapi.RunPreflight]), for whoever may open the draft. Each check
// reads only what the caller's role allows, as the routes that list the same
// things allow it, and reports as unavailable, with the reason, what the role
// does not allow or what cannot be reached. Nothing is written and nothing is
// started.
func (b *builderAPI) preflightDraft(w http.ResponseWriter, r *http.Request) error {
	plog.Debug(plog.TypeSystem, "HTTP handler called", "handler", "BuilderPreflightDraft")

	actor, meta, _, err := b.readDraft(r, "running the preflight checks of a builder draft")
	if err != nil {
		return err
	}

	var request builderPreflightRequest

	if err := builderDecodeLimit(w, r, &request, builderPreflightMaxBytes); err != nil {
		return err
	}

	checks, err := builderPreflightChecks(request.Checks)
	if err != nil {
		return err
	}

	if request.Experiment != "" && !validPublishName(builderSourceExperiment, request.Experiment) {
		return builderPreflightInvalid("the experiment name is not a config name")
	}

	snapshot, err := b.drafts.GetCurrentDocument(r.Context(), meta.ID)
	if err != nil {
		return builderWebError(err, "unable to get the current document of builder draft %s", meta.ID)
	}

	document, err := bapi.ParseDocument(snapshot.Data)
	if err != nil {
		return builderWebError(err, "unable to read the current document of builder draft %s", meta.ID)
	}

	report := bapi.RunPreflight(r.Context(), document, bapi.PreflightRequest{
		Checks: checks, Experiment: request.Experiment, Timeout: b.preflight.timeout,
	}, newBuilderPreflightEnvironment(b, actor))

	return builderWriteJSON(w, http.StatusOK, "", report)
}

// builderPreflightChecks reads the checks a preflight request names: at
// least one, each known, none twice.
func builderPreflightChecks(names []string) ([]bapi.PreflightCheck, error) {
	if len(names) == 0 {
		return nil, builderPreflightInvalid("name at least one preflight check: capacity, network, disks or apps")
	}

	checks := make([]bapi.PreflightCheck, 0, len(names))

	for _, name := range names {
		check, ok := bapi.ParsePreflightCheck(name)

		switch {
		case !ok:
			return nil, builderPreflightInvalid("a preflight check is unknown; the checks are capacity, network, disks and apps")
		case slices.Contains(checks, check):
			return nil, builderPreflightInvalid(fmt.Sprintf("preflight check %s is named twice", check))
		}

		checks = append(checks, check)
	}

	return checks, nil
}

// builderPreflightInvalid is the 400 of a malformed preflight request.
func builderPreflightInvalid(message string) error {
	return weberror.NewWebError(nil, "%s", message).
		SetStatus(http.StatusBadRequest).WithCode(string(bdoc.CodeRequestInvalid))
}

// builderPreflightEnvironment is what the preflight checks of one request
// read, as its caller may read it (see [bapi.PreflightEnvironment]). The
// cluster hosts are read at most once, for both checks that need them.
type builderPreflightEnvironment struct {
	api   *builderAPI
	actor builderActor
	hosts func() (mm.Hosts, error)
}

// newBuilderPreflightEnvironment returns the environment of a request of
// actor.
func newBuilderPreflightEnvironment(api *builderAPI, actor builderActor) *builderPreflightEnvironment {
	return &builderPreflightEnvironment{
		api: api, actor: actor, hosts: sync.OnceValues(api.preflight.clusterHosts),
	}
}

// ClusterHosts returns the schedulable hosts the caller may list.
func (e *builderPreflightEnvironment) ClusterHosts(context.Context) ([]bapi.PreflightHost, error) {
	hosts, err := e.schedulableHosts()
	if err != nil {
		return nil, err
	}

	listed := make([]bapi.PreflightHost, 0, len(hosts))

	for _, host := range hosts {
		listed = append(listed, bapi.PreflightHost{
			Name: host.Name, CPUs: host.CPUs, CPUCommit: host.CPUCommit, MemTotal: host.MemTotal, MemCommit: host.MemCommit,
		})
	}

	return listed, nil
}

// Bridges returns the bridges of each schedulable host the caller may list.
// Bridges are a facility of the hosts, so listing them takes what listing
// the hosts takes.
func (e *builderPreflightEnvironment) Bridges(context.Context) (map[string][]string, error) {
	hosts, err := e.schedulableHosts()
	if err != nil {
		return nil, err
	}

	if len(hosts) == 0 {
		return map[string][]string{}, nil
	}

	names := make([]string, 0, len(hosts))
	for _, host := range hosts {
		names = append(names, host.Name)
	}

	return e.api.preflight.bridges(names...)
}

// schedulableHosts returns the hosts VMs can be scheduled on that the caller
// may list, as GET /hosts lists hosts: with the hosts list permission, and
// that permission for each host by name.
func (e *builderPreflightEnvironment) schedulableHosts() (mm.Hosts, error) {
	if !e.actor.role.Allowed("hosts", "list") {
		return nil, bapi.NewPreflightUnavailable("your role may not list the cluster hosts")
	}

	hosts, err := e.hosts()
	if err != nil {
		return nil, err
	}

	visible := make(mm.Hosts, 0, len(hosts))

	for _, host := range hosts {
		if host.Schedulable && e.actor.role.Allowed("hosts", "list", host.Name) {
			visible = append(visible, host)
		}
	}

	return visible, nil
}

// DiskImages returns the disk images the caller may list, as GET /disks
// lists them: with the disks list permission, and that permission for each
// image by name.
func (e *builderPreflightEnvironment) DiskImages(context.Context) ([]bapi.PreflightImage, error) {
	if !e.actor.role.Allowed("disks", "list") {
		return nil, bapi.NewPreflightUnavailable("your role may not list the disk images")
	}

	images, err := e.api.preflight.diskImages()
	if err != nil {
		return nil, err
	}

	listed := make([]bapi.PreflightImage, 0, len(images))

	for _, image := range images {
		if e.actor.role.Allowed("disks", "list", image.Name) {
			listed = append(listed, bapi.PreflightImage{Name: image.Name, Kind: image.Kind.String()})
		}
	}

	return listed, nil
}

// Apps returns the default apps, which every experiment runs, and the other
// apps the caller may list, as GET /applications lists them: with the
// applications list permission, and that permission for each app by name.
func (e *builderPreflightEnvironment) Apps(context.Context) ([]string, error) {
	if !e.actor.role.Allowed("applications", "list") {
		return nil, bapi.NewPreflightUnavailable("your role may not list the apps")
	}

	available := slices.Clone(e.api.preflight.defaultApps())

	for _, name := range e.api.preflight.apps() {
		if e.actor.role.Allowed("applications", "list", name) {
			available = append(available, name)
		}
	}

	return available, nil
}

// ScenarioApps returns the apps a Scenario config runs, read as publishing
// reads the scenarios a document lists: with the configs get and the
// scenarios list permissions. A scenario the caller may not read is reported
// as such without being looked up, so whether it exists is not disclosed.
func (e *builderPreflightEnvironment) ScenarioApps(_ context.Context, scenario string) ([]string, error) {
	full := store.ConfigFullName(builderKindScenario, scenario)
	if full == "" {
		return nil, fmt.Errorf("scenario %s: %w", scenario, bapi.ErrNotFound)
	}

	if !builderBaseAllowed(e.actor.role, builderVerbGet, full) ||
		!builderKindAllowed(e.actor.role, builderScenarios, scenario) {
		return nil, bapi.NewPreflightUnavailable(fmt.Sprintf("your role may not read scenario %s", scenario))
	}

	config, err := e.api.getConfig(full)

	switch {
	case errors.Is(err, store.ErrNotExist):
		return nil, fmt.Errorf("scenario %s: %w", scenario, bapi.ErrNotFound)
	case err != nil:
		return nil, err
	}

	return builderScenarioApps(config), nil
}

// builderScenarioApps returns the names of the apps a Scenario config runs:
// those its spec lists that it does not disable.
func builderScenarioApps(config *store.Config) []string {
	entries, _ := config.Spec["apps"].([]any)
	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		object, _ := entry.(map[string]any)

		if disabled, _ := object["disabled"].(bool); disabled {
			continue
		}

		if name, _ := object["name"].(string); strings.TrimSpace(name) != "" {
			names = append(names, strings.TrimSpace(name))
		}
	}

	return names
}

// Experiment returns the VLAN range and default bridge of an experiment the
// caller may read (the configs get and experiments get permissions). One it
// may not read and one that does not exist are reported alike.
func (e *builderPreflightEnvironment) Experiment(_ context.Context, name string) (bapi.PreflightExperiment, error) {
	hidden := bapi.NewPreflightUnavailable(fmt.Sprintf("experiment %s does not exist, or your role may not read it", name))

	full := store.ConfigFullName(kindExperiment, name)
	if full == "" || !builderBaseAllowed(e.actor.role, builderVerbGet, full) ||
		!builderSourceGetAllowed(e.actor.role, kindExperiment, name) {
		return bapi.PreflightExperiment{}, hidden
	}

	config, err := e.api.getConfig(full)

	switch {
	case errors.Is(err, store.ErrNotExist):
		return bapi.PreflightExperiment{}, hidden
	case err != nil:
		return bapi.PreflightExperiment{}, err
	}

	vlans, _ := config.Spec[builderExperimentVLANs].(map[string]any)
	bridge, _ := config.Spec["defaultBridge"].(string)

	return bapi.PreflightExperiment{
		VLANMin:       builderConfigInt(vlans["min"]),
		VLANMax:       builderConfigInt(vlans["max"]),
		DefaultBridge: strings.TrimSpace(bridge),
	}, nil
}

// VLANsInUse returns the VLANs the running experiments hold, but the one
// named except, as their status records them. Reading them takes the
// experiments list permission; an experiment the caller may not list (the
// configs list and experiments list permissions on its name) is not named.
func (e *builderPreflightEnvironment) VLANsInUse(_ context.Context, except string) ([]bapi.PreflightVLAN, error) {
	if !e.actor.role.Allowed("experiments", "list") {
		return nil, bapi.NewPreflightUnavailable("your role may not list experiments")
	}

	experiments, err := e.api.listConfigs(builderSourceExperiment)
	if err != nil {
		return nil, err
	}

	var used []bapi.PreflightVLAN

	for _, experiment := range experiments {
		name := experiment.Metadata.Name

		started, _ := experiment.Status["startTime"].(string)
		if name == except || started == "" {
			continue
		}

		shown := ""
		if builderKindAllowed(e.actor.role, builderExperiments, name) &&
			builderBaseAllowed(e.actor.role, builderVerbList, experiment.FullName()) {
			shown = name
		}

		vlans, _ := experiment.Status[builderExperimentVLANs].(map[string]any)

		for alias, id := range vlans {
			used = append(used, bapi.PreflightVLAN{ID: builderConfigInt(id), Alias: alias, Experiment: shown})
		}
	}

	return used, nil
}

// builderConfigInt reads a whole number of a config, as JSON (a float) or
// YAML (an integer) decodes it, or 0 for anything else.
func builderConfigInt(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	}

	return 0
}
