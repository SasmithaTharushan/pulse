package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Endpoint is the logic behind one API route.
type Endpoint func(ctx context.Context, r *http.Request) (any, error)

// Serve wraps an Endpoint with CORS, CDN caching and JSON error handling.
// cacheSeconds lets Vercel's CDN reuse a response, so popular lookups don't
// hit Yahoo or the target site again (this is the main rate limiter).
func Serve(w http.ResponseWriter, r *http.Request, cacheSeconds int, fn Endpoint) {
	h := w.Header()
	if origin := r.Header.Get("Origin"); origin != "" && originAllowed(origin) {
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	}
	h.Set("Vary", "Origin")
	h.Set("X-Content-Type-Options", "nosniff")

	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodGet:
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
	defer cancel()
	v, err := fn(ctx, r)
	if err != nil {
		h.Set("Cache-Control", "no-store")
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, ErrBadInput):
			status = http.StatusBadRequest
		case errors.Is(err, ErrBlocked):
			status = http.StatusForbidden
		case errors.Is(err, ErrNotFound):
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	h.Set("Cache-Control", fmt.Sprintf("public, max-age=0, s-maxage=%d, stale-while-revalidate=%d", cacheSeconds, cacheSeconds*5))
	writeJSON(w, http.StatusOK, v)
}

// originAllowed checks ALLOWED_ORIGINS (comma separated). Defaults to the
// GitHub Pages dashboard and local development.
func originAllowed(origin string) bool {
	list := os.Getenv("ALLOWED_ORIGINS")
	if list == "" {
		list = "https://sasmithatharushan.github.io,http://localhost:8080"
	}
	for _, o := range strings.Split(list, ",") {
		if o = strings.TrimSpace(o); o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
