package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	bapi "phenix/api/builder"
	"phenix/api/disk"
	"phenix/store"
	bdoc "phenix/types/builder"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/rbac"
)

// preflightDisks lists the one disk image base.qc2, a VM image, as the server's.
func preflightDisks() builderOption {
	return withBuilderDisks(func() ([]disk.Details, error) {
		return []disk.Details{{Name: "base.qc2", Kind: disk.VMImage}}, nil
	})
}

// preflightSources reads schedulable host h1 and head node head, bridge
// phenix, and apps ntp and scorch. hostReads counts the host reads.
func preflightSources(hostReads *atomic.Int32) builderPreflightSources {
	return builderPreflightSources{
		clusterHosts: func() (mm.Hosts, error) {
			hostReads.Add(1)

			return mm.Hosts{
				{Name: "h1", CPUs: 8, MemTotal: 16384, Schedulable: true},
				{Name: "head", CPUs: 2, MemTotal: 2048, Schedulable: false},
			}, nil
		},
		bridges: func(hosts ...string) (map[string][]string, error) {
			bridges := map[string][]string{}
			for _, host := range hosts {
				bridges[host] = []string{"phenix"}
			}

			return bridges, nil
		},
		defaultApps: func() []string { return []string{"ntp"} },
		apps:        func() []string { return []string{"scorch"} },
		timeout:     time.Second,
	}
}

// preflightFixture is a harness with topology lab (device web on VLAN EXP),
// scenario sc and configs, and a draft made from lab that lists sc.
func preflightFixture(t *testing.T, hostReads *atomic.Int32, configs ...store.Config) (*builderHarness, builderDraftResponse) {
	t.Helper()

	topology := builderConfig(t, builderKindTopology, "lab")
	topology.Spec = map[string]any{"nodes": []any{includeNode("web")}}

	scenario := builderConfig(t, builderKindScenario, "sc")
	scenario.Spec = map[string]any{"apps": []any{
		map[string]any{"name": "scorch"},
		map[string]any{"name": "gone", "disabled": true},
	}}

	harness := newBuilderHarnessWith(t,
		[]builderOption{withBuilderPreflightSources(preflightSources(hostReads)), preflightDisks()},
		slices.Concat([]store.Config{topology, scenario}, configs)...,
	)

	document := generateBuilderDocument(t, harness, "Topology/lab")
	document.Scenarios = []string{"sc"}

	return harness, createBuilderPublishDraft(t, harness, document)
}

// postBuilderPreflight asks for the preflight checks of draft as alice, with
// role (nil for full access).
func postBuilderPreflight(
	harness *builderHarness,
	draft builderDraftResponse,
	role *rbac.Role,
	body string,
) *httptest.ResponseRecorder {
	return harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.ID + "/preflight",
		body:   body,
		user:   builderAlice,
		role:   role,
	})
}

// preflightReport decodes a successful preflight answer.
func preflightReport(t *testing.T, recorder *httptest.ResponseRecorder) bapi.PreflightReport {
	t.Helper()

	if err := builderStatusError(recorder, http.StatusOK); err != nil {
		t.Fatalf("POST preflight: %v", err)
	}

	var report bapi.PreflightReport
	decodeBuilderAnswer(t, recorder, &report)

	return report
}

// preflightResultOf returns the result of check in report.
func preflightResultOf(t *testing.T, report bapi.PreflightReport, check bapi.PreflightCheck) bapi.PreflightResult {
	t.Helper()

	for _, result := range report.Checks {
		if result.Name == check {
			return result
		}
	}

	t.Fatalf("report %+v has no %s check", report, check)

	return bapi.PreflightResult{}
}

// TestBuilderPreflightReportsEachCheck runs every check in the order asked,
// reading the cluster hosts once and writing nothing.
func TestBuilderPreflightReportsEachCheck(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	drafts := harness.store.Count(bapi.NamespaceDrafts)
	published := harness.store.Count(bapi.NamespacePublished)

	report := preflightReport(t, postBuilderPreflight(harness, draft, nil,
		`{"checks":["disks","apps","capacity","network"]}`))

	names := make([]bapi.PreflightCheck, len(report.Checks))
	for i, result := range report.Checks {
		names[i] = result.Name
	}

	want := []bapi.PreflightCheck{bapi.PreflightDisks, bapi.PreflightApps, bapi.PreflightCapacity, bapi.PreflightNetwork}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("POST preflight: checks mismatch (-want +got):\n%s", diff)
	}

	// Disks failed and the others passed.
	type outcome struct {
		Passed, Failed, Unavailable []bapi.PreflightCheck
	}

	wantOutcome := outcome{Passed: want[1:], Failed: []bapi.PreflightCheck{bapi.PreflightDisks}}
	gotOutcome := outcome{Passed: report.Passed, Failed: report.Failed, Unavailable: report.Unavailable}

	if diff := cmp.Diff(wantOutcome, gotOutcome, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("POST preflight: outcome mismatch (-want +got):\n%s", diff)
	}

	// The image of web missing, located on the node and its field.
	type located struct {
		Code  bdoc.Code
		Node  bool
		Field string
	}

	disks := preflightResultOf(t, report, bapi.PreflightDisks)

	gotDisks := make([]located, 0, len(disks.Issues))
	for _, issue := range disks.Issues {
		gotDisks = append(gotDisks, located{Code: issue.Code, Node: issue.NodeID != "", Field: issue.Field})
	}

	wantDisks := []located{{Code: bdoc.CodePreflightDiskMissing, Node: true, Field: "spec.hardware.drives.0.image"}}
	if diff := cmp.Diff(wantDisks, gotDisks); diff != "" {
		t.Errorf("POST preflight: disks issues mismatch (-want +got):\n%s", diff)
	}

	if capacity := preflightResultOf(t, report, bapi.PreflightCapacity); !strings.Contains(capacity.Summary, "on 1 schedulable host") {
		t.Errorf("POST preflight: capacity summary = %q, want the one schedulable host counted", capacity.Summary)
	}

	if got := hostReads.Load(); got != 1 {
		t.Errorf("POST preflight read the cluster hosts %d times, want once", got)
	}

	// A preflight writes nothing.
	type writes struct {
		Configs, Experiments, Drafts, Published int
	}

	wantWrites := writes{Drafts: drafts, Published: published}
	gotWrites := writes{
		Configs: harness.configWrites, Experiments: harness.experimentWrites,
		Drafts: harness.store.Count(bapi.NamespaceDrafts), Published: harness.store.Count(bapi.NamespacePublished),
	}

	if diff := cmp.Diff(wantWrites, gotWrites); diff != "" {
		t.Errorf("POST preflight: writes mismatch (-want +got):\n%s", diff)
	}
}

// TestBuilderPreflightRoutePermissions asserts the checks take configs get,
// and run on any user's draft.
func TestBuilderPreflightRoutePermissions(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)
	capacity := builderRequest{
		method: http.MethodPost, path: "/builder/drafts/" + draft.ID + "/preflight", body: `{"checks":["capacity"]}`,
	}

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{
			name: "without configs get", user: builderAlice, role: builderRole(preflightLister()),
			request: capacity, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{name: "a draft of another user", user: builderBob, role: builderUserRole(), request: capacity, status: http.StatusOK},
		{
			name: "a draft that does not exist", user: builderAlice,
			request: builderRequest{method: http.MethodPost, path: "/builder/drafts/id-missing/preflight", body: `{}`},
			status:  http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
	})
}

// TestBuilderPreflightFollowsRBAC reports as unavailable, with the reason,
// each check whose source the role may not list.
func TestBuilderPreflightFollowsRBAC(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	reader := builderRole(builderPolicy(
		[]string{"configs", "scenarios", "topologies", "experiments"}, []string{"*", "*/*"}, []string{"list", "get"},
	))

	report := preflightReport(t, postBuilderPreflight(harness, draft, reader,
		`{"checks":["capacity","network","disks","apps"]}`))

	if diff := cmp.Diff(bapi.PreflightChecks(), report.Unavailable); diff != "" {
		t.Errorf("POST preflight as a reader: unavailable checks mismatch (-want +got):\n%s", diff)
	}

	for check, reason := range map[bapi.PreflightCheck]string{
		bapi.PreflightCapacity: "your role may not list the cluster hosts",
		bapi.PreflightNetwork:  "your role may not list the cluster hosts",
		bapi.PreflightDisks:    "your role may not list the disk images",
		bapi.PreflightApps:     "your role may not list the apps",
	} {
		t.Run(string(check), func(t *testing.T) {
			result := preflightResultOf(t, report, check)

			if !slices.ContainsFunc(result.Issues, func(issue bdoc.Issue) bool {
				return issue.Code == bdoc.CodePreflightUnavailable && strings.HasPrefix(issue.Message, reason)
			}) {
				t.Errorf("POST preflight as a reader: %s issues = %+v, want one of the code %s starting %q",
					check, result.Issues, bdoc.CodePreflightUnavailable, reason)
			}

			if !strings.Contains(strings.ToLower(result.Summary), reason) {
				t.Errorf("POST preflight as a reader: %s summary = %q, want it to say %q", check, result.Summary, reason)
			}
		})
	}

	if got := hostReads.Load(); got != 0 {
		t.Errorf("POST preflight read the cluster hosts %d times for a role that may not list them, want 0", got)
	}
}

// preflightIssue returns the issue of result with code, or an error unless
// there is exactly one.
func preflightIssue(result bapi.PreflightResult, code bdoc.Code) (bdoc.Issue, error) {
	var found []bdoc.Issue

	for _, issue := range result.Issues {
		if issue.Code == code {
			found = append(found, issue)
		}
	}

	if len(found) != 1 {
		return bdoc.Issue{}, fmt.Errorf("%s = %+v, want exactly one %s issue", result.Name, result, code)
	}

	return found[0], nil
}

// preflightLister may list hosts, disks and apps, so a check is never
// unavailable for lacking them.
func preflightLister() *v1.PolicySpec {
	return builderPolicy([]string{"hosts", "disks", "applications"}, []string{"*"}, []string{"list"})
}

// TestBuilderPreflightAppsNeedScenarioRead reports the apps check unavailable
// for a scenario the role may not read.
func TestBuilderPreflightAppsNeedScenarioRead(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	for name, role := range map[string]*rbac.Role{
		"without scenarios list": builderRole(
			preflightLister(),
			builderPolicy([]string{"configs", "topologies"}, []string{"*", "*/*"}, []string{"list", "get"}),
		),
		"without configs get on the scenario": builderRole(
			preflightLister(),
			builderPolicy([]string{"configs"}, []string{"*", "*/*", "!Scenario/*"}, []string{"list", "get"}),
			builderPolicy([]string{"scenarios", "topologies"}, []string{"*"}, []string{"list", "get"}),
		),
	} {
		t.Run(name, func(t *testing.T) {
			report := preflightReport(t, postBuilderPreflight(harness, draft, role,
				`{"checks":["apps"]}`))

			apps := preflightResultOf(t, report, bapi.PreflightApps)
			if apps.Status != bapi.PreflightUnavailable {
				t.Errorf("POST preflight of apps: status = %q, want %q", apps.Status, bapi.PreflightUnavailable)
			}

			issue, err := preflightIssue(apps, bdoc.CodePreflightScenarioUnread)
			if err != nil {
				t.Fatalf("POST preflight of apps: %v", err)
			}

			if want := "your role may not read scenario sc"; issue.Message != want {
				t.Errorf("POST preflight of apps: message = %q, want %q", issue.Message, want)
			}
		})
	}
}

// TestBuilderPreflightAppsScenarioMissing fails the apps check for a listed
// scenario that does not exist.
func TestBuilderPreflightAppsScenarioMissing(t *testing.T) {
	var hostReads atomic.Int32

	harness, _ := preflightFixture(t, &hostReads)

	document := generateBuilderDocument(t, harness, "Topology/lab")
	document.Scenarios = []string{"sc", "gone"}
	draft := createBuilderPublishDraft(t, harness, document)

	apps := preflightResultOf(t, preflightReport(t, postBuilderPreflight(harness, draft, nil,
		`{"checks":["apps"]}`)), bapi.PreflightApps)

	if apps.Status != bapi.PreflightFailed {
		t.Errorf("POST preflight of apps with scenario gone: status = %q, want %q", apps.Status, bapi.PreflightFailed)
	}

	issue, err := preflightIssue(apps, bdoc.CodePreflightScenarioMissing)
	if err != nil {
		t.Fatalf("POST preflight of apps with scenario gone: %v", err)
	}

	if issue.Path != "scenarios[1]" {
		t.Errorf("POST preflight of apps with scenario gone: path = %q, want %q", issue.Path, "scenarios[1]")
	}
}

// TestBuilderPreflightExperimentNeedsRead leaves an experiment the role may
// not read unchecked, saying the same as for one that does not exist.
func TestBuilderPreflightExperimentNeedsRead(t *testing.T) {
	named := builderConfig(t, kindExperiment, "exp")
	named.Spec = map[string]any{"vlans": map[string]any{"min": 100, "max": 200}}

	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads, named)

	for _, test := range []struct {
		name       string
		role       *rbac.Role
		experiment string
	}{
		{
			name: "without experiments get",
			role: builderRole(
				preflightLister(),
				builderPolicy([]string{"configs"}, []string{"*", "*/*"}, []string{"list", "get"}),
				builderPolicy([]string{"experiments"}, []string{"*"}, []string{"list"}),
			),
			experiment: "exp",
		},
		{
			name: "without configs get on the experiment",
			role: builderRole(
				preflightLister(),
				builderPolicy([]string{"configs"}, []string{"*", "*/*", "!Experiment/*"}, []string{"list", "get"}),
				builderPolicy([]string{"experiments"}, []string{"*"}, []string{"list", "get"}),
			),
			experiment: "exp",
		},
		{name: "an experiment that does not exist", role: builderFullRole(), experiment: "nope"},
	} {
		t.Run(test.name, func(t *testing.T) {
			network := preflightResultOf(t, preflightReport(t, postBuilderPreflight(harness, draft,
				test.role, `{"checks":["network"],"experiment":"`+test.experiment+`"}`)), bapi.PreflightNetwork)

			reason := "experiment " + test.experiment + " does not exist, or your role may not read it. " +
				"The check did not compare the VLAN range and default bridge of the experiment"

			if network.Status != bapi.PreflightUnavailable {
				t.Errorf(
					"POST preflight of network for %s: status = %q, want %q",
					test.experiment,
					network.Status,
					bapi.PreflightUnavailable,
				)
			}

			if strings.Contains(network.Summary, "100 to 200") {
				t.Errorf("POST preflight of network for %s: summary = %q, want no VLAN range", test.experiment, network.Summary)
			}

			issue, err := preflightIssue(network, bdoc.CodePreflightUnavailable)
			if err != nil {
				t.Fatalf("POST preflight of network for %s: %v", test.experiment, err)
			}

			if issue.Message != reason {
				t.Errorf("POST preflight of network for %s: message = %q, want %q", test.experiment, issue.Message, reason)
			}
		})
	}
}

// TestBuilderPreflightAliasesNeedExperimentsList leaves the VLAN aliases
// unchecked for a role without experiments list, and still checks the bridges.
func TestBuilderPreflightAliasesNeedExperimentsList(t *testing.T) {
	running := builderConfig(t, kindExperiment, "other")
	running.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "vlans": map[string]any{"X": float64(150)}}

	var hostReads atomic.Int32

	harness, _ := preflightFixture(t, &hostReads, running)

	document := generateBuilderDocument(t, harness, "Topology/lab")
	alias := 150
	document.Networks[0].Alias = &alias
	draft := createBuilderPublishDraft(t, harness, document)

	role := builderRole(
		preflightLister(),
		builderPolicy([]string{"configs", "topologies", "scenarios"}, []string{"*", "*/*"}, []string{"list", "get"}),
	)

	network := preflightResultOf(t, preflightReport(t, postBuilderPreflight(harness, draft,
		role, `{"checks":["network"]}`)), bapi.PreflightNetwork)

	reason := "your role may not list experiments. The check did not compare VLAN aliases"

	if network.Status != bapi.PreflightUnavailable {
		t.Errorf("POST preflight of network without experiments list: status = %q, want %q", network.Status, bapi.PreflightUnavailable)
	}

	// Unavailable only for the reason.
	wantIssues := []bdoc.Issue{{Code: bdoc.CodePreflightUnavailable, Message: reason}}
	if diff := cmp.Diff(wantIssues, network.Issues, cmpopts.IgnoreFields(bdoc.Issue{}, "Severity")); diff != "" {
		t.Errorf("POST preflight of network without experiments list: issues mismatch (-want +got):\n%s", diff)
	}

	if !strings.Contains(network.Summary, "The check compared 1 bridge with the bridges of 1 host") {
		t.Errorf("POST preflight of network without experiments list: summary = %q, want the bridges checked", network.Summary)
	}
}

// TestBuilderPreflightRefusesBadRequests refuses malformed requests with
// request.invalid, before reading the cluster.
func TestBuilderPreflightRefusesBadRequests(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	for _, tt := range []struct{ name, body string }{
		{name: "no checks", body: `{}`},
		{name: "an empty list of checks", body: `{"checks":[]}`},
		{name: "an unknown check", body: `{"checks":["everything"]}`},
		{name: "a check twice", body: `{"checks":["apps","apps"]}`},
		{name: "an unknown field", body: `{"checks":["apps"],"more":true}`},
		{name: "an experiment name with a space", body: `{"checks":["network"],"experiment":"two words"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := postBuilderPreflight(harness, draft, nil, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Errorf("POST preflight %s: status = %d, want %d", tt.body, recorder.Code, http.StatusBadRequest)
			}

			if code := builderRefusal(t, recorder).Code; code != string(bdoc.CodeRequestInvalid) {
				t.Errorf("POST preflight %s: code = %q, want %q", tt.body, code, bdoc.CodeRequestInvalid)
			}
		})
	}

	if got := hostReads.Load(); got != 0 {
		t.Errorf("the refused requests read the cluster hosts %d times, want 0", got)
	}
}

// TestBuilderPreflightNetworkReadsExperiments checks the network against the
// named experiment's range and bridge and the VLANs of running experiments.
func TestBuilderPreflightNetworkReadsExperiments(t *testing.T) {
	named := builderConfig(t, kindExperiment, "exp")
	named.Spec = map[string]any{"vlans": map[string]any{"min": 100, "max": 200}, "defaultBridge": "lab"}
	named.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "vlans": map[string]any{"EXP": float64(150)}}

	other := builderConfig(t, kindExperiment, "other")
	other.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "vlans": map[string]any{"X": float64(150)}}

	stopped := builderConfig(t, kindExperiment, "stopped")
	stopped.Status = map[string]any{"vlans": map[string]any{"Y": float64(150)}}

	var hostReads atomic.Int32

	harness, _ := preflightFixture(t, &hostReads, named, other, stopped)

	document := generateBuilderDocument(t, harness, "Topology/lab")
	alias := 150
	document.Networks[0].Alias = &alias
	draft := createBuilderPublishDraft(t, harness, document)

	result := preflightResultOf(t, preflightReport(t, postBuilderPreflight(harness, draft, nil,
		`{"checks":["network"],"experiment":"exp"}`)), bapi.PreflightNetwork)

	codes := make([]bdoc.Code, len(result.Issues))
	for i, issue := range result.Issues {
		codes[i] = issue.Code
	}

	if result.Status != bapi.PreflightFailed {
		t.Errorf("POST preflight of network for exp: status = %q, want %q", result.Status, bapi.PreflightFailed)
	}

	want := []bdoc.Code{bdoc.CodePreflightNetworkAliasInUse, bdoc.CodePreflightNetworkBridge}
	if diff := cmp.Diff(want, codes); diff != "" {
		t.Errorf("POST preflight of network for exp: issue codes mismatch (-want +got):\n%s", diff)
	}

	// The alias is held by other.
	if len(result.Issues) > 0 && !strings.Contains(result.Issues[0].Message, "running experiment other") {
		t.Errorf("POST preflight of network for exp: first message = %q, want it to name running experiment other",
			result.Issues[0].Message)
	}

	if !strings.Contains(result.Summary, "The VLAN range of experiment exp is 100 to 200") {
		t.Errorf("POST preflight of network for exp: summary = %q, want the range of exp", result.Summary)
	}
}

// TestBuilderPreflightAliasesLeaveOutHiddenExperiments compares the VLAN
// aliases only with the running experiments the role may list, naming no other.
func TestBuilderPreflightAliasesLeaveOutHiddenExperiments(t *testing.T) {
	mine := builderConfig(t, kindExperiment, "mine")
	mine.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "vlans": map[string]any{"X": float64(150)}}

	secret := builderConfig(t, kindExperiment, "secret")
	secret.Status = map[string]any{"startTime": "2026-01-01T00:00:00Z", "vlans": map[string]any{"HIDDEN": float64(151)}}

	var hostReads atomic.Int32

	harness, _ := preflightFixture(t, &hostReads, mine, secret)

	document := generateBuilderDocument(t, harness, "Topology/lab")
	alias := 151
	document.Networks[0].Alias = &alias
	draft := createBuilderPublishDraft(t, harness, document)

	scoped := builderRole(
		preflightLister(),
		builderPolicy([]string{"configs", "topologies", "scenarios"}, []string{"*", "*/*"}, []string{"list", "get"}),
		builderPolicy([]string{"experiments"}, []string{"mine"}, []string{"list"}),
	)

	const note = "The check did not compare running experiments that your role may not list"

	for _, tt := range []struct{ name, body string }{
		{name: "no experiment", body: `{"checks":["network"]}`},
		{name: "experiment secret", body: `{"checks":["network"],"experiment":"secret"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := postBuilderPreflight(harness, draft, scoped, tt.body)
			network := preflightResultOf(t, preflightReport(t, recorder), bapi.PreflightNetwork)

			// The alias is not compared with the VLAN of secret.
			if slices.ContainsFunc(network.Issues, func(issue bdoc.Issue) bool {
				return issue.Code == bdoc.CodePreflightNetworkAliasInUse
			}) {
				t.Errorf(
					"POST preflight %s: issues = %+v, want none of the code %s",
					tt.body,
					network.Issues,
					bdoc.CodePreflightNetworkAliasInUse,
				)
			}

			if want := "The check compared 1 VLAN alias with running experiments. " + note; !strings.Contains(network.Summary, want) {
				t.Errorf("POST preflight %s: summary = %q, want it to say %q", tt.body, network.Summary, want)
			}

			if answer := recorder.Body.String(); strings.Contains(answer, "HIDDEN") ||
				strings.Contains(answer, "running experiment secret") {
				t.Errorf("POST preflight %s: the answer names secret or its VLAN: %s", tt.body, answer)
			}
		})
	}

	full := preflightResultOf(t, preflightReport(t, postBuilderPreflight(harness, draft, nil,
		`{"checks":["network"]}`)), bapi.PreflightNetwork)

	if strings.Contains(full.Summary, note) {
		t.Errorf("POST preflight of network for the full role: summary = %q, want no note %q", full.Summary, note)
	}

	issue, err := preflightIssue(full, bdoc.CodePreflightNetworkAliasInUse)
	if err != nil {
		t.Fatalf("POST preflight of network for the full role: %v", err)
	}

	if want := `running experiment secret uses for VLAN "HIDDEN"`; !strings.Contains(issue.Message, want) {
		t.Errorf("POST preflight of network for the full role: message = %q, want it to say %q", issue.Message, want)
	}
}
