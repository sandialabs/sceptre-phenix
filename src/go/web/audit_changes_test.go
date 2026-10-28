package web

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/activeshadow/libminimega/minicli"

	"phenix/api/settings"
	"phenix/store"
	"phenix/util/mm/mmtest"
	"phenix/web/middleware"
	"phenix/web/rbac"
	"phenix/web/weberror"
)

// wantErrorMessage fails the test unless event's error.message holds want.
func wantErrorMessage(t *testing.T, event auditEvent, want string) {
	t.Helper()

	if got, _ := event["error.message"].(string); !strings.Contains(got, want) {
		t.Errorf("%v error.message = %q, want it to hold %q", event["event.action"], got, want)
	}
}

// Applying a workflow is audited with its branch and the action it planned: a
// refusal, a dry run (as one, never a change), a real apply that changes
// nothing, and a failed one.
func TestWorkflowApplyIsAudited(t *testing.T) {
	const createFoo = "  auto:\n    create: foo\n  topology: topo\n"

	listAndReadTopology := func(m *store.MockStore) {
		expectWorkflowList(m)
		expectWorkflowRead(m, "topology/topo", workflowTestTopology())
	}

	cases := []struct {
		name    string
		deny    bool
		query   string
		body    string
		setup   func(m *store.MockStore)
		outcome string
		want    map[string]any
		wantErr string
	}{
		{
			name: "refused", deny: true, body: workflowTestYAML(createFoo), outcome: "failure",
			want: map[string]any{
				"event.reason": "forbidden", "event.type": []string{"change", "denied"}, "phenix.workflow.action": nil,
			},
		},
		{
			name: "dry run", query: "dryRun=true", body: workflowTestYAML(createFoo), setup: listAndReadTopology,
			outcome: "success",
			want: map[string]any{
				"phenix.dry_run": true, "event.type": []string{"info"}, "phenix.workflow.action": "createAndStart",
				"phenix.experiment.name": "foo", "msg": "checked phenix workflow",
			},
		},
		{
			name: "dry run of an invalid config", query: "dryRun=true", body: workflowTestYAML("  nope: 1\n"),
			outcome: "failure",
			want:    map[string]any{"phenix.dry_run": true, "event.type": []string{"info"}, "event.reason": nil},
			wantErr: "nope",
		},
		{
			name: "apply that changes nothing", body: workflowTestYAML("  auto:\n    restart: false\n"),
			setup: func(m *store.MockStore) {
				expectWorkflowList(m, workflowTestExperiment("foo", "main", true))
			},
			outcome: "success",
			want: map[string]any{
				"phenix.dry_run": nil, "event.type": []string{"change"}, "phenix.workflow.action": "none",
				"phenix.experiment.name": "foo", "msg": "applied phenix workflow",
			},
		},
		{
			name: "failed apply", query: "expect=createAndStart", body: workflowTestYAML(createFoo),
			setup: func(m *store.MockStore) {
				listAndReadTopology(m)
				expectWorkflowReadFails(m, "topology/topo")
			},
			outcome: "failure",
			want: map[string]any{
				"phenix.dry_run": nil, "phenix.workflow.action": "createAndStart", "phenix.experiment.name": "foo",
			},
			wantErr: "unable to create new experiment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := installValidationTestStore(t)
			if tc.setup != nil {
				tc.setup(m)
			}

			events := captureAudit(t)

			req := applyWorkflowRequest(workflowTestRole(!tc.deny), tc.query, mimeYAML, strings.NewReader(tc.body))
			serveApplyWorkflow(t, req)

			fields := map[string]any{
				"user.name": "test-user", "phenix.workflow.branch": "main", "event.category": "configuration",
				"url.path": "/api/v1/workflow/apply/main",
			}

			maps.Copy(fields, tc.want)

			event := wantAudit(t, events(), "workflow-apply", tc.outcome, fields)

			if tc.wantErr != "" && event != nil {
				wantErrorMessage(t, event, tc.wantErr)
			}

			if n := len(events()); n != 1 {
				t.Errorf("POST /workflow/apply/main (%s) logged %d audit events, want 1", tc.name, n)
			}
		})
	}
}

// Upserting a workflow config is audited as creating or updating it, with
// its branch: refusals before and after the config is named, a dry run, and a
// failed upsert.
func TestWorkflowConfigUpsertIsAudited(t *testing.T) {
	yes, no := true, false

	cases := []struct {
		name    string
		role    rbac.Role
		query   string
		body    string
		exists  *bool
		action  string
		outcome string
		want    map[string]any
	}{
		{
			name: "refused before reading the config", role: configsRole(), body: upsertTestTopology,
			action: "config-create", outcome: "failure",
			want: map[string]any{"event.reason": "forbidden", "phenix.config.name": nil},
		},
		{
			name: "refused creating the named config", role: configsRole("update"), body: upsertTestTopology,
			exists: &no, action: "config-create", outcome: "failure",
			want: map[string]any{"event.reason": "forbidden", "phenix.config.name": "Topology/main-topo"},
		},
		{
			name: "refused updating the named config", role: configsRole("create"), body: upsertTestTopology,
			exists: &yes, action: "config-update", outcome: "failure",
			want: map[string]any{"event.reason": "forbidden", "phenix.config.name": "Topology/main-topo"},
		},
		{
			name: "dry run", role: workflowTestRole(true), query: "?dryRun=true", body: upsertTestTopology,
			exists: &no, action: "config-create", outcome: "success",
			want: map[string]any{
				"phenix.dry_run": true, "event.type": []string{"info"}, "phenix.config.name": "Topology/main-topo",
			},
		},
		{
			name: "failed", role: workflowTestRole(true),
			body:   strings.Replace(upsertTestTopology, "nodes: []", "nodes: 7", 1),
			exists: &no, action: "config-create", outcome: "failure",
			want: map[string]any{
				"phenix.dry_run": nil, "event.type": []string{"creation"}, "event.reason": nil,
				"phenix.config.name": "Topology/main-topo",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BRANCH_NAME", "")

			m := installValidationTestStore(t)
			if tc.exists != nil {
				expectUpsertExists(m, workflowTestKey("topology/main-topo"), *tc.exists)
			}

			events := captureAudit(t)

			req := upsertTestRequest(t, tc.query, tc.body)
			req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeyRole, tc.role))

			weberror.ErrorHandler(WorkflowUpsertConfig).ServeHTTP(httptest.NewRecorder(), req)

			fields := map[string]any{"user.name": "test-user", "phenix.workflow.branch": "main"}
			maps.Copy(fields, tc.want)

			wantAudit(t, events(), tc.action, tc.outcome, fields)

			if n := len(events()); n != 1 {
				t.Errorf("POST /workflow/configs/main (%s) logged %d audit events, want 1", tc.name, n)
			}
		})
	}
}

// A workflow config upsert and a workflow apply over the Unix socket are
// audited without a user or client address, as having come over the socket.
//
//nolint:paralleltest // replaces the store and the server options
func TestUnixSocketWorkflowIsAudited(t *testing.T) {
	useTestStore(t)
	useGlobalAdmin(t)
	t.Setenv("BRANCH_NAME", "")

	original := o
	t.Cleanup(func() { o = original })

	o = newServerOptions()

	server := httptest.NewServer(newSocketRouter())
	t.Cleanup(server.Close)

	events := captureAudit(t)
	api := server.URL + "/api/v1/workflow"

	resp := do(t, http.MethodPost, api+"/configs/main", upsertTestTopology, "Content-Type", mimeYAML)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /workflow/configs/main over the socket = %d, want %d: %s",
			resp.StatusCode, http.StatusCreated, readBody(t, resp))
	}

	apply := workflowTestYAML("  auto:\n    create: foo\n  topology: main-topo\n")

	resp = do(t, http.MethodPost, api+"/apply/main?dryRun=true", apply, "Content-Type", mimeYAML)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /workflow/apply/main?dryRun=true over the socket = %d, want %d: %s",
			resp.StatusCode, http.StatusOK, readBody(t, resp))
	}

	socket := map[string]any{
		"phenix.auth.method": "unix-socket", "user.name": nil, "source.ip": nil, "user.roles": []string{"Global Admin"},
		"phenix.workflow.branch": "main",
	}

	created := wantAudit(t, events(), "config-create", "success", socket)
	if created != nil && created["phenix.config.name"] != "Topology/main-topo" {
		t.Errorf("config-create phenix.config.name = %v, want Topology/main-topo", created["phenix.config.name"])
	}

	applied := wantAudit(t, events(), "workflow-apply", "success", socket)
	if applied != nil && applied["phenix.dry_run"] != true {
		t.Errorf("workflow-apply phenix.dry_run = %v, want true", applied["phenix.dry_run"])
	}
}

// Updating a VM, alone or in a bulk PATCH, is audited for each VM with the
// names of the settings changed, but not their values; a refusal before the
// request body is read names none.
func TestVMUpdateIsAudited(t *testing.T) {
	useVMExperiment(t, false, "a", "b")

	var (
		events  = captureAudit(t)
		records = captureRecords(t)
		alice   = serveAs(t, "alice", testRole("vms", "patch", "exp/a", "exp/missing")).URL + "/api/v1/experiments/exp/vms"
	)

	do(t, http.MethodPatch, alice+"/a", `{"cpus":4,"annotations":{"note":"a long value"},"interface":{"index":1,"vlan":"EXP"}}`)
	do(t, http.MethodPatch, alice+"/b", `{"ram":2048}`)
	do(t, http.MethodPatch, alice, `{"vms":[{"name":"a","ram":1024},{"name":"b","cpus":2}]}`)
	do(t, http.MethodPatch, alice+"/missing", `{"disk":"other.qc2"}`)

	got := events()

	wantAudit(t, got, "vm-update", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "exp", "phenix.vm.name": "a", "event.category": "configuration",
		"phenix.vm.changed": []string{"cpus", "interface", "annotations"}, "phenix.vm.interface": 1,
		"url.path": "/api/v1/experiments/exp/vms/a",
	})
	wantAudit(t, got, "vm-update", "failure", map[string]any{
		"phenix.vm.name": "b", "event.reason": "forbidden", "url.path": "/api/v1/experiments/exp/vms/b",
	})
	wantAudit(t, got, "vm-update", "success", map[string]any{
		"phenix.vm.name": "a", "phenix.vm.changed": []string{"memory"}, "url.path": "/api/v1/experiments/exp/vms",
	})
	wantAudit(t, got, "vm-update", "failure", map[string]any{
		"phenix.vm.name": "b", "event.reason": "forbidden", "phenix.vm.changed": []string{"cpus"},
		"url.path": "/api/v1/experiments/exp/vms",
	})

	failed := wantAudit(t, got, "vm-update", "failure", map[string]any{
		"phenix.vm.name": "missing", "event.reason": nil, "phenix.vm.changed": []string{"disk"},
	})
	if failed != nil {
		wantErrorMessage(t, failed, "unable to find VM missing")
	}

	for _, e := range got {
		if strings.Contains(eventText(t, e), "a long value") {
			t.Errorf("vm-update event %v holds an annotation's value", e)
		}
	}

	for _, line := range records() {
		if strings.Contains(line, "a long value") {
			t.Errorf("logged record %s, want none holding an annotation's value", line)
		}
	}
}

// eventText is the event as JSON.
func eventText(t *testing.T, e auditEvent) string {
	t.Helper()

	text, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("json.Marshal(%v) error = %v", e, err)
	}

	return string(text)
}

// Changing and ejecting a VM's optical disc are audited: refusals, and the
// change's success and failures, with the disc's path.
//
//nolint:paralleltest // the fake minimega is shared by the process
func TestOpticalDiscIsAudited(t *testing.T) {
	useVMExperiment(t, true, "a")

	mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
		if strings.Contains(cmd.Base, "missing.iso") {
			return []*minicli.Response{{Host: "head", Error: "no such file"}}
		}

		return nil
	})

	var (
		events = captureAudit(t)
		alice  = serveAs(t, "alice", combinedRole(
			testRole("vms/cdrom", "update", "exp/a"),
			testRole("vms/cdrom", "delete", "exp/a"),
		)).URL + "/api/v1/experiments/exp/vms/a/cdrom"
		bob = serveAs(t, "bob", testRole("vms", "get", "exp/*")).URL + "/api/v1/experiments/exp/vms/a/cdrom"
	)

	do(t, http.MethodPost, alice+"?isoPath=/phenix/images/tools.iso", "")
	do(t, http.MethodPost, alice+"?isoPath=/phenix/images/missing.iso", "")
	do(t, http.MethodPost, alice, "")
	do(t, http.MethodPost, bob+"?isoPath=/phenix/images/tools.iso", "")
	do(t, http.MethodDelete, alice, "")
	do(t, http.MethodDelete, bob, "")

	got := events()

	wantAudit(t, got, "vm-cdrom-change", "success", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "exp", "phenix.vm.name": "a", "event.category": "host",
		"file.path": "/phenix/images/tools.iso",
	})
	missing := wantAudit(t, got, "vm-cdrom-change", "failure", map[string]any{
		"file.path": "/phenix/images/missing.iso", "event.reason": nil,
	})

	if missing != nil {
		wantErrorMessage(t, missing, "no such file")
	}

	none := wantAudit(t, got, "vm-cdrom-change", "failure", map[string]any{"file.path": nil, "event.reason": nil})
	if none != nil {
		wantErrorMessage(t, none, "no optical disc path provided")
	}

	wantAudit(t, got, "vm-cdrom-change", "failure", map[string]any{"user.name": "bob", "event.reason": "forbidden"})
	wantAudit(t, got, "vm-cdrom-eject", "success", map[string]any{"user.name": "alice", "phenix.vm.name": "a"})
	wantAudit(t, got, "vm-cdrom-eject", "failure", map[string]any{"user.name": "bob", "event.reason": "forbidden"})
}

// Starting and stopping an experiment's netflow capture are audited: their
// refusals, and failures such as starting it on a stopped experiment.
func TestNetflowIsAudited(t *testing.T) {
	useVMExperiment(t, false, "a")

	var (
		events = captureAudit(t)
		alice  = serveAs(t, "alice", combinedRole(
			testRole("experiments/netflow", "create", "exp"),
			testRole("experiments/netflow", "delete", "exp"),
		)).URL + "/api/v1/experiments/exp/netflow"
		bob = serveAs(t, "bob", testRole("experiments/netflow", "get", "exp")).URL + "/api/v1/experiments/exp/netflow"
	)

	for _, tc := range []struct {
		method, url string
		want        int
	}{
		{http.MethodPost, alice, http.StatusConflict},
		{http.MethodDelete, alice, http.StatusNotFound},
		{http.MethodPost, bob, http.StatusForbidden},
		{http.MethodDelete, bob, http.StatusForbidden},
	} {
		if resp := do(t, tc.method, tc.url, ""); resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.url, resp.StatusCode, tc.want)
		}
	}

	got := events()

	wantAudit(t, got, "netflow-start", "failure", map[string]any{
		"user.name": "alice", "phenix.experiment.name": "exp", "event.category": "network", "event.reason": nil,
	})
	wantAudit(t, got, "netflow-stop", "failure", map[string]any{"user.name": "alice", "event.reason": nil})
	wantAudit(t, got, "netflow-start", "failure", map[string]any{"user.name": "bob", "event.reason": "forbidden"})
	wantAudit(t, got, "netflow-stop", "failure", map[string]any{"user.name": "bob", "event.reason": "forbidden"})
}

// settingsBody is a POST /settings body with the default settings, but for
// the password minimum length and whether sessions time out.
func settingsBody(minLength string, timeout bool) string {
	enabled := strconv.FormatBool(timeout)

	return `{"password_settings":{"lowercase_req":true,"uppercase_req":false,"number_req":true,"symbol_req":true,` +
		`"min_length":` + minLength + `},"logging_settings":{"max_file_rotations":0,"max_file_size":100,"max_file_age":90},` +
		`"timeout_settings":{"enabled":` + enabled + `,"timeout_min":30,"warning_min":3}}`
}

// Changing the settings is audited with the names and values of those
// changed, and so are its refusals and failures.
func TestSettingsChangesAreAudited(t *testing.T) {
	useTestStore(t)

	if err := settings.SetDefaults(); err != nil {
		t.Fatalf("settings.SetDefaults() error = %v", err)
	}

	var (
		events = captureAudit(t)
		alice  = serveAs(t, "alice", testRole("settings", "update")).URL + "/api/v1/settings"
		bob    = serveAs(t, "bob", testRole("settings", "get")).URL + "/api/v1/settings"
	)

	for _, tc := range []struct {
		url, body string
		want      int
	}{
		{bob, settingsBody("12", true), http.StatusForbidden},
		{alice, settingsBody("3", true), http.StatusBadRequest},
		{alice, settingsBody("12", true), http.StatusOK},
	} {
		if resp := do(t, http.MethodPost, tc.url, tc.body); resp.StatusCode != tc.want {
			t.Errorf("POST %s %s = %d, want %d", tc.url, tc.body, resp.StatusCode, tc.want)
		}
	}

	got := events()
	changed := []string{"password_settings.min_length", "timeout_settings.enabled"}

	wantAudit(t, got, "settings-update", "failure", map[string]any{"user.name": "bob", "event.reason": "forbidden"})

	refused := wantAudit(t, got, "settings-update", "failure", map[string]any{
		"user.name": "alice", "event.reason": nil, "phenix.settings.changed": changed,
	})
	if refused != nil {
		wantErrorMessage(t, refused, settings.ErrPasswordMinLength.Error())
	}

	wantAudit(t, got, "settings-update", "success", map[string]any{
		"user.name": "alice", "event.category": "configuration", "phenix.settings.changed": changed,
		"phenix.settings.values": map[string]any{"password_settings.min_length": "12", "timeout_settings.enabled": "true"},
	})
}

// A request refused for its token is audited once, with why, and never with
// the token; the refusal's status is as before.
//
//nolint:paralleltest // replaces the server options
func TestTokenRefusalsAreAudited(t *testing.T) {
	useTestStore(t)

	original := o
	t.Cleanup(func() { o = original })

	o.jwtKey = "token-refusal-test-key"

	router := newRouter(http.FS(testPublic()))

	const garbage = "garbage.token.value"

	var (
		events = captureAudit(t)
		// addresses of this run alone, as the rate limit outlives a test
		run    = time.Now().UnixNano() & 0xffffffff
		header = []string{"X-Phenix-Auth-Token", "Bearer " + garbage}
		ip     = func(n int) string { return fmt.Sprintf("2001:db8::%x:%d", run, n) }
	)

	for _, tc := range []struct {
		name, ip, target string
		header           []string
		want             int
	}{
		{"a garbage token", ip(1), "/api/v1/experiments", header, http.StatusUnauthorized},
		{"the same, again", ip(1), "/api/v1/experiments", header, http.StatusUnauthorized},
		{"a garbage token in the query", ip(2), "/api/v1/experiments?token=" + garbage, nil, http.StatusUnauthorized},
		{"no token", ip(3), "/api/v1/experiments", nil, http.StatusForbidden},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		req.RemoteAddr = "[" + tc.ip + "]:1000"

		for i := 0; i+1 < len(tc.header); i += 2 {
			req.Header.Set(tc.header[i], tc.header[i+1])
		}

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != tc.want {
			t.Errorf("GET %s with %s = %d, want %d", tc.target, tc.name, w.Code, tc.want)
		}
	}

	got := events()

	for _, e := range got {
		if strings.Contains(eventText(t, e), garbage) {
			t.Errorf("token refusal %v holds the token", e)
		}
	}

	refused := map[string]any{
		"event.category": "authentication", "event.type": []string{"info"}, "url.path": "/api/v1/experiments",
		"phenix.suppressed": 0,
	}

	for addr, reason := range map[string]string{
		ip(1): middleware.RefusedInvalid, ip(2): middleware.RefusedInvalid, ip(3): middleware.RefusedMissing,
	} {
		want := map[string]any{"source.ip": addr, "event.reason": reason}
		maps.Copy(want, refused)

		if n := countAudit(got, "request-auth", "failure", want); n != 1 {
			t.Errorf("request-auth refusals from %s for %s = %d, want 1", addr, reason, n)
		}
	}
}
