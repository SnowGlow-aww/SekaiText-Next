package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

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

// newTeamHTTPClient accepts the team's self-signed certificate. The server URL
// remains HTTPS-only, and redirects cannot leave the selected origin.
func newTeamHTTPClient(rawServerURL string) (string, *http.Client, error) {
	serverURL, err := normalizeTeamServerURL(rawServerURL)
	if err != nil {
		return "", nil, err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // The configured team server uses a self-signed certificate.
	}
	origin, _ := url.Parse(serverURL)
	return serverURL, &http.Client{
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

func publicSnapshotResolvedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	// Clash and compatible TUN proxies use 198.18.0.0/15 as a DNS Fake-IP
	// range. Raw URLs in this range are still rejected by
	// publicSnapshotURLAllowed; only a hostname resolved by the local DNS proxy
	// may reach the dialer with one of these synthetic addresses.
	if clashFakeIPPrefix.Contains(addr.Unmap()) {
		return true
	}
	return publicSnapshotIP(ip)
}

var clashFakeIPPrefix = netip.MustParsePrefix("198.18.0.0/15")

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
			if !publicSnapshotResolvedIP(candidate.IP) {
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
