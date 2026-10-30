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

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() error = %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "phenix.test"},
		DNSNames:     []string{"phenix.test"},
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

// TLS needs both a key and a certificate that load as a pair; a certificate
// expired or not yet valid fails, and one expiring within 30 days warns.
func TestTLSCheck(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	day := 24 * time.Hour
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	key, cert, keyPEM := writeTestKeyPair(t, dir, "good", now.Add(-day), now.Add(365*day))
	soonKey, soonCert, _ := writeTestKeyPair(t, dir, "soon", now.Add(-day), now.Add(10*day))
	oldKey, oldCert, _ := writeTestKeyPair(t, dir, "old", now.Add(-30*day), now.Add(-day))
	newKey, newCert, _ := writeTestKeyPair(t, dir, "new", now.Add(day), now.Add(30*day))
	otherKey, _, _ := writeTestKeyPair(t, dir, "other", now.Add(-day), now.Add(365*day))
	missing := filepath.Join(dir, "missing.crt")

	tests := map[string]struct {
		key, cert string
		want      doctorCheck
		prefix    bool // want.Detail is a prefix, before an error text that varies by Go version
	}{
		"not set":   {want: passCheck("not set, so phenix ui serves plain HTTP")},
		"only key":  {key: key, want: failCheck("ui.tls-key is set without ui.tls-cert, so phenix ui serves plain HTTP")},
		"only cert": {cert: cert, want: failCheck("ui.tls-cert is set without ui.tls-key, so phenix ui serves plain HTTP")},
		"valid":     {key: key, cert: cert, want: passCheck(cert + ", valid until 2027-10-30")},
		"expiring":  {key: soonKey, cert: soonCert, want: warnCheck("the certificate in " + soonCert + " expires on 2026-11-09")},
		"expired":   {key: oldKey, cert: oldCert, want: failCheck("the certificate in " + oldCert + " expired on 2026-10-29")},
		"not yet valid": {
			key: newKey, cert: newCert, want: failCheck("the certificate in " + newCert + " is not valid until 2026-10-31"),
		},
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

			got := env.tlsCheck(tc.key, tc.cert, now)
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

		name := rapid.StringMatching(`[a-z][a-z0-9-]{0,10}`).Draw(t, "service")
		got := listenCheck(doctorEnvLookupPort, host+":"+name)

		_, err := doctorEnvLookupPort(name)
		if (got.Status == doctorPass) != (err == nil) || (got.Status != doctorPass && got.Status != doctorFail) {
			t.Fatalf("listenCheck(%s:%s) = %+v, lookup error %v", host, name, got, err)
		}
	})
}
