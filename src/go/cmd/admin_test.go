package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/store"
	"phenix/util/common"
	"phenix/web"
)

func TestParsePolicy(t *testing.T) {
	tests := []struct {
		value   string
		want    web.Policy
		wantErr bool
	}{
		{
			value: "resources=vms,vms/*;verbs=get,list;names=lab/*",
			want:  web.Policy{Resources: []string{"vms", "vms/*"}, Verbs: []string{"get", "list"}, ResourceNames: []string{"lab/*"}},
		},
		{
			value: " verbs = list ; resources = experiments ",
			want:  web.Policy{Resources: []string{"experiments"}, Verbs: []string{"list"}, ResourceNames: nil},
		},
		{value: "resources=vms", wantErr: true},
		{value: "verbs=get", wantErr: true},
		{value: "resources=vms;verbs=get;verbs=list", wantErr: true},
		{value: "resources=vms;verbs=get,", wantErr: true},
		{value: "resources=vms;verbs=get;owner=me", wantErr: true},
		{value: "resources:vms;verbs=get", wantErr: true},
		{value: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.value, func(t *testing.T) {
			got, err := parsePolicy(tc.value)
			if tc.wantErr {
				if !errors.Is(err, errBadPolicy) {
					t.Errorf("parsePolicy(%q) error = %v, want %v", tc.value, err, errBadPolicy)
				}

				return
			}

			if err != nil {
				t.Fatalf("parsePolicy(%q) error = %v", tc.value, err)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parsePolicy(%q) mismatch (-want +got):\n%s", tc.value, diff)
			}
		})
	}
}

// A policy written as a --policy value parses back to itself.
func TestParsePolicyProperty(t *testing.T) {
	item := rapid.StringMatching(`[a-z*!/_.-]{1,10}`)

	rapid.Check(t, func(t *rapid.T) {
		want := web.Policy{
			Resources:     rapid.SliceOfN(item, 1, 4).Draw(t, "resources"),
			Verbs:         rapid.SliceOfN(item, 1, 4).Draw(t, "verbs"),
			ResourceNames: nil,
		}

		parts := []string{"resources=" + strings.Join(want.Resources, ","), "verbs=" + strings.Join(want.Verbs, ",")}

		if rapid.Bool().Draw(t, "named") {
			want.ResourceNames = rapid.SliceOfN(item, 1, 4).Draw(t, "names")
			parts = append(parts, "names="+strings.Join(want.ResourceNames, ","))
		}

		if rapid.Bool().Draw(t, "reversed") {
			parts[0], parts[1] = parts[1], parts[0]
		}

		value := strings.Join(parts, ";")

		got, err := parsePolicy(value)
		if err != nil {
			t.Fatalf("parsePolicy(%q) error = %v", value, err)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("parsePolicy(%q) mismatch (-want +got):\n%s", value, diff)
		}
	})
}

// With --password-stdin, each password is one line of stdin; without it, stdin must be a terminal.
func TestPasswordReader(t *testing.T) {
	cmd := newAdminCmd()
	cmd.SetIn(strings.NewReader("old secret\r\nnew secret"))
	cmd.SetErr(new(bytes.Buffer))

	r := newPasswordReader(cmd, true)

	for _, want := range []string{"old secret", "new secret"} {
		if got, err := r.read("Password: ", true); err != nil || got != want {
			t.Errorf("read() = %q, %v, want %q, nil", got, err, want)
		}
	}

	if got, err := r.read("Password: ", true); err == nil {
		t.Errorf("read() past the end of stdin = %q, nil, want an error", got)
	}

	cmd.SetIn(strings.NewReader("secret\n"))

	if _, err := newPasswordReader(cmd, false).read("Password: ", true); !errors.Is(err, errNoPasswordInput) {
		t.Errorf("read() of stdin that is not a terminal error = %v, want %v", err, errNoPasswordInput)
	}
}

// useDefaultSettingsStore makes a store mock with no stored settings the phenix store, so the default password
// settings apply.
func useDefaultSettingsStore(t *testing.T) {
	t.Helper()

	m := store.NewMockStore(gomock.NewController(t))
	m.EXPECT().List(gomock.Any()).Return(store.Configs{}, nil).AnyTimes()
	m.EXPECT().Create(gomock.Any()).Return(nil).AnyTimes()

	original := store.DefaultStore
	store.DefaultStore = m //nolint:reassign // monkey patching for test

	t.Cleanup(func() { store.DefaultStore = original }) //nolint:reassign // restore
}

// runAdminCmd runs phenix admin with args and stdin against the server at socket.
func runAdminCmd(t *testing.T, socket, stdin string, args ...string) (string, error) {
	t.Helper()

	original := common.UnixSocket
	common.UnixSocket = socket //nolint:reassign // point the command at the fake server

	t.Cleanup(func() { common.UnixSocket = original }) //nolint:reassign // restore

	cmd := newAdminCmd()

	var out bytes.Buffer

	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

// The admin commands that change things send the API's request bodies to the socket.
func TestAdminChanges(t *testing.T) {
	useDefaultSettingsStore(t)

	fake := newFakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tokens"):
			writeJSONBody(w, web.CreateTokenResponse{Token: "signed", Description: "ci", Expiration: "2026-11-07T00:00:00Z"})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case strings.HasPrefix(r.URL.Path, "/api/v1/roles"):
			writeJSONBody(w, web.Role{Name: "Lab", Policies: nil})
		default:
			writeJSONBody(w, web.User{Username: "alice"})
		}
	})

	tests := []struct {
		args     []string
		stdin    string
		method   string
		path     string
		wantBody any
		wantOut  string
	}{
		{
			args:   []string{"user", "create", "alice", "--role", "experiment-user", "--resource-names", "lab-*", "--password-stdin"},
			stdin:  "Str0ng!Passw0rd\n",
			method: http.MethodPost, path: "/api/v1/users",
			wantBody: web.CreateUserRequest{
				Username: "alice", Password: "Str0ng!Passw0rd", RoleName: "experiment-user", ResourceNames: []string{"lab-*"},
			},
		},
		{
			args:   []string{"user", "update", "alice", "--first-name", "Alice", "--password-stdin"},
			stdin:  "old\nN3w!Passw0rd\n",
			method: http.MethodPatch, path: "/api/v1/users/alice",
			wantBody: web.UpdateUserRequest{
				CreateUserRequest: web.CreateUserRequest{FirstName: "Alice", Password: "old"}, NewPassword: "N3w!Passw0rd",
			},
		},
		{args: []string{"user", "delete", "bob"}, method: http.MethodDelete, path: "/api/v1/users/bob"},
		{
			args:   []string{"role", "create", "Lab", "--policy", "resources=vms;verbs=list"},
			method: http.MethodPost, path: "/api/v1/roles",
			wantBody: web.CreateRoleRequest{Name: "Lab", Policies: &[]web.Policy{{Resources: []string{"vms"}, Verbs: []string{"list"}}}},
		},
		{args: []string{"role", "delete", "lab"}, method: http.MethodDelete, path: "/api/v1/roles/lab"},
		{
			args:   []string{"token", "create", "ci-bot", "--lifetime", "30", "--description", "ci"},
			method: http.MethodPost, path: "/api/v1/users/ci-bot/tokens",
			wantBody: web.CreateTokenRequest{Lifetime: "30", Description: "ci"}, wantOut: "signed\n",
		},
	}

	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			before := len(fake.recorded())

			out, err := runAdminCmd(t, fake.socket, tc.stdin, tc.args...)
			if err != nil {
				t.Fatalf("admin %q error = %v", tc.args, err)
			}

			got := fake.recorded()[before:]
			if len(got) != 1 || got[0].method != tc.method || got[0].path != tc.path {
				t.Fatalf("admin %q requests = %+v, want one %s %s", tc.args, got, tc.method, tc.path)
			}

			if tc.wantBody != nil {
				want, _ := json.Marshal(tc.wantBody)
				if diff := cmp.Diff(decodeJSON(t, string(want)), decodeJSON(t, got[0].body)); diff != "" {
					t.Errorf("admin %q body mismatch (-want +got):\n%s", tc.args, diff)
				}
			}

			if tc.wantOut != "" && out != tc.wantOut {
				t.Errorf("admin %q output = %q, want %q", tc.args, out, tc.wantOut)
			}
		})
	}
}

// decodeJSON decodes body for a comparison of decoded data.
func decodeJSON(t *testing.T, body string) any {
	t.Helper()

	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", body, err)
	}

	return v
}

// The admin commands refuse bad input before they call the server.
func TestAdminRefusals(t *testing.T) {
	useDefaultSettingsStore(t)

	fake := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	for _, tc := range []struct {
		args  []string
		stdin string
	}{
		{args: []string{"user", "create", "alice", "--role", "experiment-user", "--password-stdin"}, stdin: "short\n"},
		{args: []string{"user", "create", "alice", "--password-stdin"}, stdin: "Str0ng!Passw0rd\n"},
		{args: []string{"user", "create", "-alice", "--role", "r", "--password-stdin"}, stdin: "Str0ng!Passw0rd\n"},
		{args: []string{"user", "create", "alice", "--role", "experiment-user"}},
		{args: []string{"user", "update", "alice"}},
		{args: []string{"role", "create", "Lab"}},
		{args: []string{"role", "update", "lab", "--policy", "resources=vms"}},
		{args: []string{"token", "create", "alice", "--lifetime", "0"}},
		{args: []string{"token", "create", "alice", "--lifetime", "forever"}},
	} {
		if _, err := runAdminCmd(t, fake.socket, tc.stdin, tc.args...); err == nil {
			t.Errorf("admin %q error = nil, want an error", tc.args)
		}
	}

	if got := fake.recorded(); len(got) != 0 {
		t.Errorf("requests = %+v, want none", got)
	}
}
