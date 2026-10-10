package web

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	bapi "phenix/api/builder"
	"phenix/api/disk"
	"phenix/store"
	bdoc "phenix/types/builder"
	v1 "phenix/types/version/v1"
	"phenix/util/mm"
	"phenix/web/rbac"
)

// preflightDisks lists the one disk image base.qc2, a VM image, as the
// server's.
func preflightDisks() builderOption {
	return withBuilderDisks(func() ([]disk.Details, error) {
		return []disk.Details{{Name: "base.qc2", Kind: disk.VMImage}}, nil
	})
}

// preflightSources is what the preflight checks read in these tests: one
// schedulable host h1 (and a head node VMs are not scheduled on) with bridge
// phenix, and the apps ntp (a default app) and scorch. hostReads counts the
// reads of the cluster hosts. The disk images are set with [preflightDisks].
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

// preflightFixture is a harness reading preflightSources and preflightDisks,
// holding topology lab (device web, whose drive image is miniccc.qc2, on
// VLAN EXP), scenario sc (app scorch, and app gone, disabled), and the given
// configs, and a draft of alice's made from lab that lists sc.
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

// postBuilderPreflight asks for the preflight checks of draft as its owner
// alice, with role (nil for full access).
func postBuilderPreflight(
	harness *builderHarness,
	draft builderDraftResponse,
	role *rbac.Role,
	body string,
) *httptest.ResponseRecorder {
	return harness.do(builderRequest{
		method: http.MethodPost,
		path:   "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/preflight",
		body:   body,
		user:   builderTestOwner,
		role:   role,
	})
}

// preflightReport decodes a successful preflight answer.
func preflightReport(t *testing.T, harness *builderHarness, recorder *httptest.ResponseRecorder) bapi.PreflightReport {
	t.Helper()

	if recorder.Code != http.StatusOK {
		t.Fatalf("preflight status = %d: %s", recorder.Code, recorder.Body.String())
	}

	var report bapi.PreflightReport
	harness.decode(recorder, &report)

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

// TestBuilderPreflightReportsEachCheck runs every check on a draft's current
// document, in the order asked, reading the cluster hosts once and writing
// nothing.
func TestBuilderPreflightReportsEachCheck(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	drafts := harness.store.Count(bapi.NamespaceDrafts)
	published := harness.store.Count(bapi.NamespacePublished)

	report := preflightReport(t, harness, postBuilderPreflight(harness, draft, nil,
		`{"checks":["disks","apps","capacity","network"]}`))

	names := make([]bapi.PreflightCheck, len(report.Checks))
	for i, result := range report.Checks {
		names[i] = result.Name
	}

	want := []bapi.PreflightCheck{bapi.PreflightDisks, bapi.PreflightApps, bapi.PreflightCapacity, bapi.PreflightNetwork}
	if !slices.Equal(names, want) {
		t.Fatalf("checks = %v, want %v", names, want)
	}

	if !slices.Equal(report.Failed, []bapi.PreflightCheck{bapi.PreflightDisks}) ||
		!slices.Equal(report.Passed, want[1:]) || len(report.Unavailable) != 0 {
		t.Fatalf("report = %+v, want disks failed and the others passed", report)
	}

	disks := preflightResultOf(t, report, bapi.PreflightDisks)
	if len(disks.Issues) != 1 || disks.Issues[0].Code != bdoc.CodePreflightDiskMissing ||
		disks.Issues[0].NodeID == "" || disks.Issues[0].Field != "spec.hardware.drives.0.image" {
		t.Fatalf("disks = %+v, want web's miniccc.qc2 missing, located", disks)
	}

	if capacity := preflightResultOf(t, report, bapi.PreflightCapacity); !strings.Contains(capacity.Summary, "on 1 schedulable host") {
		t.Fatalf("capacity = %+v, want the one schedulable host counted", capacity)
	}

	if got := hostReads.Load(); got != 1 {
		t.Fatalf("the cluster hosts were read %d times, want once", got)
	}

	if harness.configWrites != 0 || harness.experimentWrites != 0 ||
		harness.store.Count(bapi.NamespaceDrafts) != drafts || harness.store.Count(bapi.NamespacePublished) != published {
		t.Fatal("a preflight wrote something")
	}
}

// TestBuilderPreflightRoutePermissions asks for the preflight checks of a
// draft as callers with and without what reading the draft takes, which is
// what GET of the draft takes: a caller with no identity, or whose role may
// list the sources of the checks but not get configs, is refused; another
// user, with whom the draft is not shared, is answered as if it did not
// exist; and a request naming an unknown check is refused as malformed.
func TestBuilderPreflightRoutePermissions(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	var (
		noConfigs = builderRole(preflightLister())
		path      = "/builder/drafts/" + draft.Owner + "/" + draft.ID + "/preflight"
		capacity  = builderRequest{method: http.MethodPost, path: path, body: `{"checks":["capacity"]}`}
		unknown   = builderRequest{method: http.MethodPost, path: path, body: `{"checks":["everything"]}`}
	)

	runBuilderAccessCases(t, harness, []builderAccessCase{
		{name: "with every permission", user: builderTestOwner, request: capacity, status: http.StatusOK},
		{name: "with no identity", request: capacity, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden},
		{
			name: "without configs get", user: builderTestOwner, role: &noConfigs,
			request: capacity, status: http.StatusForbidden, code: bdoc.CodeRequestForbidden,
		},
		{
			name: "another user's draft", user: builderTestPeer, role: builderConfigsRole(),
			request: capacity, status: http.StatusNotFound, code: bdoc.CodeRequestNotFound,
		},
		{
			name: "an unknown check", user: builderTestOwner,
			request: unknown, status: http.StatusBadRequest, code: bdoc.CodeRequestInvalid,
		},
	})
}

// TestBuilderPreflightFollowsRBAC reports as unavailable each check whose
// source the caller's role may not list, says why, and still makes the
// others.
func TestBuilderPreflightFollowsRBAC(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	reader := builderRole(builderPolicy(
		[]string{"configs", "scenarios", "topologies", "experiments"}, []string{"*", "*/*"}, []string{"list", "get"},
	))

	report := preflightReport(t, harness, postBuilderPreflight(harness, draft, &reader,
		`{"checks":["capacity","network","disks","apps"]}`))

	if !slices.Equal(report.Unavailable, bapi.PreflightChecks()) {
		t.Fatalf("report = %+v, want every check unavailable", report)
	}

	for check, reason := range map[bapi.PreflightCheck]string{
		bapi.PreflightCapacity: "your role may not list the cluster hosts",
		bapi.PreflightNetwork:  "your role may not list the cluster hosts",
		bapi.PreflightDisks:    "your role may not list the disk images",
		bapi.PreflightApps:     "your role may not list the apps",
	} {
		result := preflightResultOf(t, report, check)

		found := slices.ContainsFunc(result.Issues, func(issue bdoc.Issue) bool {
			return issue.Code == bdoc.CodePreflightUnavailable && strings.HasPrefix(issue.Message, reason)
		})
		if !found || !strings.Contains(strings.ToLower(result.Summary), reason) {
			t.Errorf("%s = %+v, want it unavailable because %s", check, result, reason)
		}
	}

	if got := hostReads.Load(); got != 0 {
		t.Fatalf("the cluster hosts were read %d times for a role that may not list them", got)
	}
}

// preflightIssue returns the issue of result with code, failing the test
// unless there is exactly one.
func preflightIssue(t *testing.T, result bapi.PreflightResult, code bdoc.Code) bdoc.Issue {
	t.Helper()

	var found []bdoc.Issue

	for _, issue := range result.Issues {
		if issue.Code == code {
			found = append(found, issue)
		}
	}

	if len(found) != 1 {
		t.Fatalf("%s = %+v, want exactly one %s issue", result.Name, result, code)
	}

	return found[0]
}

// preflightLister may list hosts, disks and apps, so a check is never
// unavailable for lacking them.
func preflightLister() *v1.PolicySpec {
	return builderPolicy([]string{"hosts", "disks", "applications"}, []string{"*"}, []string{"list"})
}

// TestBuilderPreflightAppsNeedScenarioRead reports the apps check unavailable,
// with the reason, for a scenario the role may not read: without scenarios
// list, or without configs get on the Scenario config.
func TestBuilderPreflightAppsNeedScenarioRead(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	for name, role := range map[string]rbac.Role{
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
		report := preflightReport(t, harness, postBuilderPreflight(harness, draft, &role,
			`{"checks":["apps"]}`))

		apps := preflightResultOf(t, report, bapi.PreflightApps)
		issue := preflightIssue(t, apps, bdoc.CodePreflightScenarioUnread)

		if apps.Status != bapi.PreflightUnavailable || issue.Severity != bdoc.SeverityWarning ||
			issue.Message != "your role may not read scenario sc" || issue.Path != "scenarios[0]" ||
			!strings.Contains(apps.Summary, "Not read: 1 scenario.") {
			t.Errorf("%s: apps = %+v, want unavailable because the role may not read scenario sc", name, apps)
		}
	}
}

// TestBuilderPreflightAppsScenarioMissing fails the apps check, with
// preflight.app.scenario-missing, for a listed scenario that does not exist.
func TestBuilderPreflightAppsScenarioMissing(t *testing.T) {
	var hostReads atomic.Int32

	harness, _ := preflightFixture(t, &hostReads)

	document := generateBuilderDocument(t, harness, "Topology/lab")
	document.Scenarios = []string{"sc", "gone"}
	draft := createBuilderPublishDraft(t, harness, document)

	apps := preflightResultOf(t, preflightReport(t, harness, postBuilderPreflight(harness, draft, nil,
		`{"checks":["apps"]}`)), bapi.PreflightApps)

	issue := preflightIssue(t, apps, bdoc.CodePreflightScenarioMissing)

	if apps.Status != bapi.PreflightFailed || len(apps.Issues) != 1 || issue.Severity != bdoc.SeverityError ||
		issue.Path != "scenarios[1]" || !strings.Contains(issue.Message, `scenario "gone" does not exist`) {
		t.Fatalf("apps = %+v, want failed because scenario gone does not exist", apps)
	}
}

// TestBuilderPreflightExperimentNeedsRead leaves the network check's
// experiment part unchecked for an experiment the role may not read (without
// experiments get, or configs get on it), saying the same as for one that
// does not exist.
func TestBuilderPreflightExperimentNeedsRead(t *testing.T) {
	named := builderConfig(t, kindExperiment, "exp")
	named.Spec = map[string]any{"vlans": map[string]any{"min": 100, "max": 200}}

	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads, named)

	for _, test := range []struct {
		name       string
		role       rbac.Role
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
		network := preflightResultOf(t, preflightReport(t, harness, postBuilderPreflight(harness, draft,
			&test.role, `{"checks":["network"],"experiment":"`+test.experiment+`"}`)), bapi.PreflightNetwork)

		reason := "experiment " + test.experiment + " does not exist, or your role may not read it, " +
			"so its VLAN range and default bridge were not checked"

		issue := preflightIssue(t, network, bdoc.CodePreflightUnavailable)

		if network.Status != bapi.PreflightUnavailable || issue.Message != reason ||
			!strings.Contains(network.Summary, "Not checked: "+reason) || strings.Contains(network.Summary, "100 to 200") {
			t.Errorf("%s: network = %+v, want unavailable saying %q", test.name, network, reason)
		}
	}
}

// TestBuilderPreflightAliasesNeedExperimentsList leaves the VLAN aliases
// unchecked, with the reason, for a role without experiments list, while the
// bridges, which the role may list through the hosts, are still checked.
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

	network := preflightResultOf(t, preflightReport(t, harness, postBuilderPreflight(harness, draft,
		&role, `{"checks":["network"]}`)), bapi.PreflightNetwork)

	reason := "your role may not list experiments, so VLAN aliases were not checked"

	if network.Status != bapi.PreflightUnavailable || len(network.Issues) != 1 ||
		network.Issues[0].Code != bdoc.CodePreflightUnavailable || network.Issues[0].Message != reason {
		t.Fatalf("network = %+v, want unavailable only because %s", network, reason)
	}

	if !strings.Contains(network.Summary, "1 bridge compared with the bridges of 1 host") ||
		!strings.Contains(network.Summary, "Not checked: "+reason) {
		t.Fatalf("summary = %q, want the bridges checked and the aliases not", network.Summary)
	}

	if got := hostReads.Load(); got != 1 {
		t.Fatalf("the cluster hosts were read %d times, want once", got)
	}
}

// TestBuilderPreflightRefusesBadRequests refuses, with request.invalid, a
// request naming no check, an unknown or repeated one, an unknown key, or an
// experiment name that is not a config name.
func TestBuilderPreflightRefusesBadRequests(t *testing.T) {
	var hostReads atomic.Int32

	harness, draft := preflightFixture(t, &hostReads)

	for _, body := range []string{
		`{}`,
		`{"checks":[]}`,
		`{"checks":["everything"]}`,
		`{"checks":["apps","apps"]}`,
		`{"checks":["apps"],"more":true}`,
		`{"checks":["network"],"experiment":"two words"}`,
	} {
		recorder := postBuilderPreflight(harness, draft, nil, body)

		var refusal struct {
			Code string `json:"code"`
		}

		harness.decode(recorder, &refusal)

		if recorder.Code != http.StatusBadRequest || refusal.Code != string(bdoc.CodeRequestInvalid) {
			t.Errorf("%s: status = %d, code = %q, want 400 request.invalid", body, recorder.Code, refusal.Code)
		}
	}

	if got := hostReads.Load(); got != 0 {
		t.Fatalf("a refused request read the cluster hosts %d times", got)
	}
}

// TestBuilderPreflightNetworkReadsExperiments checks the network against the
// experiment named, whose VLAN range and default bridge apply, and the VLANs
// the other running experiments hold.
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

	result := preflightResultOf(t, preflightReport(t, harness, postBuilderPreflight(harness, draft, nil,
		`{"checks":["network"],"experiment":"exp"}`)), bapi.PreflightNetwork)

	codes := make([]bdoc.Code, len(result.Issues))
	for i, issue := range result.Issues {
		codes[i] = issue.Code
	}

	want := []bdoc.Code{bdoc.CodePreflightNetworkAliasInUse, bdoc.CodePreflightNetworkBridge}
	if result.Status != bapi.PreflightFailed || !slices.Equal(codes, want) {
		t.Fatalf("network = %+v, want failed with %v", result, want)
	}

	if message := result.Issues[0].Message; !strings.Contains(message, `running experiment other uses for VLAN "X"`) {
		t.Fatalf("alias issue = %q, want VLAN 150 held by other", message)
	}

	if message := result.Issues[1].Message; !strings.Contains(message, `bridge "lab"`) {
		t.Fatalf("bridge issue = %q, want the experiment's default bridge lab", message)
	}

	if !strings.Contains(result.Summary, "experiment exp's VLAN range is 100 to 200") {
		t.Fatalf("summary = %q, want the experiment's range", result.Summary)
	}
}

// TestBuilderPreflightAliasesLeaveOutHiddenExperiments compares the VLAN
// aliases only with the running experiments the caller may list by name: for
// a role whose experiments list names only mine, the VLAN that running
// experiment secret holds is not compared, and the summary says that running
// experiments the role may not list were not compared, naming neither secret
// nor its VLAN. Naming secret as the experiment changes nothing of that.
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

	const note = "running experiments your role may not list were not compared"

	for _, body := range []string{`{"checks":["network"]}`, `{"checks":["network"],"experiment":"secret"}`} {
		recorder := postBuilderPreflight(harness, draft, &scoped, body)
		network := preflightResultOf(t, preflightReport(t, harness, recorder), bapi.PreflightNetwork)

		if slices.ContainsFunc(network.Issues, func(issue bdoc.Issue) bool {
			return issue.Code == bdoc.CodePreflightNetworkAliasInUse
		}) {
			t.Errorf("%s: network = %+v, want the alias not compared with secret's VLAN", body, network)
		}

		if !strings.Contains(network.Summary, "1 VLAN alias compared with running experiments; "+note) {
			t.Errorf("%s: summary = %q, want it to say the experiments the role may not list were not compared", body, network.Summary)
		}

		if answer := recorder.Body.String(); strings.Contains(answer, "HIDDEN") ||
			strings.Contains(answer, "running experiment secret") {
			t.Errorf("%s: the answer names secret or its VLAN: %s", body, answer)
		}
	}

	full := preflightResultOf(t, preflightReport(t, harness, postBuilderPreflight(harness, draft, nil,
		`{"checks":["network"]}`)), bapi.PreflightNetwork)

	issue := preflightIssue(t, full, bdoc.CodePreflightNetworkAliasInUse)
	if !strings.Contains(issue.Message, `running experiment secret uses for VLAN "HIDDEN"`) || strings.Contains(full.Summary, note) {
		t.Fatalf("network for the full role = %+v, want the alias in use by secret and no note", full)
	}
}
