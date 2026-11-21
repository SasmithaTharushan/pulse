package lookup

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// SiteResult is the outcome of an on-demand website check.
type SiteResult struct {
	URL        string `json:"url"`
	FinalURL   string `json:"finalUrl,omitempty"`
	OK         bool   `json:"ok"`
	Status     int    `json:"status,omitempty"`
	Ms         int64  `json:"ms,omitempty"`
	CertDays   *int   `json:"certDays,omitempty"`
	CertIssuer string `json:"certIssuer,omitempty"`
	TLSVersion string `json:"tlsVersion,omitempty"`
	Server     string `json:"server,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ErrBlocked is returned when a URL points at a private/internal address.
var ErrBlocked = errors.New("address not allowed")

// allowPrivate disables the SSRF guard; tests only.
var allowPrivate = false

var allowedPorts = map[string]bool{"80": true, "443": true, "8080": true, "8443": true}

var cgnat = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// publicIP reports whether ip is safe to connect to from a public API.
func publicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || cgnat.Contains(ip))
}

// guardedClient refuses to connect to private, loopback or link-local IPs.
// The check runs at connect time on the resolved IP, so DNS tricks and
// redirects to internal addresses are blocked too.
func guardedClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if allowPrivate {
				return nil
			}
			if !allowedPorts[port] || !publicIP(net.ParseIP(host)) {
				return ErrBlocked
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               nil,
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 5 * time.Second,
			MaxIdleConns:        10,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

var siteClient = guardedClient(10 * time.Second)

// NormalizeURL turns "example.com" into "https://example.com".
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", ErrBadInput
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", ErrBadInput
	}
	if !allowPrivate {
		h := strings.ToLower(u.Hostname())
		if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".local") {
			return "", ErrBlocked
		}
	}
	u.Fragment = ""
	return u.String(), nil
}

// CheckSite checks whether a website is up right now.
func CheckSite(ctx context.Context, raw string) (*SiteResult, error) {
	target, err := NormalizeURL(raw)
	if err != nil {
		return nil, err
	}
	r := &SiteResult{URL: target}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, ErrBadInput
	}
	req.Header.Set("User-Agent", "Pulse-Monitor/1.0 (+https://github.com/SasmithaTharushan/pulse)")

	start := time.Now()
	resp, err := siteClient.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		r.Error = friendly(err)
		return r, nil
	}
	defer resp.Body.Close()
	r.Ms = time.Since(start).Milliseconds()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	r.Status = resp.StatusCode
	r.OK = resp.StatusCode >= 200 && resp.StatusCode < 400
	r.Server = resp.Header.Get("Server")
	if fu := resp.Request.URL.String(); fu != target {
		r.FinalURL = fu
	}
	if !r.OK {
		r.Error = resp.Status
	}
	if st := resp.TLS; st != nil && len(st.PeerCertificates) > 0 {
		c := st.PeerCertificates[0]
		d := int(time.Until(c.NotAfter).Hours() / 24)
		r.CertDays = &d
		if len(c.Issuer.Organization) > 0 {
			r.CertIssuer = c.Issuer.Organization[0]
		} else {
			r.CertIssuer = c.Issuer.CommonName
		}
		r.TLSVersion = tls.VersionName(st.Version)
	}
	return r, nil
}

func friendly(err error) string {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var netErr net.Error
	switch {
	case errors.As(err, &dnsErr):
		return "domain not found (DNS lookup failed)"
	case errors.As(err, &certErr):
		return "invalid TLS certificate"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timed out"
	case strings.Contains(err.Error(), "connection refused"):
		return "connection refused"
	}
	return fmt.Sprintf("unreachable: %v", errors.Unwrap(err))
}
