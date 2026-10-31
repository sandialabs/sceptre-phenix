package cmd

import (
	"fmt"
	"net"
	"slices"
	"strings"

	"phenix/web"
	"phenix/web/middleware"
)

const proxyJWTKey = "proxy-jwt"

// jwtKeyCheck reports the authentication mode the JWT signing key selects,
// never the key; roles are the built-in roles, or nil when unknown.
func jwtKeyCheck(key string, roles []string) doctorCheck {
	check := doctorCheck{Name: "ui.jwt-signing-key", Status: doctorPass, Detail: "set, so phenix ui requires sign-in"}

	switch {
	case key == "":
		check.Status, check.Detail = doctorWarn, "empty, so phenix ui serves without authentication"
	case strings.HasPrefix(key, "dev|"):
		_, role, ok := middleware.DevAuthCredentials(key)

		switch {
		case !ok:
			check.Status, check.Detail = doctorFail, "a dev| key that is not dev|<user>|<role>, so phenix ui refuses every request"
		case roles != nil && !slices.Contains(roles, role):
			check.Status, check.Detail = doctorWarn,
				"a dev| key whose role is not built in, so phenix ui refuses every request unless the store holds the role"
		default:
			check.Status, check.Detail = doctorWarn, "a dev| key, so phenix ui uses development authentication"
		}
	case key == proxyJWTKey:
		check.Detail = "proxy-jwt, so phenix ui trusts JWTs from the proxy"
	}

	return check
}

// userEntryChecks checks each ui.users entry as phenix ui reads it, and gives
// a row for each problem. A row names an entry by position and user name only.
func userEntryChecks(users, roles []string) []doctorCheck {
	var checks []doctorCheck

	for i, entry := range users {
		u, err := web.ParseDefaultUser(i+1, entry)
		label := fmt.Sprintf("user %d (%q)", i+1, u.Name)

		switch {
		case err != nil:
			checks = append(checks, failCheck(err.Error()+", so phenix ui skips it"))
		case strings.Contains(u.Role, ","):
			checks = append(checks, failCheck(fmt.Sprintf(
				"%s has a comma in its role. phenix splits %s on spaces, so separate users with spaces",
				label, settingEnvVar("ui.users"),
			)))
		case roles != nil && !slices.Contains(roles, u.Role):
			checks = append(checks, warnCheck(
				label+" has a role that is not built in, so phenix ui skips the user unless the store holds the role",
			))
		}
	}

	return checks
}

// usersCheck checks the PHENIX_UI_USERS variable, which viper splits on
// spaces, as one row, without showing any user.
func usersCheck(value string, roles []string) doctorCheck {
	users := strings.Fields(value)

	if problems := userEntryChecks(users, roles); len(problems) > 0 {
		status, details := doctorWarn, make([]string, 0, len(problems))

		for _, p := range problems {
			if p.Status == doctorFail {
				status = doctorFail
			}

			details = append(details, p.Detail)
		}

		return doctorCheck{Name: "", Status: status, Detail: strings.Join(details, ". ")}
	}

	return passCheck(userCount(len(users)))
}

func userCount(n int) string {
	if n == 1 {
		return "1 user"
	}

	return fmt.Sprintf("%d users", n)
}

// usersSettingCheck checks the users phenix ui creates, users, given the JWT
// signing key: a row for each problem entry, or one row.
func usersSettingCheck(users []string, key string, roles []string) []doctorCheck {
	if len(users) == 0 {
		if key == "" || strings.HasPrefix(key, "dev|") {
			return []doctorCheck{passCheck("empty, so phenix ui creates the default user " + web.DefaultAdminName +
				", but requires no sign-in")}
		}

		return []doctorCheck{warnCheck("empty, so phenix ui creates the default user " + web.DefaultAdminName +
			", with the password in the phenix source, when the store lacks it")}
	}

	if problems := userEntryChecks(users, roles); len(problems) > 0 {
		return problems
	}

	return []doctorCheck{passCheck(userCount(len(users)))}
}

// proxyJWTCheck warns when phenix ui, reading proxy JWTs without checking
// their signatures, listens on every address of the host.
func proxyJWTCheck(endpoint string) doctorCheck {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil && endpoint != "" {
		return passCheck("proxy-jwt, and the ui.listen-endpoint check covers the endpoint")
	}

	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		return warnCheck(fmt.Sprintf(
			"phenix ui does not check the signatures of proxy JWTs and listens on every address (%q). "+
				"A client that reaches it past the proxy can claim to be any user", endpoint,
		))
	}

	return passCheck("proxy-jwt, and phenix ui listens only on " + host)
}

// proxyHeaderCheck checks the proxy auth header, which phenix ui ignores
// without a JWT signing key or with a dev| key.
func proxyHeaderCheck(env doctorEnv, header, key string) doctorCheck {
	check, _ := settingValueCheck(env, "ui.proxy-auth-header", header)

	switch {
	case check.Status != doctorPass:
		return check
	case key == "":
		return warnCheck("phenix ui ignores " + header + ": with no ui.jwt-signing-key, phenix ui requires no sign-in")
	case strings.HasPrefix(key, "dev|"):
		return warnCheck("phenix ui ignores " + header + ": with a dev| ui.jwt-signing-key, phenix ui uses development authentication")
	}

	return check
}
