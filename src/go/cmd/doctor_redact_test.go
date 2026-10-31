package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// redactTestInput is the doctor input the redactor reads: the host name, the
// hostname suffixes, the user, ui.users, the JWT key, variables, addresses and
// the host name minimega answers from.
type redactTestInput struct {
	host, suffixes, user, home, users, key, mmHost string
	vars                                           map[string]string
	addrs, meshNodes                               []string
}

func (ti redactTestInput) input() *doctorInput {
	in := fakeDoctorInput("/nonexistent", map[string]string{
		"hostname-suffixes": ti.suffixes, "ui.users": ti.users, "ui.jwt-signing-key": ti.key,
	})

	in.env = fakeDoctorEnv(ti.vars, false)
	in.hostname = func() (string, error) {
		if ti.host == "" {
			return "", errors.New("no host name")
		}

		return ti.host, nil
	}
	in.addrs = func() ([]string, error) { return ti.addrs, nil }
	in.userName, in.home, in.phenixUID = ti.user, ti.home, ""

	if ti.mmHost != "" || ti.meshNodes != nil {
		in.found = &doctorFound{inventory: &doctorInventory{host: ti.mmHost}, mesh: &doctorMesh{nodes: ti.meshNodes}}
	}

	return in
}

// The redactor replaces the host name (keeping a hostname suffix), the user
// names, email addresses and IP addresses with tokens, and secrets and URL
// credentials with <redacted>. It keeps defaults and text that only looks alike.
func TestDoctorRedact(t *testing.T) {
	t.Parallel()

	base := redactTestInput{
		host: "ws42-phenix", suffixes: "-minimega,-phenix", user: "alice", home: "/home/alice",
		users: "bob@corp.example:hunter2:global-admin carol:Pw-3x:viewer", key: "s3cr3t-key",
		vars: map[string]string{"SUDO_USER": "erin"}, addrs: []string{"127.0.0.1/8", "192.0.2.10/24", "fe80::1/64"},
	}

	tests := map[string]struct {
		in   redactTestInput
		text string
		want string
	}{
		"host with a suffix":    {in: base, text: "ws42-phenix and ws42-minimega", want: "<host-1>-phenix and <host-1>-minimega"},
		"host in another case":  {in: base, text: "WS42 is ws42", want: "<host-1> is <host-1>"},
		"host in a longer word": {in: base, text: "ws420 xws42", want: "ws420 xws42"},
		"host in a snapshot file": {
			in: base, text: "ws42_exp1_vm1_snapshot (1.0 GiB)", want: "<host-1>_exp1_vm1_snapshot (1.0 GiB)",
		},
		"minimega host": {
			in:   redactTestInput{host: "ws42-phenix", suffixes: "-minimega,-phenix", mmHost: "mm7"},
			text: "mm7_exp1_vm1_snapshot on ws42-phenix", want: "<host-2>_exp1_vm1_snapshot on <host-1>-phenix",
		},
		"mesh nodes": {
			in:   redactTestInput{host: "ws42-phenix", suffixes: "-minimega,-phenix", meshNodes: []string{"ws42-minimega", "node3"}},
			text: "e1 VM a on node3, and ws42", want: "e1 VM a on <host-1>, and <host-2>",
		},
		"FQDN": {
			in:   redactTestInput{host: "node7.lab.example", suffixes: "-phenix"},
			text: "node7.lab.example node7", want: "<host-1> <host-1>",
		},
		"user in a path": {in: base, text: "/home/alice/images/a.qcow2", want: "/home/<user-1>/images/a.qcow2"},
		"user in a word": {in: base, text: "malice alicex", want: "malice alicex"},
		"sudo user":      {in: base, text: "SUDO_USER erin", want: "SUDO_USER <user-1>"},
		"file owner": {
			in:   base,
			text: "/x/store.bdb belongs to mallory (uid 1001), not to this user, alice (uid 1000)",
			want: "/x/store.bdb belongs to <user-1> (uid 1001), not to this user, <user-2> (uid 1000)",
		},
		"kept owner":         {in: base, text: "belongs to root (uid 0)", want: "belongs to root (uid 0)"},
		"ui.users":           {in: base, text: `user 2 ("carol") of bob@corp.example`, want: `user 2 ("<user-1>") of <email-1>`},
		"ui.users passwords": {in: base, text: "hunter2, Pw-3x", want: "<redacted>, <redacted>"},
		"JWT key":            {in: base, text: "key s3cr3t-key", want: "key <redacted>"},
		"dev key user":       {in: redactTestInput{key: "dev|dave|global-admin"}, text: "dave", want: "<user-1>"},
		"proxy-jwt stays":    {in: redactTestInput{key: proxyJWTKey}, text: "proxy-jwt, so", want: "proxy-jwt, so"},
		"email":              {in: base, text: "mail Zed.Q@Mail.example.org.", want: "mail <email-1>."},
		"IPv4 with a port":   {in: base, text: "listen on 10.1.2.3:3000, 10.1.2.3", want: "listen on <ip-1>:3000, <ip-1>"},
		"IPv6":               {in: base, text: "[2001:db8::5]:443 and fe80::1.", want: "[<ip-2>]:443 and <ip-1>."},
		"interface address":  {in: base, text: "192.0.2.10/24", want: "<ip-1>/24"},
		"default addresses": {
			in:   base,
			text: "0.0.0.0:3000 127.0.0.1 ::1 [::]:80 localhost",
			want: "0.0.0.0:3000 127.0.0.1 ::1 [::]:80 localhost",
		},
		"default users": {
			in:   base,
			text: "root phenix minimega nobody admin@foo.com",
			want: "root phenix minimega nobody admin@foo.com",
		},
		"not addresses": {
			in:   base,
			text: "12:00:00Z 6.8.0-49-generic aa:bb:cc:dd:ee:ff 999.1.1.1",
			want: "12:00:00Z 6.8.0-49-generic aa:bb:cc:dd:ee:ff 999.1.1.1",
		},
		"URL credentials": {in: base, text: "http://u:p%40ss@proxy.example:8080/", want: "http://<redacted>@proxy.example:8080/"},
		"secret-like values": {
			in:   base,
			text: "password=abc1 api_key=xyz2 TOKEN=q",
			want: "password=<redacted> api_key=<redacted> TOKEN=<redacted>",
		},
		"user format":            {in: base, text: "name:password:role[:resource...]", want: "name:password:role[:resource...]"},
		"JWT":                    {in: base, text: "Bearer eyJhbGciOi.eyJzdWIiOi.c2ln", want: "Bearer <redacted>"},
		"tokens stay":            {in: base, text: "<host-1> <redacted> <user-3>", want: "<host-1> <redacted> <user-3>"},
		"same value, same token": {in: base, text: "alice ws42 alice ws42", want: "<user-1> <host-1> <user-1> <host-1>"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newDoctorRedactor(tc.in.input())
			if diff := cmp.Diff(tc.want, r.redact(tc.text)); diff != "" {
				t.Errorf("redact(%q) mismatch (-want +got):\n%s", tc.text, diff)
			}
		})
	}
}

// --report redacts the header, every check and every setting value, but keeps
// the ids, groups, setting keys and sources, and no longer adds a hostname.
func TestDoctorReportRedacted(t *testing.T) {
	t.Parallel()

	in := redactTestInput{host: "ws42", user: "alice", home: "/home/alice"}.input()
	in.cwd = "/home/alice/topologies"

	results := []doctorResult{{
		ID: "store.endpoint", Group: doctorGroupFiles, Name: "store.endpoint", Status: doctorFail,
		Detail: "/home/alice/store.bdb on ws42 (10.0.0.9)", Hint: "ask alice",
	}, {
		ID: "store.read", Group: doctorGroupFiles, Name: "store.read", Status: doctorWarn,
		Detail: "/home/mallory/a.bdb is not readable",
	}, {
		ID: "store.owner", Group: doctorGroupFiles, Name: "store.owner", Status: doctorWarn,
		Detail: "a.bdb belongs to mallory (uid 1001)",
	}}

	report := newDoctorReport(in, results, doctorOptions{output: "text", report: true})

	if got, want := report.Environment.WorkingDir, "/home/<user-1>/topologies"; got != want {
		t.Errorf("working directory = %q, want %q", got, want)
	}

	want := []doctorResult{{
		ID: "store.endpoint", Group: doctorGroupFiles, Name: "store.endpoint", Status: doctorFail,
		Detail: "/home/<user-1>/store.bdb on <host-1> (<ip-1>)", Hint: "ask <user-1>",
	}, {
		ID: "store.read", Group: doctorGroupFiles, Name: "store.read", Status: doctorWarn,
		Detail: "/home/<user-2>/a.bdb is not readable",
	}, {
		ID: "store.owner", Group: doctorGroupFiles, Name: "store.owner", Status: doctorWarn,
		Detail: "a.bdb belongs to <user-2> (uid 1001)",
	}}
	if diff := cmp.Diff(want, report.Checks); diff != "" {
		t.Errorf("checks mismatch (-want +got):\n%s", diff)
	}

	settings := make(map[string]doctorSetting)
	for _, s := range report.Settings {
		settings[s.Key] = s
	}

	if s := settings["log.system.path"]; s.Value != "/nonexistent/phenix.log" || s.Source != sourceDefault {
		t.Errorf("log.system.path setting = %+v, want the path and its source", s)
	}

	var out bytes.Buffer
	if err := writeDoctorReport(&out, report, doctorOptions{output: "text", report: true}); err != nil {
		t.Fatalf("writeDoctorReport() error = %v", err)
	}

	for _, leak := range []string{"alice", "mallory", "ws42", "10.0.0.9", "hostname:"} {
		if strings.Contains(out.String(), leak) {
			t.Errorf("--report output contains %q:\n%s", leak, out.String())
		}
	}
}

// -p leaves only the checks that warn or fail in the JSON report, counts the
// rest in hiddenChecks, and keeps the status of every check.
func TestDoctorJSONProblemsOnly(t *testing.T) {
	t.Parallel()

	report := doctorReportBase(doctorWarn,
		doctorResult{ID: "a", Group: "g", Name: "a", Status: doctorPass, Detail: ""},
		doctorResult{ID: "b", Group: "g", Name: "b", Status: doctorWarn, Detail: "w", Hint: "h"},
		doctorResult{ID: "c", Group: "g", Name: "c", Status: doctorSkip, Detail: "s"},
	)

	tests := map[string]struct {
		problems   bool
		wantIDs    []string
		wantHidden bool
	}{
		"all":           {wantIDs: []string{"a", "b", "c"}},
		"problems only": {problems: true, wantIDs: []string{"b"}, wantHidden: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			if err := writeDoctorReport(&out, report, doctorOptions{output: FormatJSON, problems: tc.problems}); err != nil {
				t.Fatalf("writeDoctorReport() error = %v", err)
			}

			var decoded struct {
				Status string         `json:"status"`
				Checks []doctorResult `json:"checks"`
				Hidden *int           `json:"hiddenChecks"`
			}
			if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			ids := make([]string, 0, len(decoded.Checks))
			for _, c := range decoded.Checks {
				ids = append(ids, c.ID)
			}

			if diff := cmp.Diff(tc.wantIDs, ids); diff != "" {
				t.Errorf("check ids mismatch (-want +got):\n%s", diff)
			}

			if decoded.Status != string(doctorWarn) {
				t.Errorf("status = %q, want warn", decoded.Status)
			}

			switch {
			case tc.wantHidden && (decoded.Hidden == nil || *decoded.Hidden != 2):
				t.Errorf("hiddenChecks = %v, want 2", decoded.Hidden)
			case !tc.wantHidden && decoded.Hidden != nil:
				t.Errorf("hiddenChecks = %d, want none", *decoded.Hidden)
			}
		})
	}
}

// doctorRedactValue is a value of the given letters and digits, which starts
// with two letters so it never reads as a number.
func doctorRedactValue(letters string) *rapid.Generator[string] {
	return rapid.StringMatching("[" + letters + "]{2}[" + letters + "0-9]{1,6}")
}

// No known value survives redaction, in any mix with other text; the same
// value always gets the same token; redacting again changes nothing; and the
// defaults stay.
func TestDoctorRedactProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		host := doctorRedactValue("a-c").Draw(t, "host")
		user := doctorRedactValue("d-f").Draw(t, "user")
		uiUser := doctorRedactValue("p-r").Draw(t, "uiUser")
		password := rapid.StringMatching(`[s-u]{2}[s-u0-9]{1,6}[!#%]?`).Draw(t, "password")
		ip := rapid.Map(rapid.SliceOfN(rapid.IntRange(0, 255), 3, 3), func(b []int) string {
			return fmt.Sprintf("10.%d.%d.%d", b[0], b[1], b[2])
		}).Draw(t, "ip")
		suffix := rapid.SampledFrom([]string{"", "-phenix", "-minimega"}).Draw(t, "suffix")

		r := newDoctorRedactor(redactTestInput{
			host: host + suffix, suffixes: "-minimega,-phenix", user: user, home: "/home/" + user,
			users: uiUser + ":" + password + ":global-admin", addrs: []string{ip + "/24"},
		}.input())

		known := []string{host, user, uiUser, password, ip}
		kept := []string{"localhost", "127.0.0.1", "root", "phenix", "minimega", "nobody", "0.0.0.0"}
		parts := rapid.SliceOfN(rapid.OneOf(
			rapid.SampledFrom(known),
			rapid.Map(rapid.SampledFrom([]string{host}), strings.ToUpper),
			rapid.StringMatching(`[g-o]{1,5}`),
			rapid.SampledFrom(kept),
		), 1, 12).Draw(t, "parts")
		seps := rapid.SliceOfN(rapid.SampledFrom([]string{" ", "/", ":", ",", "=", "(", ")", "-", ".", "@"}),
			len(parts), len(parts)).Draw(t, "seps")

		var b strings.Builder
		for i, p := range parts {
			b.WriteString(p)
			b.WriteString(seps[i])
		}

		text := b.String()
		got := r.redact(text)

		stripped := strings.ToLower(redactToken.ReplaceAllString(got, " "))
		for _, v := range known {
			if strings.Contains(stripped, strings.ToLower(v)) {
				t.Fatalf("redact(%q) = %q keeps %q", text, got, v)
			}
		}

		if again := r.redact(got); again != got {
			t.Fatalf("redact(%q) = %q, but redact of that = %q", text, got, again)
		}

		for _, v := range []string{host, strings.ToUpper(host), user, uiUser, ip} {
			tok := r.redact(v)
			if !redactToken.MatchString(tok) || r.redact(" "+v+" ") != " "+tok+" " || r.redact(v) != tok {
				t.Fatalf("token of %q = %q, not one token that stays the same", v, tok)
			}
		}

		if r.redact(host) != r.redact(strings.ToUpper(host)) {
			t.Fatalf("host %q and %q have different tokens", host, strings.ToUpper(host))
		}

		if got := r.redact(password); got != redacted {
			t.Fatalf("redact(password %q) = %q, want %s", password, got, redacted)
		}

		keptText := strings.Join(rapid.SliceOfN(rapid.SampledFrom(append(kept, "::1", "::", "admin@foo.com")), 1, 6).
			Draw(t, "kept"), rapid.SampledFrom([]string{" ", ", ", " / ", "="}).Draw(t, "keptSep"))
		if got := r.redact(keptText); got != keptText {
			t.Fatalf("redact(%q) = %q, want the defaults kept", keptText, got)
		}
	})
}
