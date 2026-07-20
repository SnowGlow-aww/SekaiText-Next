package service

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var (
	ErrTeamFingerprintRequired = errors.New("server certificate fingerprint confirmation required")
	ErrTeamFingerprintChanged  = errors.New("server certificate fingerprint changed")
)

// TeamCertificateProbe is safe to show before authentication. Probe performs a
// TLS handshake only; it does not send an HTTP request or any credentials.
type TeamCertificateProbe struct {
	ServerURL   string `json:"serverUrl"`
	Fingerprint string `json:"fingerprint"`
	Trusted     bool   `json:"trusted"`
	Changed     bool   `json:"changed"`
}

func normalizeTeamServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("team server URL must be an absolute HTTPS URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("team server URL must not contain credentials, query, or fragment")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("team server URL must be an origin without a path")
	}
	u.Scheme = "https"
	u.Host = strings.ToLower(u.Host)
	u.Path, u.RawPath = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

func normalizeFingerprint(raw string) (string, error) {
	fingerprint := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(raw), ":", ""))
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != sha256.Size {
		return "", ErrTeamFingerprintRequired
	}
	return fingerprint, nil
}

// ProbeCertificate retrieves the leaf certificate without sending an HTTP
// request. Verification failures expose the received chain for the no-data TOFU
// prompt; all application requests use newPinnedTeamClient below.
func (t *TeamService) ProbeCertificate(rawServerURL string) (*TeamCertificateProbe, []byte, error) {
	serverURL, err := normalizeTeamServerURL(rawServerURL)
	if err != nil {
		return nil, nil, err
	}
	u, _ := url.Parse(serverURL)
	port := u.Port()
	if port == "" {
		port = "443"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rawConn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return nil, nil, fmt.Errorf("certificate probe failed: %w", err)
	}
	defer rawConn.Close()

	// Use normal verification first. For a self-signed server, Go returns the
	// received chain in CertificateVerificationError; extracting that chain is
	// enough for a no-data TOFU prompt and does not weaken TLS configuration.
	tlsConn := tls.Client(rawConn, &tls.Config{
		ServerName: u.Hostname(),
		MinVersion: tls.VersionTLS12,
	})
	var leaf *x509.Certificate
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		var verifyErr *tls.CertificateVerificationError
		if !errors.As(err, &verifyErr) || len(verifyErr.UnverifiedCertificates) == 0 {
			return nil, nil, fmt.Errorf("certificate probe failed: %w", err)
		}
		leaf = verifyErr.UnverifiedCertificates[0]
	} else {
		state := tlsConn.ConnectionState()
		if len(state.PeerCertificates) > 0 {
			leaf = state.PeerCertificates[0]
		}
	}
	if leaf == nil {
		return nil, nil, errors.New("certificate probe returned no peer certificate")
	}
	certDER := append([]byte(nil), leaf.Raw...)
	fingerprint := certificateFingerprint(certDER)

	t.mu.RLock()
	trustedURL, trustedFingerprint := t.serverURL, t.fingerprint
	t.mu.RUnlock()
	probe := &TeamCertificateProbe{
		ServerURL:   serverURL,
		Fingerprint: fingerprint,
		Trusted:     trustedURL == serverURL && trustedFingerprint == fingerprint,
		Changed:     trustedURL == serverURL && trustedFingerprint != "" && trustedFingerprint != fingerprint,
	}
	return probe, certDER, nil
}

func (t *TeamService) preparePinnedServer(rawServerURL, confirmedFingerprint string) (string, string, []byte, *http.Client, error) {
	confirmed, err := normalizeFingerprint(confirmedFingerprint)
	if err != nil {
		return "", "", nil, nil, err
	}
	probe, certDER, err := t.ProbeCertificate(rawServerURL)
	if err != nil {
		return "", "", nil, nil, err
	}
	if probe.Fingerprint != confirmed {
		return "", "", nil, nil, fmt.Errorf("%w: expected %s, got %s", ErrTeamFingerprintChanged, confirmed, probe.Fingerprint)
	}
	client, err := newPinnedTeamClient(probe.ServerURL, certDER, confirmed)
	if err != nil {
		return "", "", nil, nil, err
	}
	return probe.ServerURL, confirmed, certDER, client, nil
}

func newPinnedTeamClient(serverURL string, certDER []byte, fingerprint string) (*http.Client, error) {
	cert, err := certificateFromDER(certDER)
	if err != nil {
		return nil, fmt.Errorf("parse pinned certificate: %w", err)
	}
	if certificateFingerprint(certDER) != fingerprint {
		return nil, ErrTeamFingerprintChanged
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	roots.AddCert(cert)

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || certificateFingerprint(cs.PeerCertificates[0].Raw) != fingerprint {
				return ErrTeamFingerprintChanged
			}
			return nil
		},
	}
	origin, _ := url.Parse(serverURL)
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if !sameOrigin(origin, req.URL) {
				return errors.New("team server redirect crossed origin")
			}
			return nil
		},
	}, nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (t *TeamService) currentClient() (*http.Client, error) {
	t.mu.RLock()
	client, fingerprint := t.client, t.fingerprint
	t.mu.RUnlock()
	if client == nil || fingerprint == "" {
		return nil, ErrTeamFingerprintRequired
	}
	return client, nil
}

func publicSnapshotURLAllowed(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return publicSnapshotIP(ip)
	}
	return true
}

func publicSnapshotIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() {
		return false
	}
	for _, blocked := range nonPublicSnapshotPrefixes {
		if blocked.Contains(addr) {
			return false
		}
	}
	return true
}

var nonPublicSnapshotPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func newSnapshotHTTPClient() *http.Client {
	baseDialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := newPublicSnapshotTransport(net.DefaultResolver.LookupIPAddr, baseDialer.DialContext)
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if !publicSnapshotURLAllowed(req.URL.String()) {
				return errors.New("snapshot redirect target is not public HTTPS")
			}
			return nil
		},
	}
}

type lookupIPAddrFunc func(context.Context, string) ([]net.IPAddr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

// newPublicSnapshotTransport validates and dials the same DNS result set. This
// avoids a second resolution between policy enforcement and connection setup.
func newPublicSnapshotTransport(lookup lookupIPAddrFunc, dial dialContextFunc) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A configured HTTPS proxy would receive the connection instead of the
	// validated snapshot address, bypassing the DNS/private-address dial checks.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := lookup(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, candidate := range ips {
			if !publicSnapshotIP(candidate.IP) {
				return nil, fmt.Errorf("snapshot host resolved to a non-public address: %s", candidate.IP)
			}
		}
		var lastErr error
		for _, candidate := range ips {
			conn, err := dial(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("snapshot host resolved to no addresses")
		}
		return nil, lastErr
	}
	return transport
}
