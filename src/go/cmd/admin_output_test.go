package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"phenix/api/config"
	"phenix/store"
	"phenix/store/storetest"
	"phenix/util/mm"
	"phenix/web"
	"phenix/web/rbac"
)

// adminOutputUseStore makes a new store the phenix store, with the built-in
// role vm-viewer, the custom roles lab-viewer and lab-user, and the user alice
// with the role lab-user.
func adminOutputUseStore(t *testing.T) {
	t.Helper()

	storetest.UseBolt(t)

	if _, err := config.CreateDefault("role", "vm-viewer"); err != nil {
		t.Fatalf("config.CreateDefault(role, vm-viewer) error = %v", err)
	}

	for name, roleName := range map[string]string{"lab-viewer": "Lab Viewer", "lab-user": "Lab User"} {
		c := store.Config{
			Version: "phenix.sandia.gov/v1", Kind: "Role", Metadata: store.ConfigMetadata{Name: name},
			Spec: map[string]any{"roleName": roleName, "policies": []any{
				map[string]any{"resources": []any{"vms"}, "verbs": []any{"list"}},
			}},
		}

		if err := store.Create(&c); err != nil {
			t.Fatalf("store.Create(role %s) error = %v", name, err)
		}
	}

	alice, err := rbac.NewUser("alice", "Str0ng!Passw0rd", "Alice", "Smith")
	if err != nil {
		t.Fatalf("rbac.NewUser(alice) error = %v", err)
	}

	role, err := rbac.RoleFromConfig("lab-user")
	if err != nil {
		t.Fatalf("rbac.RoleFromConfig(lab-user) error = %v", err)
	}

	if err := alice.SetRole(role); err != nil {
		t.Fatalf("alice.SetRole(lab-user) error = %v", err)
	}
}

// adminOutputRole is the part of a role of phenix admin role -o json that the
// tests check.
type adminOutputRole struct {
	ConfigName string       `json:"configName"`
	Name       string       `json:"name"`
	Policies   []web.Policy `json:"policies"`
}

// The admin list and get commands print their tables as before, and the
// users and roles of the API with -o json and -o yaml.
func TestAdminOutput(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	adminOutputUseStore(t)

	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

	run := func(args ...string) func(...string) (string, error) {
		return func(more ...string) (string, error) {
			return runAdminCmd(t, fake.socket, "", append(append([]string{}, args...), more...)...)
		}
	}

	stored, err := rbac.GetUser("alice")
	if err != nil {
		t.Fatalf("rbac.GetUser(alice) error = %v", err)
	}

	alice := web.UserView(*stored)

	if alice.Username != "alice" || alice.FirstName != "Alice" || alice.Role.Name != "Lab User" {
		t.Fatalf("web.UserView(alice) = %+v, want alice Alice with the role Lab User", alice)
	}

	if table := checkOutputFormats(
		t,
		"phenix admin user list",
		run("user", "list"),
		[]web.User{alice},
	); !strings.Contains(
		table,
		"Lab User",
	) {
		t.Errorf("phenix admin user list = %q, want the role Lab User", table)
	}

	if table := checkOutputFormats(
		t,
		"phenix admin user get alice",
		run("user", "get", "alice"),
		alice,
	); !strings.Contains(
		table,
		"RESOURCES",
	) {
		t.Errorf("phenix admin user get alice = %q, want the policy table", table)
	}

	lab := []web.Policy{{Resources: []string{"vms"}, ResourceNames: nil, Verbs: []string{"list"}}}

	checkOutputFormats(t, "phenix admin role get lab-viewer", run("role", "get", "lab-viewer"),
		adminOutputRole{ConfigName: "lab-viewer", Name: "Lab Viewer", Policies: lab})

	out, err := runAdminCmd(t, fake.socket, "", "role", "list", "-o", "json")
	if err != nil {
		t.Fatalf("phenix admin role list -o json error = %v", err)
	}

	for _, want := range []string{`"configName": "lab-user"`, `"configName": "lab-viewer"`, `"configName": "vm-viewer"`, `"name": "VM Viewer"`} {
		if !strings.Contains(out, want) {
			t.Errorf("phenix admin role list -o json = %q, want %s", out, want)
		}
	}

	if got := fake.recorded(); len(got) != 0 {
		t.Errorf("the admin views sent %+v to the server, want none", got)
	}
}

// The admin deletes with --dry-run check the store as the server does, print
// what they would delete, and call no server.
func TestAdminDryRun(t *testing.T) { //nolint:paralleltest // replaces the phenix store
	adminOutputUseStore(t)

	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	tests := []struct {
		args    []string
		want    string
		errHas  string
		wantErr error
	}{
		{args: []string{"user", "delete", "alice"}, want: "would delete user alice\n"},
		{args: []string{"user", "delete", "bob"}, errHas: "unable to delete the user bob"},
		{args: []string{"user", "delete", socketUser}, wantErr: errOwnUser},
		{args: []string{"role", "delete", "lab-viewer"}, want: "would delete role lab-viewer\n"},
		{args: []string{"role", "delete", "Lab Viewer"}, want: "would delete role Lab Viewer\n"},
		{args: []string{"role", "delete", "lab-user"}, errHas: "is assigned to 1 users"},
		{args: []string{"role", "delete", "vm-viewer"}, errHas: "is a built-in role"},
		{args: []string{"role", "delete", "none"}, errHas: "role not found"},
	}

	for _, tt := range tests {
		args := append(append([]string{}, tt.args...), "--dry-run")

		out, err := runAdminCmd(t, fake.socket, "", args...)

		switch {
		case tt.wantErr != nil:
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("phenix admin %q error = %v, want %v", args, err, tt.wantErr)
			}
		case tt.errHas != "":
			if err == nil || !strings.Contains(err.Error(), tt.errHas) {
				t.Errorf("phenix admin %q error = %v, want it to contain %q", args, err, tt.errHas)
			}
		case err != nil:
			t.Errorf("phenix admin %q error = %v", args, err)
		}

		if err == nil && out != tt.want {
			t.Errorf("phenix admin %q = %q, want %q", args, out, tt.want)
		}
	}

	if _, err := rbac.GetUser("alice"); err != nil {
		t.Errorf("after the dry runs, rbac.GetUser(alice) error = %v, want the user", err)
	}

	if _, err := rbac.RoleFromConfig("lab-viewer"); err != nil {
		t.Errorf("after the dry runs, rbac.RoleFromConfig(lab-viewer) error = %v, want the role", err)
	}

	if got := fake.recorded(); len(got) != 0 {
		t.Errorf("the dry runs sent %+v to the server, want none", got)
	}
}

// host list prints the table as before, and the hosts of GET /hosts with
// -o json and -o yaml.
func TestHostListOutput(t *testing.T) { //nolint:paralleltest // replaces minimega
	utilCmdUseMM(t, utilCmdMM{})

	run := func(args ...string) (string, error) {
		out, err := outputRun(newHostCmd(), append([]string{"host", "list"}, args...)...)

		return out, err
	}

	if table := checkOutputFormats(
		t,
		"phenix host list",
		run,
		[]mm.Host{{Name: "compute1", Schedulable: true}},
	); !strings.Contains(
		table,
		"compute1",
	) {
		t.Errorf("phenix host list = %q, want compute1", table)
	}
}

// logs and logs files print their lines and table as before, and the
// entries and files of the API with -o json and -o yaml.
func TestLogsOutput(t *testing.T) {
	size := int64(2048)
	modified := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	entries := []logEntry{{Time: 1, Timestamp: "2026-11-06 08:00:00.000", Level: "INFO", Type: "SYSTEM", Message: "started "}}
	files := []web.LogFile{
		{Name: "phenix", Available: true, Size: &size, Modified: &modified},
		{Name: "minimega", Reason: "the minimega log file is not configured"},
	}

	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/logs/files" {
			writeJSONBody(w, map[string][]web.LogFile{"files": files})

			return
		}

		writeJSONBody(w, entries)
	})

	run := func(args ...string) func(...string) (string, error) {
		return func(more ...string) (string, error) {
			return runSocketCmd(t, fake.socket, newLogsCmd(), append(append([]string{}, args...), more...)...)
		}
	}

	if table := checkOutputFormats(t, "phenix logs", run(), entries); table != "2026-11-06 08:00:00.000 INFO  SYSTEM    started\n" {
		t.Errorf("phenix logs = %q, want the line of the entry", table)
	}

	if table := checkOutputFormats(t, "phenix logs files", run("files"), files); !strings.Contains(table, "2.0 KiB") {
		t.Errorf("phenix logs files = %q, want the size of the phenix log", table)
	}
}
