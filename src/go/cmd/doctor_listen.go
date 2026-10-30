package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"
)

// certExpiryWarning is how close to its expiry a TLS certificate warns.
const certExpiryWarning = 30 * 24 * time.Hour

// tlsCheck checks the TLS key and certificate phenix ui serves HTTPS with,
// at now, never showing the key. With only one set, it serves plain HTTP.
func (e doctorEnv) tlsCheck(keyPath, certPath string, now time.Time) doctorCheck {
	switch {
	case keyPath == "" && certPath == "":
		return passCheck("not set, so phenix ui serves plain HTTP")
	case certPath == "":
		return failCheck("ui.tls-key is set without ui.tls-cert, so phenix ui serves plain HTTP")
	case keyPath == "":
		return failCheck("ui.tls-cert is set without ui.tls-key, so phenix ui serves plain HTTP")
	}

	for _, path := range []string{certPath, keyPath} {
		if check := e.readableFileCheck(path, doctorFail); check.Status != doctorPass {
			return check
		}
	}

	certPEM, err := e.readFile(certPath)
	if err != nil {
		return failCheck(fmt.Sprintf("reading %s: %v", certPath, err))
	}

	keyPEM, err := e.readFile(keyPath)
	if err != nil {
		return failCheck(fmt.Sprintf("reading %s: %v", keyPath, err))
	}

	// crypto/tls errors name PEM block types, never their contents
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return failCheck(fmt.Sprintf("phenix ui cannot load the key pair: %v", err))
	}

	leaf := pair.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(pair.Certificate[0]); err != nil {
			return failCheck(fmt.Sprintf("parsing %s: %v", certPath, err))
		}
	}

	day := func(t time.Time) string { return t.UTC().Format(time.DateOnly) }

	switch {
	case now.After(leaf.NotAfter):
		return failCheck(fmt.Sprintf("the certificate in %s expired on %s", certPath, day(leaf.NotAfter)))
	case now.Before(leaf.NotBefore):
		return failCheck(fmt.Sprintf("the certificate in %s is not valid until %s", certPath, day(leaf.NotBefore)))
	case leaf.NotAfter.Sub(now) < certExpiryWarning:
		return warnCheck(fmt.Sprintf("the certificate in %s expires on %s", certPath, day(leaf.NotAfter)))
	}

	return passCheck(fmt.Sprintf("%s, valid until %s", certPath, day(leaf.NotAfter)))
}

// listenEndpointCheck checks the endpoint phenix ui listens on; empty is
// the HTTP port, or the HTTPS one with TLS.
func listenEndpointCheck(lookupPort func(string) (int, error), value string) doctorCheck {
	if value == "" {
		return passCheck("empty, so phenix ui listens on port 80, or on port 443 with TLS")
	}

	return listenCheck(lookupPort, value)
}
