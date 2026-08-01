package service

import (
	"crypto/x509"
	_ "embed"
	"errors"
)

// officialTeamServerOrigin is the existing first-party team glossary endpoint.
// Its Caddy deployment intentionally uses an internal CA, so the app carries
// that CA's public root certificate and applies it only to this exact origin.
// Other team servers continue to use the operating-system trust store (or an
// explicitly supplied test/private trust pool) and never inherit this root.
const officialTeamServerOrigin = "https://8.140.254.217:8443"

//go:embed certs/official-team-root.crt
var officialTeamRootPEM []byte

func teamRootCAsForServer(serverURL string, rootCAs *x509.CertPool) (*x509.CertPool, error) {
	if serverURL != officialTeamServerOrigin {
		return rootCAs, nil
	}

	var roots *x509.CertPool
	if rootCAs != nil {
		roots = rootCAs.Clone()
	} else {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
	}
	if !roots.AppendCertsFromPEM(officialTeamRootPEM) {
		return nil, errors.New("embedded official team CA certificate is invalid")
	}
	return roots, nil
}
