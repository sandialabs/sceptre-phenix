package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"

	"phenix/web"
)

// The users phenix ui creates give a row per entry it skips or may skip, by
// position and name, and an empty list warns only when sign-in needs a user.
func TestUsersSettingCheck(t *testing.T) {
	t.Parallel()

	roles := builtinRoles()

	tests := map[string]struct {
		users []string
		key   string
		want  []doctorCheck
	}{
		"users": {
			users: []string{"alice:Alicepass1!:Global Admin", "bob:Bobpass1!:vm-viewer:exp1"},
			key:   "secret",
			want:  []doctorCheck{passCheck("2 users")},
		},
		"default user with sign-in": {
			key: "secret",
			want: []doctorCheck{
				warnCheck(
					"empty, so phenix ui creates the default user admin@foo.com, with the password in the phenix source, when the store lacks it",
				),
			},
		},
		"default user with proxy JWTs": {
			key: proxyJWTKey,
			want: []doctorCheck{
				warnCheck(
					"empty, so phenix ui creates the default user admin@foo.com, with the password in the phenix source, when the store lacks it",
				),
			},
		},
		"default user without sign-in": {
			want: []doctorCheck{passCheck("empty, so phenix ui creates the default user admin@foo.com, but requires no sign-in")},
		},
		"default user with a dev key": {
			key:  "dev|alice|global-admin",
			want: []doctorCheck{passCheck("empty, so phenix ui creates the default user admin@foo.com, but requires no sign-in")},
		},
		"skipped users": {
			users: []string{"alice:Alicepass1!:global-admin", "bob:Bobpass1!", "Carolpass1!", "dave:Davepass1!:wizard"},
			key:   "secret",
			want: []doctorCheck{
				failCheck(`default user 2 ("bob") is not name:password:role[:resource...], so phenix ui skips it`),
				failCheck(`default user 3 ("") is not name:password:role[:resource...], so phenix ui skips it`),
				warnCheck(`user 4 ("dave") has a role that is not built in, so phenix ui skips the user unless the store holds the role`),
			},
		},
		"comma in a role": {
			users: []string{"alice:Alicepass1!:global-admin,bob:Bobpass1!:global-admin"},
			want: []doctorCheck{failCheck(
				`user 1 ("alice") has a comma in its role. phenix splits PHENIX_UI_USERS on spaces, so separate users with spaces`,
			)},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := usersSettingCheck(tc.users, tc.key, roles)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("usersSettingCheck(%q) mismatch (-want +got):\n%s", tc.users, diff)
			}

			for _, c := range got {
				for _, password := range []string{"Alicepass1!", "Bobpass1!", "Carolpass1!", "Davepass1!"} {
					if strings.Contains(c.Detail, password) {
						t.Errorf("usersSettingCheck(%q) row %q shows the password %s", tc.users, c.Detail, password)
					}
				}
			}
		})
	}
}

// For any entries, doctor fails exactly those that phenix ui's parser
// refuses, and no row shows a password.
func TestUserEntryChecksMatchConfigureUsers(t *testing.T) {
	t.Parallel()

	roles := builtinRoles()

	rapid.Check(t, func(t *rapid.T) {
		password := rapid.StringMatching(`PW[A-Z0-9]{8,12}`).Draw(t, "password")
		entry := rapid.OneOf(
			rapid.String(),
			rapid.StringMatching(`[a-z]{0,6}(:[a-z ,-]{0,8}){0,4}`),
			rapid.Just(password),
			rapid.Map(rapid.StringMatching(`[a-z]{0,6}`), func(name string) string { return name + ":" + password }),
			rapid.Map(rapid.SampledFrom(roles), func(role string) string { return "alice:" + password + ":" + role }),
		)
		users := rapid.SliceOfN(entry, 1, 4).Draw(t, "users")

		var wantFailed []string

		for i, u := range users {
			if _, err := web.ParseDefaultUser(i+1, u); err != nil {
				wantFailed = append(wantFailed, fmt.Sprintf("default user %d ", i+1))
			}
		}

		var gotFailed []string

		for _, c := range userEntryChecks(users, roles) {
			if strings.Contains(c.Detail, password) {
				t.Fatalf("userEntryChecks(%q) row %q shows the password", users, c.Detail)
			}

			if c.Status == doctorFail && strings.HasPrefix(c.Detail, "default user ") {
				n, _, _ := strings.Cut(strings.TrimPrefix(c.Detail, "default user "), " ")
				gotFailed = append(gotFailed, "default user "+n+" ")
			}
		}

		if diff := cmp.Diff(wantFailed, gotFailed); diff != "" {
			t.Fatalf("userEntryChecks(%q) failed entries mismatch (-parser +doctor):\n%s", users, diff)
		}
	})
}

// proxy-jwt warns when phenix ui listens on every address, where clients
// can reach it past the proxy.
func TestProxyJWTCheck(t *testing.T) {
	t.Parallel()

	warn := func(endpoint string) doctorCheck {
		return warnCheck(fmt.Sprintf("phenix ui does not check the signatures of proxy JWTs and listens on every address (%q). "+
			"A client that reaches it past the proxy can claim to be any user", endpoint))
	}

	tests := map[string]doctorCheck{
		"0.0.0.0:3000":   warn("0.0.0.0:3000"),
		":3000":          warn(":3000"),
		"[::]:3000":      warn("[::]:3000"),
		"":               warn(""),
		"127.0.0.1:3000": passCheck("proxy-jwt, and phenix ui listens only on 127.0.0.1"),
		"localhost:3000": passCheck("proxy-jwt, and phenix ui listens only on localhost"),
		"3000":           passCheck("proxy-jwt, and the ui.listen-endpoint check covers the endpoint"),
	}

	for endpoint, want := range tests {
		if diff := cmp.Diff(want, proxyJWTCheck(endpoint)); diff != "" {
			t.Errorf("proxyJWTCheck(%q) mismatch (-want +got):\n%s", endpoint, diff)
		}
	}
}

// A proxy auth header warns where phenix ui ignores it, and fails when it
// is not a header name.
func TestProxyHeaderCheck(t *testing.T) {
	t.Parallel()

	env := fakeDoctorEnv(nil, false)

	tests := map[string]struct {
		header, key string
		want        doctorCheck
	}{
		"with sign-in": {header: "X-Forwarded-User", key: "secret", want: passCheck("X-Forwarded-User")},
		"with proxy JWTs": {
			header: "X-Forwarded-User", key: proxyJWTKey, want: passCheck("X-Forwarded-User"),
		},
		"without a key": {
			header: "X-Forwarded-User",
			want:   warnCheck("phenix ui ignores X-Forwarded-User: with no ui.jwt-signing-key, phenix ui requires no sign-in"),
		},
		"with a dev key": {
			header: "X-Forwarded-User", key: "dev|alice|global-admin",
			want: warnCheck(
				"phenix ui ignores X-Forwarded-User: with a dev| ui.jwt-signing-key, phenix ui uses development authentication"),
		},
		"not a header": {
			header: "X User",
			want:   failCheck(`"X User" is not an HTTP header name, so no request carries it`),
		},
	}

	for name, tc := range tests {
		if diff := cmp.Diff(tc.want, proxyHeaderCheck(env, tc.header, tc.key)); diff != "" {
			t.Errorf("%s: proxyHeaderCheck(%q, key) mismatch (-want +got):\n%s", name, tc.header, diff)
		}
	}
}

// ui.jwt-lifetime fails for any value viper reads as under a minute.
func TestJWTLifetimeCheck(t *testing.T) {
	t.Parallel()

	tests := map[string]doctorStatus{
		"24h": doctorPass, "1m": doctorPass, "59s": doctorFail, "24": doctorFail, "1d": doctorFail, "": doctorFail,
		"-1h": doctorFail, "0": doctorFail,
	}

	for value, want := range tests {
		got := jwtLifetimeCheck(value)
		if got.Status != want {
			t.Errorf("jwtLifetimeCheck(%q) = %+v, want %s", value, got, want)
		}

		if read := viperWith("k", value).GetDuration("k"); (read >= minJWTLifetime) != (got.Status == doctorPass) {
			t.Errorf("jwtLifetimeCheck(%q) = %s, but viper reads %s", value, got.Status, read)
		}
	}
}
