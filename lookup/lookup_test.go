package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fd00::1", "fe80::1", "::ffff:127.0.0.1"}
	for _, s := range blocked {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "140.82.112.3", "2606:4700::1111"} {
		if !publicIP(net.ParseIP(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"example.com":          "https://example.com",
		"http://example.com/x": "http://example.com/x",
		" go.dev ":             "https://go.dev",
	}
	for in, want := range cases {
		if got, err := NormalizeURL(in); err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "ftp://example.com", "localhost:8080", "http://metadata.google.internal", "javascript:alert(1)"} {
		if _, err := NormalizeURL(in); err == nil {
			t.Errorf("NormalizeURL(%q) should fail", in)
		}
	}
}

// The SSRF guard must refuse to connect to a server on localhost.
func TestCheckSiteBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("guard let a request through to localhost")
	}))
	defer srv.Close()
	// 127.0.0.1 is an IP literal, so it skips the hostname check and must be
	// stopped by the dialer guard.
	if _, err := CheckSite(context.Background(), srv.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
}

func TestCheckSiteUpDown(t *testing.T) {
	allowPrivate = true
	defer func() { allowPrivate = false }()
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "test")
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer bad.Close()

	r, err := CheckSite(context.Background(), ok.URL)
	if err != nil || !r.OK || r.Status != 200 || r.Server != "test" {
		t.Fatalf("expected up, got %+v %v", r, err)
	}
	r, err = CheckSite(context.Background(), bad.URL)
	if err != nil || r.OK || r.Status != 500 {
		t.Fatalf("expected down, got %+v %v", r, err)
	}
}

func fakeYahoo(t *testing.T) func() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/finance/search"):
			w.Write([]byte(`{"quotes":[{"symbol":"TSLA","shortname":"Tesla","longname":"Tesla, Inc.","exchDisp":"NASDAQ","quoteType":"EQUITY"},{"shortname":"no symbol"}]}`))
		case strings.Contains(r.URL.Path, "NOPE"):
			w.WriteHeader(404)
			w.Write([]byte(`{"chart":{"result":null,"error":{"description":"No data found"}}}`))
		default:
			w.Write([]byte(`{"chart":{"result":[{"meta":{"symbol":"TSLA","longName":"Tesla, Inc.","currency":"USD","fullExchangeName":"NasdaqGS","regularMarketPrice":110,"regularMarketTime":1795208402},
			"timestamp":[1,2,3,4],"indicators":{"quote":[{"close":[90,null,100,110]}]}}],"error":null}}`))
		}
	}))
	old := YahooBase
	YahooBase = srv.URL
	return func() { YahooBase = old; srv.Close() }
}

func TestSearchAndQuote(t *testing.T) {
	defer fakeYahoo(t)()
	ctx := context.Background()

	ms, err := Search(ctx, "tesla")
	if err != nil || len(ms) != 1 || ms[0].Symbol != "TSLA" || ms[0].Name != "Tesla, Inc." || ms[0].Type != "equity" {
		t.Fatalf("search: %+v %v", ms, err)
	}
	if _, err := Search(ctx, "  "); !errors.Is(err, ErrBadInput) {
		t.Fatalf("empty search should be bad input, got %v", err)
	}

	q, err := GetQuote(ctx, "tsla")
	if err != nil {
		t.Fatal(err)
	}
	// No changePct in meta, so it falls back to the last two closes (100 -> 110);
	// the null close is skipped.
	if q.Price != 110 || q.ChangePct != 10 || len(q.History) != 3 || q.Name != "Tesla, Inc." {
		t.Fatalf("quote: %+v", q)
	}
	if _, err := GetQuote(ctx, "NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := GetQuote(ctx, "bad symbol!"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("expected bad input, got %v", err)
	}
}

func TestServeCORSAndErrors(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://good.example")
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Serve(w, r, 60, func(ctx context.Context, r *http.Request) (any, error) {
			if r.URL.Query().Get("fail") != "" {
				return nil, ErrBlocked
			}
			return map[string]int{"n": 1}, nil
		})
	})

	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "https://good.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "https://good.example" ||
		!strings.Contains(rec.Header().Get("Cache-Control"), "s-maxage=60") {
		t.Fatalf("good origin: %d %v", rec.Code, rec.Header())
	}
	var body map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["n"] != 1 {
		t.Fatalf("body: %s", rec.Body)
	}

	req = httptest.NewRequest("GET", "/x?fail=1", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 || rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bad origin/error: %d %v", rec.Code, rec.Header())
	}
}
