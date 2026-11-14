package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/sandia-minimega/minimega/v2/pkg/minicli"

	"phenix/api/config"
	"phenix/api/minimega"
	"phenix/util/mm/mmtest"
	"phenix/web/rbac"
)

// POST /minimega/commands sends the command line, in the namespace the body
// names, answers with each host's answer and errors, and audits it.
func TestRunMinimegaCommand(t *testing.T) {
	commander := testRole("minimega/commands", "create")

	tests := map[string]struct {
		role     rbac.Role
		body     string
		want     int
		wantSent string // the command minimega got, "" for none
		wantBody minimegaCommandResponse
		outcome  string // the audit outcome, "" for none
	}{
		"a command": {
			role: commander, body: `{"command":"vm info"}`, want: http.StatusOK, wantSent: "vm info",
			wantBody: minimegaCommandResponse{Command: "vm info", Result: minimega.Result{
				Output:    "vm1",
				Responses: []minimega.HostResponse{{Host: "head", Response: "vm1"}},
				Errors:    []string{},
			}},
			outcome: "success",
		},
		"in a namespace": {
			role: commander, body: `{"command":"vm info","namespace":"my exp"}`, want: http.StatusOK,
			wantSent: `namespace "my exp" vm info`,
			wantBody: minimegaCommandResponse{Command: `namespace "my exp" vm info`, Result: minimega.Result{
				Output:    "vm1",
				Responses: []minimega.HostResponse{{Host: "head", Response: "vm1"}},
				Errors:    []string{},
			}},
			outcome: "success",
		},
		"an error from minimega": {
			role: commander, body: `{"command":"vm kill nope"}`, want: http.StatusOK, wantSent: "vm kill nope",
			wantBody: minimegaCommandResponse{Command: "vm kill nope", Result: minimega.Result{
				Responses: []minimega.HostResponse{{Host: "head", Error: "vm not found"}},
				Errors:    []string{"head: vm not found"},
			}},
			outcome: "failure",
		},
		"no command": {role: commander, body: `{"command":"  "}`, want: http.StatusBadRequest},
		"two lines":  {role: commander, body: `{"command":"vm info\nvm kill all"}`, want: http.StatusBadRequest},
		"not a body": {role: commander, body: `{`, want: http.StatusBadRequest},
		"no permission": {
			role:    testRole("minimega/commands", "get"),
			body:    `{"command":"vm info"}`,
			want:    http.StatusForbidden,
			outcome: "failure",
		},
		"another resource": {
			role:    testRole("vms", "create", "*"),
			body:    `{"command":"vm info"}`,
			want:    http.StatusForbidden,
			outcome: "failure",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			received := mmtest.Use(t, func(cmd mmtest.Command) []*minicli.Response {
				if strings.Contains(cmd.Raw, "vm kill") {
					return []*minicli.Response{mmtest.Error("head", "vm not found")}
				}

				return []*minicli.Response{mmtest.Text("head", "vm1")}
			})

			server := serveAs(t, "alice", tt.role).URL
			events := captureAudit(t)

			resp := do(t, http.MethodPost, server+"/api/v1/minimega/commands", tt.body)
			body := readBody(t, resp)

			if resp.StatusCode != tt.want {
				t.Fatalf("POST /minimega/commands %s = %d, want %d: %s", tt.body, resp.StatusCode, tt.want, body)
			}

			commands := received()

			sent := make([]string, 0, len(commands))
			for _, cmd := range commands {
				sent = append(sent, cmd.Raw)
			}

			wantSent := []string{}
			if tt.wantSent != "" {
				wantSent = []string{tt.wantSent}
			}

			if diff := cmp.Diff(wantSent, sent); diff != "" {
				t.Errorf("POST /minimega/commands %s sent mismatch (-want +got):\n%s", tt.body, diff)
			}

			if tt.want == http.StatusOK {
				var got minimegaCommandResponse
				if err := json.Unmarshal([]byte(body), &got); err != nil {
					t.Fatalf("POST /minimega/commands = %s, not JSON: %v", body, err)
				}

				// the fake minimega renders nothing
				got.Output, tt.wantBody.Output = "", ""

				if diff := cmp.Diff(tt.wantBody, got); diff != "" {
					t.Errorf("POST /minimega/commands %s mismatch (-want +got):\n%s", tt.body, diff)
				}
			}

			if tt.outcome == "" {
				return
			}

			fields := map[string]any{"user.name": "alice"}
			if tt.want == http.StatusOK {
				fields["phenix.minimega.command"] = tt.wantSent
			}

			wantAudit(t, events(), "minimega-command", tt.outcome, fields)
		})
	}
}

// Of the built-in roles, only Global Admin may run minimega commands and read
// or change the runtime settings.
func TestBuiltinRolesOfAdminOnlyPermissions(t *testing.T) {
	configs, err := config.BuiltinRoleConfigs()
	if err != nil {
		t.Fatalf("BuiltinRoleConfigs() error = %v", err)
	}

	if len(configs) == 0 {
		t.Fatal("BuiltinRoleConfigs() = none, want the built-in roles")
	}

	checks := []rbac.Permission{
		{Resource: "minimega/commands", Verb: "create"},
		{Resource: resourceRuntimeSettings, Verb: "read"},
		{Resource: resourceRuntimeSettings, Verb: "update"},
	}

	for _, c := range configs {
		role := builtinRole(t, c.Metadata.Name)

		for _, p := range checks {
			if got, want := role.Allowed(p.Resource, p.Verb), role.Spec.Name == "Global Admin"; got != want {
				t.Errorf("built-in role %s allows %s %s = %t, want %t", role.Spec.Name, p.Resource, p.Verb, got, want)
			}
		}
	}
}
