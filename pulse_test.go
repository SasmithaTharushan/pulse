package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newSite(url string) *HTTPWatcher {
	cfg := &Config{Sites: []SiteConfig{{Name: "test", URL: url}}}
	if err := cfg.applyDefaults(); err != nil {
		panic(err)
	}
	return &HTTPWatcher{cfg: cfg.Sites[0], client: http.DefaultClient}
}

func TestSiteCheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()

	if r := newSite(ok.URL).Check(context.Background()); !r.OK || r.Status != 200 {
		t.Fatalf("expected OK, got %+v", r)
	}
	if r := newSite(bad.URL).Check(context.Background()); r.OK || r.Status != 503 {
		t.Fatalf("expected failure, got %+v", r)
	}
	if r := newSite("http://127.0.0.1:1").Check(context.Background()); r.OK || r.Err == "" {
		t.Fatalf("expected connection error, got %+v", r)
	}
}

func TestSiteTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	w := newSite(slow.URL)
	w.cfg.TimeoutSeconds = 1
	if r := w.Check(context.Background()); r.OK || r.Err != "timeout" {
		t.Fatalf("expected timeout, got %+v", r)
	}
}

func TestSiteAlerts(t *testing.T) {
	w := newSite("https://example.com") // FailThreshold defaults to 2
	st := &Store{Series: map[string]*Series{}}
	step := func(ok bool) []string {
		s := st.Add(w, Result{OK: ok, Err: map[bool]string{false: "boom"}[ok]}, 100)
		return w.Alerts(s)
	}
	if a := step(true); len(a) != 0 {
		t.Fatalf("unexpected alert %v", a)
	}
	if a := step(false); len(a) != 0 {
		t.Fatalf("alerted after 1 failure: %v", a)
	}
	if a := step(false); len(a) != 1 || !strings.Contains(a[0], "DOWN") {
		t.Fatalf("expected DOWN alert, got %v", a)
	}
	if a := step(false); len(a) != 0 {
		t.Fatalf("repeated DOWN alert: %v", a)
	}
	if a := step(true); len(a) != 1 || !strings.Contains(a[0], "UP") {
		t.Fatalf("expected UP alert, got %v", a)
	}
}

func TestYahooQuote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "BAD") {
			w.WriteHeader(404)
			w.Write([]byte(`{"chart":{"result":null,"error":{"description":"No data found"}}}`))
			return
		}
		w.Write([]byte(`{"chart":{"result":[{"meta":{"regularMarketPrice":110,"chartPreviousClose":100,"regularMarketTime":1795208402}}],"error":null}}`))
	}))
	defer srv.Close()
	old := yahooBase
	yahooBase = srv.URL
	defer func() { yahooBase = old }()

	w := &StockWatcher{cfg: StockConfig{Symbol: "TEST", Name: "Test", AlertPercent: 3}, client: http.DefaultClient}
	r := w.Check(context.Background())
	if !r.OK || r.Value != 110 || r.Change != 10 {
		t.Fatalf("bad quote: %+v", r)
	}
	st := &Store{Series: map[string]*Series{}}
	if a := w.Alerts(st.Add(w, r, 10)); len(a) != 1 {
		t.Fatalf("expected move alert, got %v", a)
	}
	if a := w.Alerts(st.Add(w, r, 10)); len(a) != 0 {
		t.Fatalf("alerted twice in one trading day: %v", a)
	}

	bad := &StockWatcher{cfg: StockConfig{Symbol: "BAD"}, client: http.DefaultClient}
	if r := bad.Check(context.Background()); r.OK || !strings.Contains(r.Err, "No data found") {
		t.Fatalf("expected error, got %+v", r)
	}
}

func TestStoreRoundTripAndTrim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "history.json")
	st, err := LoadStore(path) // missing file is fine
	if err != nil {
		t.Fatal(err)
	}
	w := newSite("https://example.com")
	for i := 0; i < 10; i++ {
		st.Add(w, Result{OK: true, Value: float64(i)}, 5)
	}
	if err := st.Save(path); err != nil {
		t.Fatal(err)
	}
	st2, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rs := st2.Series[w.ID()].Results
	if len(rs) != 5 || rs[0].Value != 5 {
		t.Fatalf("trim failed: %+v", rs)
	}
}

func TestRenderDashboard(t *testing.T) {
	cfg := &Config{Sites: []SiteConfig{{URL: "example.com"}}, Stocks: []StockConfig{{Symbol: "aapl"}}}
	if err := cfg.applyDefaults(); err != nil {
		t.Fatal(err)
	}
	ws := BuildWatchers(cfg, http.DefaultClient)
	st := &Store{Series: map[string]*Series{}, Updated: time.Now()}
	st.Add(ws[0], Result{OK: true, Value: 120}, 10)
	st.Add(ws[0], Result{OK: false, Err: "timeout"}, 10)
	st.Add(ws[1], Result{OK: true, Value: 200, Change: 1.5}, 10)
	st.Add(ws[1], Result{OK: true, Value: 205, Change: 4}, 10)

	out := t.TempDir()
	if err := RenderDashboard(cfg, st, ws, out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	for _, want := range []string{"1 site(s) down", "https://example.com", "205.00", "+4.00% today", "<svg"} {
		if !strings.Contains(html, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "history.json")); err != nil {
		t.Error("history.json not published")
	}
}
