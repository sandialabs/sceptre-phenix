package cmd

import (
	"cmp"
	"net"
	"net/netip"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/viper"

	"phenix/web"
	"phenix/web/middleware"
)

// The kinds of value the --report redactor replaces with a numbered token.
const (
	redactHost  = "host"
	redactIP    = "ip"
	redactUser  = "user"
	redactEmail = "email"

	// redactMaxPasses bounds the passes that redact a string until it stays the same.
	redactMaxPasses = 8
)

//nolint:gochecknoglobals // constant patterns and list
var (
	// redactToken matches the tokens the redactor writes, which it never changes.
	redactToken = regexp.MustCompile(`<(?:host|ip|user|email)-[0-9]+>|<redacted>`)

	redactURLUser = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://[^\s/@]+@`)
	redactJWT     = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]*`)
	redactKeyVal  = regexp.MustCompile(
		`^(?i:[A-Za-z0-9_.-]*(?:password|passwd|secret|token|api[_-]?key|signing[_-]?key))=`)
	redactSecretVal = regexp.MustCompile(`^[^\s,;&"']+`)
	redactEmailRE   = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+`)
	redactIPv4      = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]{1,3}){3}`)
	redactIPv6      = regexp.MustCompile(`^[0-9A-Fa-f:.]*:[0-9A-Fa-f.]*:[0-9A-Fa-f:.]*`)
	redactUIDName   = regexp.MustCompile(`([A-Za-z0-9_][A-Za-z0-9_.-]*) \(uid [0-9]+`)

	// redactKept are defaults and common values that tell a reader nothing
	// about the host, so a report keeps them.
	redactKept = []string{
		"localhost", "127.0.0.1", "::1", "0.0.0.0", "::",
		"root", "phenix", "minimega", "nobody", web.DefaultAdminName,
	}
)

// redactWord is a known value the redactor replaces where it is a whole word.
type redactWord struct {
	value string
	kind  string // a token kind, or "" for <redacted>
	key   string // values with the same key get the same token
	fold  bool   // match without regard to case
}

// doctorRedactor replaces host names, IP addresses, user names, email
// addresses and secrets with tokens, such as <host-1>, for --report. The same
// value always gets the same token, so a report still shows which values match.
type doctorRedactor struct {
	words  []redactWord
	tokens map[string]string // by kind and key
	counts map[string]int    // the tokens made, by kind
}

// newDoctorRedactor collects the values to redact from in: the host names,
// such as the host of ui.listen-endpoint, the user names, the ui.users names
// and passwords, the JWT signing key and the addresses of the host's interfaces.
func newDoctorRedactor(in *doctorInput) *doctorRedactor {
	r := &doctorRedactor{words: nil, tokens: make(map[string]string), counts: make(map[string]int)}

	if in.hostname != nil {
		if name, err := in.hostname(); err == nil {
			r.addHost(name, redactSuffixes(in.text("hostname-suffixes")))
		}
	}

	// the head node in the names of snapshot files, as minimega answers
	if in.found != nil && in.found.inventory != nil && in.found.inventory.host != "" {
		r.addHost(in.found.inventory.host, redactSuffixes(in.text("hostname-suffixes")))
	}

	// the nodes of minimega's mesh, which schedule-hosts and bridge-trunk name
	if in.found != nil && in.found.mesh != nil {
		for _, node := range in.found.mesh.nodes {
			r.addHost(node, redactSuffixes(in.text("hostname-suffixes")))
		}
	}

	// the cluster nodes that VM schedules name, which schedule-hosts names when they are not in the mesh
	if in.found != nil && in.found.experiments != nil {
		for _, exp := range in.found.experiments.list {
			for _, node := range exp.schedules {
				r.addHost(node, redactSuffixes(in.text("hostname-suffixes")))
			}
		}
	}

	// the DNS names of the TLS certificate, which ui.tls-cert names
	if in.found != nil {
		for _, name := range in.found.certNames {
			r.addHost(strings.TrimPrefix(name, "*."), redactSuffixes(in.text("hostname-suffixes")))
		}
	}

	// the host of ui.listen-endpoint, which ui.tls-cert and proxy-env name
	if host, _, err := net.SplitHostPort(in.text("ui.listen-endpoint")); err == nil {
		if _, err := netip.ParseAddr(host); err == nil {
			r.addIP(host)
		} else {
			r.addHost(host, redactSuffixes(in.text("hostname-suffixes")))
		}
	}

	r.addUser(in.userName)
	r.addUser(filepath.Base(in.home))

	// the stored users that rbac-users names
	if in.found != nil {
		for _, name := range in.found.users {
			r.addUser(name)
		}
	}

	if in.env.lookupUID != nil && in.phenixUID != "" {
		if u, err := in.env.lookupUID(in.phenixUID); err == nil {
			r.addUser(u.Username)
		}
	}

	lookup := func(name string) string {
		value, _ := in.env.lookup(name)

		return value
	}

	r.addUser(lookup("SUDO_USER"))

	for _, entry := range redactUsersEntries(in, lookup) {
		creds := strings.Split(entry, ":")
		r.addUser(creds[0])

		if len(creds) > 1 {
			r.addSecret(creds[1])
		}
	}

	for _, key := range []string{in.text("ui.jwt-signing-key"), lookup(settingEnvVar("ui.jwt-signing-key"))} {
		if user, _, ok := middleware.DevAuthCredentials(key); ok {
			r.addUser(user)
		}

		if key != proxyJWTKey {
			r.addSecret(key)
		}
	}

	if in.addrs != nil {
		if addrs, err := in.addrs(); err == nil {
			for _, a := range addrs {
				r.addIP(a)
			}
		}
	}

	r.sortWords()

	return r
}

// sortWords puts the longest words first, so a word never hides a longer one.
func (r *doctorRedactor) sortWords() {
	slices.SortStableFunc(r.words, func(a, b redactWord) int { return cmp.Compare(len(b.value), len(a.value)) })
}

// learnUIDNames adds each user name that texts show as "name (uid N)", such
// as a file owner, so the report also redacts other uses of that name.
func (r *doctorRedactor) learnUIDNames(texts ...string) {
	for _, text := range texts {
		for _, m := range redactUIDName.FindAllStringSubmatchIndex(text, -1) {
			if m[0] == 0 || !isWordByte(text[m[0]-1]) {
				r.addUser(text[m[2]:m[3]])
			}
		}
	}

	r.sortWords()
}

// redactUsersEntries are the ui.users entries phenix reads, from the
// setting, the config files and PHENIX_UI_USERS.
func redactUsersEntries(in *doctorInput, lookup func(string) string) []string {
	asList := func(value any) []string {
		v := viper.New()
		v.Set("users", value)

		return v.GetStringSlice("users")
	}

	entries := asList(in.settings["ui.users"].value)
	if f, ok := in.config.values["ui.users"]; ok {
		entries = append(entries, asList(f.value)...)
	}

	return append(entries, strings.Fields(lookup(settingEnvVar("ui.users")))...)
}

// redactSuffixes are the hostname-suffixes setting's suffixes, such as -phenix.
func redactSuffixes(setting string) []string {
	var suffixes []string

	for s := range strings.SplitSeq(setting, ",") {
		if s = strings.TrimSpace(s); s != "" {
			suffixes = append(suffixes, s)
		}
	}

	return suffixes
}

// addHost adds the host name and its short name. A short name keeps a
// hostname suffix, so ws42-phenix becomes <host-1>-phenix. It adds a full
// name also when the short name is a kept value, such as phenix.
func (r *doctorRedactor) addHost(name string, suffixes []string) {
	short, _, _ := strings.Cut(name, ".")

	base := short
	for _, s := range suffixes {
		base = strings.TrimSuffix(base, s)
	}

	key := strings.ToLower(name)
	if base != "" && !redactIsKept(base) {
		key = strings.ToLower(base)
		r.add(redactWord{value: base, kind: redactHost, key: key, fold: true})
	}

	if name != short && !redactIsKept(name) {
		r.add(redactWord{value: name, kind: redactHost, key: key, fold: true})
	}
}

// addUser adds a user name; a name with @ is an email address.
func (r *doctorRedactor) addUser(name string) {
	switch {
	case name == "", name == ".", name == "/", redactIsKept(name):
	case redactEmailRE.MatchString(name):
		r.add(redactWord{value: name, kind: redactEmail, key: strings.ToLower(name), fold: true})
	default:
		r.add(redactWord{value: name, kind: redactUser, key: name, fold: false})
	}
}

func (r *doctorRedactor) addSecret(value string) {
	if value != "" && !redactIsKept(value) {
		r.add(redactWord{value: value, kind: "", key: "", fold: false})
	}
}

func (r *doctorRedactor) addIP(text string) {
	if prefix, err := netip.ParsePrefix(text); err == nil {
		text = prefix.Addr().String()
	}

	if addr, err := netip.ParseAddr(text); err == nil && !redactIsKept(addr.String()) {
		r.add(redactWord{value: addr.String(), kind: redactIP, key: addr.String(), fold: true})
	}
}

func (r *doctorRedactor) add(w redactWord) {
	if !slices.ContainsFunc(r.words, func(o redactWord) bool { return o.value == w.value }) {
		r.words = append(r.words, w)
	}
}

func redactIsKept(value string) bool {
	return slices.ContainsFunc(redactKept, func(k string) bool { return strings.EqualFold(k, value) })
}

// token returns the token of the value with key, and makes it the first time.
func (r *doctorRedactor) token(kind, key string) string {
	if kind == "" {
		return redacted
	}

	id := kind + "\x00" + key
	if tok, ok := r.tokens[id]; ok {
		return tok
	}

	r.counts[kind]++
	tok := "<" + kind + "-" + strconv.Itoa(r.counts[kind]) + ">"
	r.tokens[id] = tok

	return tok
}

// report returns report with each string redacted, apart from the check ids
// and groups, the setting keys and sources, and the statuses, which phenix sets.
func (r *doctorRedactor) report(report doctorReport) doctorReport {
	s := r.redact

	for _, c := range report.Checks {
		r.learnUIDNames(c.Name, c.Detail, c.Hint)
	}

	v, env := &report.Version, &report.Environment
	for _, field := range []*string{
		&report.Time, &v.Phenix, &v.Label, &v.Commit, &v.Date, &v.Minimega,
		&env.OS, &env.Kernel, &env.Arch, &env.User, &env.WorkingDir, &env.ConfigFile, &env.ContainerHint,
	} {
		*field = s(*field)
	}

	checks := make([]doctorResult, len(report.Checks))
	for i, c := range report.Checks {
		c.Name, c.Detail, c.Hint = s(c.Name), s(c.Detail), s(c.Hint)
		checks[i] = c
	}

	settings := make([]doctorSetting, len(report.Settings))
	for i, setting := range report.Settings {
		setting.Value = r.value(setting.Value)
		settings[i] = setting
	}

	report.Checks, report.Settings = checks, settings

	return report
}

// value redacts a setting's value: a string, or each string of a list.
func (r *doctorRedactor) value(value any) any {
	switch v := value.(type) {
	case string:
		return r.redact(v)
	case []string:
		out := make([]string, len(v))
		for i, e := range v {
			out[i] = r.redact(e)
		}

		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = r.value(e)
		}

		return out
	}

	return value
}

// redact replaces each value to hide in text with its token, pass after
// pass, until a pass changes nothing, so redacting twice changes nothing more.
func (r *doctorRedactor) redact(text string) string {
	for range redactMaxPasses {
		next := r.pass(text)
		if next == text {
			break
		}

		text = next
	}

	return text
}

// pass redacts text once: URL credentials and secrets first, then email
// addresses, "name (uid N)" names, the known values from the longest, and IP addresses.
func (r *doctorRedactor) pass(text string) string {
	stages := []func(string) string{
		func(s string) string { return redactScan(s, false, r.urlUser) },
		func(s string) string { return redactScan(s, true, r.jwt) },
		func(s string) string { return redactScan(s, false, r.keyValue) },
		func(s string) string { return redactScan(s, true, r.email) },
		func(s string) string { return redactScan(s, true, r.uidName) },
	}

	for _, w := range r.words {
		stages = append(stages, func(s string) string { return redactScan(s, true, r.word(w)) })
	}

	stages = append(stages,
		func(s string) string { return redactScan(s, true, r.ipv6) },
		func(s string) string { return redactScan(s, true, r.ipv4) },
	)

	for _, stage := range stages {
		text = redactOutsideTokens(text, stage)
	}

	return text
}

// redactOutsideTokens applies f to each part of text between tokens.
func redactOutsideTokens(text string, f func(string) string) string {
	var b strings.Builder

	at := 0
	for _, loc := range redactToken.FindAllStringIndex(text, -1) {
		b.WriteString(f(text[at:loc[0]]))
		b.WriteString(text[loc[0]:loc[1]])
		at = loc[1]
	}

	b.WriteString(f(text[at:]))

	return b.String()
}

// redactScan tries match at each word boundary of s (at each byte, unless
// words), and writes what match gives for the n bytes it takes.
func redactScan(s string, words bool, match func(s string) (string, int)) string {
	var b strings.Builder

	for i := 0; i < len(s); {
		if !words || i == 0 || !isWordByte(s[i-1]) || !isWordByte(s[i]) {
			if out, n := match(s[i:]); n > 0 {
				b.WriteString(out)
				i += n

				continue
			}
		}

		b.WriteByte(s[i])
		i++
	}

	return b.String()
}

// isWordByte reports whether c can be part of a word: an ASCII letter, digit
// or underscore, or a byte of a non-ASCII character.
func isWordByte(c byte) bool {
	return c == '_' || c >= 0x80 || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// endsWord reports whether s[:n] ends at a word boundary: not between two word bytes.
func endsWord(s string, n int) bool {
	return n == len(s) || n == 0 || !isWordByte(s[n-1]) || !isWordByte(s[n])
}

func (r *doctorRedactor) urlUser(s string) (string, int) {
	loc := redactURLUser.FindStringIndex(s)
	if loc == nil {
		return "", 0
	}

	scheme, _, _ := strings.Cut(s[:loc[1]], "://")

	return scheme + "://" + redacted + "@", loc[1]
}

func (r *doctorRedactor) jwt(s string) (string, int) {
	if loc := redactJWT.FindStringIndex(s); loc != nil && endsWord(s, loc[1]) {
		return redacted, loc[1]
	}

	return "", 0
}

// keyValue redacts the value of a secret-like key=value pair, such as password=x.
func (r *doctorRedactor) keyValue(s string) (string, int) {
	loc := redactKeyVal.FindStringIndex(s)
	if loc == nil {
		return "", 0
	}

	val := redactSecretVal.FindStringIndex(s[loc[1]:])
	if val == nil {
		return "", 0
	}

	return s[:loc[1]] + redacted, loc[1] + val[1]
}

func (r *doctorRedactor) email(s string) (string, int) {
	loc := redactEmailRE.FindStringIndex(s)
	if loc == nil || !endsWord(s, loc[1]) || redactIsKept(s[:loc[1]]) {
		return "", 0
	}

	return r.token(redactEmail, strings.ToLower(s[:loc[1]])), loc[1]
}

// uidName redacts the user name in "name (uid N)", such as a file owner.
func (r *doctorRedactor) uidName(s string) (string, int) {
	m := redactUIDName.FindStringSubmatchIndex(s)
	if m == nil || m[0] != 0 {
		return "", 0
	}

	name := s[m[2]:m[3]]
	if redactIsKept(name) || redactEmailRE.MatchString(name) {
		return "", 0
	}

	return r.token(redactUser, name) + s[m[3]:m[1]], m[1]
}

// word matches w where it is a whole word. A host name can also come before
// '_', as in the names of snapshot files.
func (r *doctorRedactor) word(w redactWord) func(string) (string, int) {
	return func(s string) (string, int) {
		n := len(w.value)
		if n > len(s) || !endsWord(s, n) && (w.kind != redactHost || s[n] != '_') {
			return "", 0
		}

		if s[:n] == w.value || (w.fold && strings.EqualFold(s[:n], w.value)) {
			return r.token(w.kind, w.key), n
		}

		return "", 0
	}
}

func (r *doctorRedactor) ipv4(s string) (string, int) {
	loc := redactIPv4.FindStringIndex(s)
	if loc == nil || !endsWord(s, loc[1]) {
		return "", 0
	}

	return r.ip(s[:loc[1]])
}

// ipv6 takes the longest start of an IPv6-like run that parses as an address.
func (r *doctorRedactor) ipv6(s string) (string, int) {
	loc := redactIPv6.FindStringIndex(s)
	if loc == nil {
		return "", 0
	}

	for n := loc[1]; n > 1; n-- {
		if endsWord(s, n) {
			if out, taken := r.ip(s[:n]); taken > 0 {
				return out, taken
			}
		}
	}

	return "", 0
}

func (r *doctorRedactor) ip(text string) (string, int) {
	addr, err := netip.ParseAddr(text)
	if err != nil || addr.Zone() != "" {
		return "", 0
	}

	if redactIsKept(addr.String()) {
		return text, len(text)
	}

	return r.token(redactIP, addr.String()), len(text)
}

// interfaceAddrs is the addresses of the host's network interfaces.
func interfaceAddrs() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err //nolint:wrapcheck // the redactor only skips them
	}

	out := make([]string, len(addrs))
	for i, a := range addrs {
		out[i] = a.String()
	}

	return out, nil
}
