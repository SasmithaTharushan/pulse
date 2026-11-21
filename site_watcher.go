package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// HTTPWatcher checks that a website responds with a 2xx/3xx status.
type HTTPWatcher struct {
	cfg    SiteConfig
	client *http.Client
}

func (w *HTTPWatcher) ID() string     { return "site:" + w.cfg.URL }
func (w *HTTPWatcher) Kind() string   { return "site" }
func (w *HTTPWatcher) Label() string  { return w.cfg.Name }
func (w *HTTPWatcher) Target() string { return w.cfg.URL }

func (w *HTTPWatcher) Check(ctx context.Context) Result {
	r := Result{Time: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(w.cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.cfg.URL, nil)
	if err != nil {
		r.Err = err.Error()
		return r
	}
	req.Header.Set("User-Agent", userAgent)

	start := time.Now()
	resp, err := w.client.Do(req)
	if err != nil {
		r.Err = friendlyErr(err)
		return r
	}
	defer resp.Body.Close()
	r.Value = float64(time.Since(start).Milliseconds())
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	r.Status = resp.StatusCode
	r.OK = resp.StatusCode >= 200 && resp.StatusCode < 400
	if !r.OK {
		r.Err = resp.Status
	}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		days := int(time.Until(resp.TLS.PeerCertificates[0].NotAfter).Hours() / 24)
		if days < 1 {
			days = -1 // keep non-zero so it isn't dropped by omitempty
		}
		r.CertDays = days
	}
	return r
}

func (w *HTTPWatcher) Alerts(s *Series) []string {
	var out []string
	last := s.Results[len(s.Results)-1]
	if !last.OK {
		if !s.Down && s.consecutiveFailures() >= w.cfg.FailThreshold {
			s.Down = true
			out = append(out, fmt.Sprintf("🔴 %s is DOWN (%s) %s", w.cfg.Name, last.Err, w.cfg.URL))
		}
	} else if s.Down {
		s.Down = false
		out = append(out, fmt.Sprintf("🟢 %s is back UP (%.0f ms) %s", w.cfg.Name, last.Value, w.cfg.URL))
	}

	if last.OK && last.CertDays != 0 {
		if last.CertDays <= 14 && !s.CertAlerted {
			s.CertAlerted = true
			out = append(out, fmt.Sprintf("⚠️ %s TLS certificate expires in %d days", w.cfg.Name, last.CertDays))
		} else if last.CertDays > 14 {
			s.CertAlerted = false
		}
	}
	return out
}

func friendlyErr(err error) string {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &dnsErr):
		return "DNS lookup failed"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return err.Error()
}
