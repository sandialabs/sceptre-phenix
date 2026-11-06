package cmd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// writeTestKeyPair writes a new key and a certificate for it valid from
// notBefore to notAfter under dir, and returns their paths and the key's PEM.
func writeTestKeyPair(t *testing.T, dir, name string, notBefore, notAfter time.Time) (string, string, string) {
	t.Helper()

	return writeTestKeyPairNames(t, dir, name, notBefore, notAfter, []string{"phenix.test"}, nil)
}

// writeTestKeyPairNames is writeTestKeyPair with a certificate for the DNS names dns and the addresses ips.
func writeTestKeyPairNames(
	t *testing.T, dir, name string, notBefore, notAfter time.Time, dns []string, ips []net.IP,
) (string, string, string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "phenix.test"},
		DNSNames:     dns,
		IPAddresses:  ips,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() error = %v", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("x509.MarshalPKCS8PrivateKey() error = %v", err)
	}

	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Headers: nil, Bytes: keyDER}))
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: der}))

	return writeTestFile(t, dir, name+".key", keyPEM), writeTestFile(t, dir, name+".crt", certPEM), keyPEM
}

// TLS needs both a key and a certificate that load as a pair, whatever the dates of the certificate.
func TestTLSCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := 24 * time.Hour
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	key, cert, keyPEM := writeTestKeyPair(t, dir, "good", now.Add(-day), now.Add(365*day))
	oldKey, oldCert, _ := writeTestKeyPair(t, dir, "old", now.Add(-30*day), now.Add(-day))
	otherKey, _, _ := writeTestKeyPair(t, dir, "other", now.Add(-day), now.Add(365*day))
	missing := filepath.Join(dir, "missing.crt")
	missingKey := filepath.Join(dir, "missing.key")

	tests := map[string]struct {
		key, cert  string
		unreadable string // readFile fails for this path after stat and access pass
		want       doctorCheck
		prefix     bool // want.Detail is a prefix, before an error text that varies by Go version
	}{
		"missing key":      {key: missingKey, cert: cert, want: failCheck(missingKey + " does not exist")},
		"cert a directory": {key: key, cert: dir, want: failCheck(dir + " is not a regular file")},
		"cert unreadable": {
			key: key, cert: cert, unreadable: cert, want: failCheck("reading " + cert + ": permission denied"),
		},
		"key unreadable": {
			key: key, cert: cert, unreadable: key, want: failCheck("reading " + key + ": permission denied"),
		},
		"not set":      {want: passCheck("not set, so phenix ui serves plain HTTP")},
		"only key":     {key: key, want: failCheck("ui.tls-key is set without ui.tls-cert, so phenix ui serves plain HTTP")},
		"only cert":    {cert: cert, want: failCheck("ui.tls-cert is set without ui.tls-key, so phenix ui serves plain HTTP")},
		"valid":        {key: key, cert: cert, want: passCheck(cert + " and " + key + " load as a key pair")},
		"expired":      {key: oldKey, cert: oldCert, want: passCheck(oldCert + " and " + oldKey + " load as a key pair")},
		"missing cert": {key: key, cert: missing, want: failCheck(missing + " does not exist")},
		"another key": {
			key: otherKey, cert: cert, want: failCheck("phenix ui cannot load the key pair: tls: private key does not match public key"),
		},
		"swapped": {key: cert, cert: key, want: failCheck("phenix ui cannot load the key pair: tls: "), prefix: true},
	}

	env := fakeDoctorEnv(nil, false)
	env.readFile = os.ReadFile

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := env
			if tc.unreadable != "" {
				env.readFile = func(path string) ([]byte, error) {
					if path == tc.unreadable {
						return nil, os.ErrPermission
					}

					return os.ReadFile(path)
				}
			}

			got := env.tlsCheck(tc.key, tc.cert)
			if tc.prefix && strings.HasPrefix(got.Detail, tc.want.Detail) {
				got.Detail = tc.want.Detail
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("tlsCheck(%q, %q) mismatch (-want +got):\n%s", tc.key, tc.cert, diff)
			}

			for line := range strings.SplitSeq(keyPEM, "\n") {
				if len(line) > 16 && !strings.HasPrefix(line, "-----") && strings.Contains(got.Detail, line) {
					t.Errorf("tlsCheck(%q, %q) = %q, which shows key material", tc.key, tc.cert, got.Detail)
				}
			}
		})
	}
}

// ui.tls-cert fails for a certificate that expired or is not valid yet, and warns for one that expires within 30 days,
// for one whose names do not cover the host that users reach, and for one with no DNS or IP names. It gives no row
// when TLS is off, and skips when the pair does not load.
func TestTLSCertCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := 24 * time.Hour
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	year := now.Add(365 * day)
	addr := net.ParseIP("192.0.2.10")

	pair := func(name string, from, to time.Time, dns []string, ips ...net.IP) [2]string {
		key, cert, _ := writeTestKeyPairNames(t, dir, name, from, to, dns, ips)

		return [2]string{key, cert}
	}

	good := pair("good", now.Add(-day), year, []string{"ws42.example.com", "*.lab.example.com"}, addr)
	soon := pair("soon", now.Add(-day), now.Add(10*day), []string{"ws42.example.com"})
	old := pair("old", now.Add(-30*day), now.Add(-day), []string{"ws42.example.com"})
	early := pair("early", now.Add(day), year, []string{"ws42.example.com"})
	other := pair("other", now.Add(-day), year, []string{"other.example.com"})
	bare := pair("bare", now.Add(-day), year, nil)
	otherKey := pair("otherkey", now.Add(-day), year, nil)

	names := "It names ws42.example.com, *.lab.example.com, 192.0.2.10"

	tests := map[string]struct {
		pair     [2]string
		listen   string
		hostname string
		want     []doctorCheck
	}{
		"off": {},
		"pair does not load": {
			pair: [2]string{otherKey[0], good[1]},
			want: []doctorCheck{row("", doctorSkip, "not checked, because the ui.tls row fails")},
		},
		"host name by its short name": {
			pair: good, hostname: "ws42", listen: "0.0.0.0:3000",
			want: []doctorCheck{row("", doctorPass, good[1]+" is valid until 2027-10-30. "+names+
				". They cover ws42 (the host name of this host)")},
		},
		"listen address": {
			pair: good, hostname: "ws42", listen: "192.0.2.10:443",
			want: []doctorCheck{row("", doctorPass, good[1]+" is valid until 2027-10-30. "+names+
				". They cover 192.0.2.10 (the host of ui.listen-endpoint)")},
		},
		"wildcard": {
			pair: good, listen: "ui.lab.example.com:443",
			want: []doctorCheck{row("", doctorPass, good[1]+" is valid until 2027-10-30. "+names+
				". They cover ui.lab.example.com (the host of ui.listen-endpoint)")},
		},
		"host name not known": {
			pair: good, listen: ":3000",
			want: []doctorCheck{row("", doctorPass, good[1]+" is valid until 2027-10-30. "+names+
				". The names are not checked, because the host name is not known")},
		},
		"name not covered": {
			pair: other, hostname: "ws42.example.com",
			want: []doctorCheck{row("", doctorWarn, other[1]+" is valid until 2027-10-30. It names other.example.com. "+
				"They do not cover ws42.example.com (the host name of this host), so browsers refuse the connection")},
		},
		"expires soon": {
			pair: soon, hostname: "ws42",
			want: []doctorCheck{row("", doctorWarn, "the certificate in "+soon[1]+" expires on 2026-11-09. "+
				"It names ws42.example.com. They cover ws42 (the host name of this host)")},
		},
		"expired": {
			pair: old, hostname: "ws42",
			want: []doctorCheck{row("", doctorFail, "the certificate in "+old[1]+" expired on 2026-10-29. "+
				"It names ws42.example.com. They cover ws42 (the host name of this host)")},
		},
		"not valid yet and not covered": {
			pair: early, hostname: "other",
			want: []doctorCheck{row("", doctorFail, "the certificate in "+early[1]+" is not valid until 2026-10-31. "+
				"It names ws42.example.com. They do not cover other (the host name of this host), so browsers "+
				"refuse the connection")},
		},
		"no names": {
			pair: bare, hostname: "ws42",
			want: []doctorCheck{row("", doctorWarn, bare[1]+" is valid until 2027-10-30. "+
				"It has no DNS or IP names, and browsers ignore its common name")},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := fakeDoctorInput(newDoctorTestDir(t), map[string]string{
				"ui.tls-key": tc.pair[0], "ui.tls-cert": tc.pair[1], "ui.listen-endpoint": tc.listen,
			})
			in.env.readFile = os.ReadFile
			in.found = &doctorFound{}
			in.hostname = func() (string, error) {
				if tc.hostname == "" {
					return "", os.ErrNotExist
				}

				return tc.hostname, nil
			}

			got := tlsCertCheck(in)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("tlsCertCheck() mismatch (-want +got):\n%s", diff)
			}

			for _, c := range got {
				wantHint := ""

				switch {
				case c.Status == doctorSkip || c.Status == doctorPass:
				case strings.Contains(c.Detail, "not cover") || strings.Contains(c.Detail, "no DNS"):
					wantHint = "make a certificate whose DNS or IP names include the host that users reach, then restart phenix ui"
				default:
					wantHint = "renew the certificate, then restart phenix ui"
				}

				if got := doctorHintOf(c, tlsCertHint); got != wantHint {
					t.Errorf("tlsCertHint() = %q, want %q", got, wantHint)
				}
			}
		})
	}
}

// --report replaces the DNS names of the certificate, which ui.tls-cert names.
func TestTLSCertRedacted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := 24 * time.Hour
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	key, cert, _ := writeTestKeyPairNames(t, dir, "lab", now.Add(-day), now.Add(365*day),
		[]string{"ui.secretlab.example", "*.corp.example"}, nil)

	in := fakeDoctorInput(newDoctorTestDir(t), map[string]string{"ui.tls-key": key, "ui.tls-cert": cert})
	in.env.readFile = os.ReadFile
	in.found = &doctorFound{}

	rows := tlsCertCheck(in)
	if len(rows) != 1 {
		t.Fatalf("tlsCertCheck() = %v, want one row", rows)
	}

	got := newDoctorRedactor(in).redact(rows[0].Detail)
	for _, name := range []string{"secretlab", "corp.example"} {
		if strings.Contains(got, name) {
			t.Errorf("redact(%q) = %q, which keeps %s", rows[0].Detail, got, name)
		}
	}
}

// The host users reach is the host of ui.listen-endpoint, unless that is
// every address or not host:port; then it is this host's name, if known.
func TestDoctorListenTLSTarget(t *testing.T) {
	t.Parallel()

	const listenWhat, hostWhat = "the host of ui.listen-endpoint", "the host name of this host"

	named := func(name string, err error) func() (string, error) {
		return func() (string, error) { return name, err }
	}

	tests := map[string]struct {
		listen   string
		hostname func() (string, error)
		want     [2]string
	}{
		"address": {
			listen: "192.0.2.10:443", hostname: named("ws42", nil), want: [2]string{"192.0.2.10", listenWhat},
		},
		"IPv6 address": {listen: "[2001:db8::1]:443", want: [2]string{"2001:db8::1", listenWhat}},
		"name":         {listen: "ui.example.com:443", want: [2]string{"ui.example.com", listenWhat}},
		"every IPv4 address": {
			listen: "0.0.0.0:3000", hostname: named("ws42", nil), want: [2]string{"ws42", hostWhat},
		},
		"every IPv6 address": {
			listen: "[::]:3000", hostname: named("ws42", nil), want: [2]string{"ws42", hostWhat},
		},
		"no host":                {listen: ":3000", hostname: named("ws42", nil), want: [2]string{"ws42", hostWhat}},
		"not host:port":          {listen: "3000", hostname: named("ws42", nil), want: [2]string{"ws42", hostWhat}},
		"empty":                  {hostname: named("ws42", nil), want: [2]string{"ws42", hostWhat}},
		"no host name reader":    {listen: "0.0.0.0:3000"},
		"host name fails":        {listen: "0.0.0.0:3000", hostname: named("ws42", os.ErrPermission)},
		"empty host name":        {listen: ":3000", hostname: named("", nil)},
		"every address, no name": {listen: "[::]:443"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			in := &doctorInput{
				settings: map[string]resolvedSetting{"ui.listen-endpoint": {text: tc.listen}},
				hostname: tc.hostname,
			}

			host, what := tlsTarget(in)
			if diff := cmp.Diff(tc.want, [2]string{host, what}); diff != "" {
				t.Errorf("tlsTarget() with ui.listen-endpoint %q mismatch (-want +got):\n%s", tc.listen, diff)
			}
		})
	}
}

// A certificate covers a host its names verify for, and a host name without
// a domain whose full name it holds; never an IP address or a full name it lacks.
func TestDoctorListenCertCovers(t *testing.T) {
	t.Parallel()

	cert := &x509.Certificate{
		DNSNames:    []string{"ws42.example.com", "*.lab.example.com", "localhost"},
		IPAddresses: []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::1")},
	}

	tests := map[string]bool{
		"ws42.example.com":    true,
		"WS42.Example.com":    true,
		"ws42":                true,
		"WS42":                true,
		"ui.lab.example.com":  true,
		"localhost":           true,
		"192.0.2.10":          true,
		"2001:db8::1":         true,
		"ws4":                 false,
		"lab":                 false,
		"example":             false,
		"other.example.com":   false,
		"a.b.lab.example.com": false,
		"192.0.2.11":          false,
		"2001:db8::2":         false,
		"":                    false,
	}

	for host, want := range tests {
		if got := certCovers(cert, host); got != want {
			t.Errorf("certCovers(%v, %q) = %t, want %t", cert.DNSNames, host, got, want)
		}
	}

	if certCovers(&x509.Certificate{}, "ws42") {
		t.Error(`certCovers(no names, "ws42") = true, want false`)
	}
}

// certNames is the DNS names, then the IP addresses, and leaves the
// certificate as it was.
func TestDoctorListenCertNames(t *testing.T) {
	t.Parallel()

	dns := make([]string, 1, 4)
	dns[0] = "ws42.example.com"
	cert := &x509.Certificate{DNSNames: dns, IPAddresses: []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("::1")}}

	want := []string{"ws42.example.com", "192.0.2.10", "::1"}
	if diff := cmp.Diff(want, certNames(cert)); diff != "" {
		t.Errorf("certNames() mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff([]string{"ws42.example.com"}, cert.DNSNames); diff != "" || dns[:2][1] != "" {
		t.Errorf("certNames() changed the certificate's DNS names (-want +got):\n%s", diff)
	}

	if got := certNames(&x509.Certificate{}); len(got) != 0 {
		t.Errorf("certNames(no names) = %v, want none", got)
	}
}

// worseStatus is the worse of two statuses, in either order: fail, then
// warn, then pass.
func TestDoctorListenWorseStatus(t *testing.T) {
	t.Parallel()

	ranked := []doctorStatus{doctorPass, doctorWarn, doctorFail}

	for ra, a := range ranked {
		for rb, b := range ranked {
			want := a
			if rb > ra {
				want = b
			}

			if got := worseStatus(a, b); got != want {
				t.Errorf("worseStatus(%s, %s) = %s, want %s", a, b, got, want)
			}
		}
	}
}

// doctorEnvLookupPort knows only the TCP service http, so the tests do not
// depend on the host's /etc/services.
func doctorEnvLookupPort(service string) (int, error) {
	if service == "http" {
		return 80, nil
	}

	return 0, &net.DNSError{Err: "unknown port", Name: "tcp/" + service, IsNotFound: true}
}

// An empty listen endpoint is the HTTP or HTTPS port, and any other value
// is checked as the environment variable is: a port of 1 to 65535 or a
// service name passes, port 0 or none warns, and any other port fails.
func TestListenEndpointCheck(t *testing.T) {
	t.Parallel()

	const (
		outOfRange = "is out of range 1-65535, so phenix ui cannot listen on it"
		random     = " has no port, so phenix ui listens on a random one"
	)

	tests := map[string]doctorCheck{
		"":                 passCheck("empty, so phenix ui listens on port 80, or on port 443 with TLS"),
		"0.0.0.0:3000":     passCheck("0.0.0.0:3000"),
		"3000":             failCheck(`"3000" is not host:port, so phenix ui cannot listen on it`),
		"0.0.0.0:http":     passCheck("0.0.0.0:http"),
		"0.0.0.0:nosuch":   failCheck(`port "nosuch" is not a number or service name, so phenix ui cannot listen on it`),
		"0.0.0.0:-1":       failCheck("port -1 " + outOfRange),
		"0.0.0.0:0":        warnCheck("0.0.0.0:0" + random),
		"0.0.0.0:":         warnCheck("0.0.0.0:" + random),
		"0.0.0.0:1":        passCheck("0.0.0.0:1"),
		"0.0.0.0:65535":    passCheck("0.0.0.0:65535"),
		"0.0.0.0:65536":    failCheck("port 65536 " + outOfRange),
		"[::1]:8080":       passCheck("[::1]:8080"),
		"localhost:8080:1": failCheck(`"localhost:8080:1" is not host:port, so phenix ui cannot listen on it`),
	}

	for value, want := range tests {
		if diff := cmp.Diff(want, listenEndpointCheck(doctorEnvLookupPort, value)); diff != "" {
			t.Errorf("listenEndpointCheck(%q) mismatch (-want +got):\n%s", value, diff)
		}
	}
}

// Whatever the port, a number passes in 1-65535, warns at 0 and fails
// outside; a name passes exactly when it is a service name.
func TestListenCheckProperty(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		host := rapid.SampledFrom([]string{"", "0.0.0.0", "127.0.0.1", "[::]", "phenix.test"}).Draw(t, "host")

		if rapid.Bool().Draw(t, "number") {
			n := rapid.IntRange(-2*maxPort, 2*maxPort).Draw(t, "port")
			got := listenCheck(doctorEnvLookupPort, host+":"+strconv.Itoa(n))

			want := doctorFail
			if n == 0 {
				want = doctorWarn
			} else if n > 0 && n <= maxPort {
				want = doctorPass
			}

			if got.Status != want {
				t.Fatalf("listenCheck(%s:%d) = %+v, want %s", host, n, got, want)
			}

			return
		}

		name := rapid.OneOf(rapid.Just("http"), rapid.StringMatching(`[a-z][a-z0-9-]{0,10}`)).Draw(t, "service")
		got := listenCheck(doctorEnvLookupPort, host+":"+name)

		_, err := doctorEnvLookupPort(name)
		if (got.Status == doctorPass) != (err == nil) || (got.Status != doctorPass && got.Status != doctorFail) {
			t.Fatalf("listenCheck(%s:%s) = %+v, lookup error %v", host, name, got, err)
		}
	})
}
