package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

const (
	// certExpiryWarning is how close to its expiry a TLS certificate warns.
	certExpiryWarning = 30 * 24 * time.Hour
	tlsCertName       = "ui.tls-cert"

	// Phrases in the details of rows, which their hints look for.
	certDatePhrase  = "the certificate in"
	certNamesPhrase = "do not cover"
)

// tlsCheck checks the TLS key and certificate phenix ui serves HTTPS with,
// never showing the key. With only one set, it serves plain HTTP.
func (e doctorEnv) tlsCheck(keyPath, certPath string) doctorCheck {
	_, check := e.tlsLeaf(keyPath, certPath)

	return check
}

// tlsLeaf loads the key pair as phenix ui does, and returns its certificate, or nil and the check that says why not.
func (e doctorEnv) tlsLeaf(keyPath, certPath string) (*x509.Certificate, doctorCheck) {
	switch {
	case keyPath == "" && certPath == "":
		return nil, passCheck("not set, so phenix ui serves plain HTTP")
	case certPath == "":
		return nil, failCheck("ui.tls-key is set without ui.tls-cert, so phenix ui serves plain HTTP")
	case keyPath == "":
		return nil, failCheck("ui.tls-cert is set without ui.tls-key, so phenix ui serves plain HTTP")
	}

	for _, path := range []string{certPath, keyPath} {
		if check := e.readableFileCheck(path, doctorFail); check.Status != doctorPass {
			return nil, check
		}
	}

	certPEM, err := e.readFile(certPath)
	if err != nil {
		return nil, failCheck(fmt.Sprintf("reading %s: %v", certPath, err))
	}

	keyPEM, err := e.readFile(keyPath)
	if err != nil {
		return nil, failCheck(fmt.Sprintf("reading %s: %v", keyPath, err))
	}

	// crypto/tls errors name PEM block types, never their contents
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, failCheck(fmt.Sprintf("phenix ui cannot load the key pair: %v", err))
	}

	leaf := pair.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(pair.Certificate[0]); err != nil {
			return nil, failCheck(fmt.Sprintf("parsing %s: %v", certPath, err))
		}
	}

	return leaf, passCheck(certPath + " and " + keyPath + " load as a key pair")
}

// tlsCertCheck checks the dates of the certificate phenix ui serves and whether its names cover the host that users
// reach. It gives no row when TLS is off, and records the names for --report.
func tlsCertCheck(in *doctorInput) []doctorCheck {
	keyPath, certPath := in.text("ui.tls-key"), in.text("ui.tls-cert")
	if keyPath == "" && certPath == "" {
		return nil
	}

	leaf, _ := in.env.tlsLeaf(keyPath, certPath)
	if leaf == nil {
		return []doctorCheck{hostSkip("the ui.tls row fails")}
	}

	names := certNames(leaf)
	if in.found != nil {
		in.found.certNames = append(in.found.certNames, leaf.DNSNames...)
	}

	status, parts := doctorPass, []string(nil)
	day := func(t time.Time) string { return t.UTC().Format(time.DateOnly) }
	now := in.now()

	switch {
	case now.After(leaf.NotAfter):
		status = doctorFail
		parts = append(parts, fmt.Sprintf("%s %s expired on %s", certDatePhrase, certPath, day(leaf.NotAfter)))
	case now.Before(leaf.NotBefore):
		status = doctorFail
		parts = append(parts, fmt.Sprintf("%s %s is not valid until %s", certDatePhrase, certPath, day(leaf.NotBefore)))
	case leaf.NotAfter.Sub(now) < certExpiryWarning:
		status = doctorWarn
		parts = append(parts, fmt.Sprintf("%s %s expires on %s", certDatePhrase, certPath, day(leaf.NotAfter)))
	default:
		parts = append(parts, fmt.Sprintf("%s is valid until %s", certPath, day(leaf.NotAfter)))
	}

	if len(names) == 0 {
		parts = append(parts, "It has no DNS or IP names, and browsers ignore its common name")

		return []doctorCheck{{Name: "", Status: worseStatus(status, doctorWarn), Detail: strings.Join(parts, ". ")}}
	}

	parts = append(parts, "It names "+strings.Join(names, ", "))

	target, what := tlsTarget(in)

	switch {
	case target == "":
		parts = append(parts, "The names are not checked, because the host name is not known")
	case certCovers(leaf, target):
		parts = append(parts, fmt.Sprintf("They cover %s (%s)", target, what))
	default:
		status = worseStatus(status, doctorWarn)
		parts = append(parts, fmt.Sprintf("They %s %s (%s), so browsers refuse the connection", certNamesPhrase, target, what))
	}

	return []doctorCheck{{Name: "", Status: status, Detail: strings.Join(parts, ". ")}}
}

// tlsTarget is the host that users reach phenix ui on: the host of ui.listen-endpoint, or this host's name when phenix
// ui listens on every address. It returns "" when it does not know the host name.
func tlsTarget(in *doctorInput) (string, string) {
	if host, _, err := net.SplitHostPort(in.text("ui.listen-endpoint")); err == nil && host != "" {
		if ip := net.ParseIP(host); ip == nil || !ip.IsUnspecified() {
			return host, "the host of ui.listen-endpoint"
		}
	}

	if in.hostname == nil {
		return "", ""
	}

	name, err := in.hostname()
	if err != nil || name == "" {
		return "", ""
	}

	return name, "the host name of this host"
}

// certNames is the DNS and IP names of cert.
func certNames(cert *x509.Certificate) []string {
	names := slices.Clone(cert.DNSNames)
	for _, ip := range cert.IPAddresses {
		names = append(names, ip.String())
	}

	return names
}

// certCovers reports whether cert is valid for host. A host name without a domain also matches a DNS name whose first
// label it is, because users can reach the host by its full name.
func certCovers(cert *x509.Certificate, host string) bool {
	if cert.VerifyHostname(host) == nil {
		return true
	}

	if strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return false
	}

	return slices.ContainsFunc(cert.DNSNames, func(name string) bool {
		label, _, ok := strings.Cut(name, ".")

		return ok && strings.EqualFold(label, host)
	})
}

// worseStatus is the worse of a and b: fail, then warn, then pass.
func worseStatus(a, b doctorStatus) doctorStatus {
	switch {
	case a == doctorFail || b == doctorFail:
		return doctorFail
	case a == doctorWarn || b == doctorWarn:
		return doctorWarn
	}

	return doctorPass
}

// tlsCertHint is what to do about a ui.tls-cert row that warns or fails.
func tlsCertHint(c doctorCheck) string {
	switch {
	case c.Status == doctorSkip:
		return ""
	case strings.Contains(c.Detail, certNamesPhrase) || strings.Contains(c.Detail, "no DNS or IP names"):
		return "make a certificate whose DNS or IP names include the host that users reach, then restart phenix ui"
	}

	return "renew the certificate, then restart phenix ui"
}

// listenEndpointCheck checks the endpoint phenix ui listens on; empty is
// the HTTP port, or the HTTPS one with TLS.
func listenEndpointCheck(lookupPort func(string) (int, error), value string) doctorCheck {
	if value == "" {
		return passCheck("empty, so phenix ui listens on port 80, or on port 443 with TLS")
	}

	return listenCheck(lookupPort, value)
}
