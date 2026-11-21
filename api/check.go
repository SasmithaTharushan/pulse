package handler

import (
	"context"
	"net/http"

	"github.com/SasmithaTharushan/pulse/lookup"
)

// Check handles GET /api/check?url=example.com
func Check(w http.ResponseWriter, r *http.Request) {
	lookup.Serve(w, r, 30, func(ctx context.Context, r *http.Request) (any, error) {
		return lookup.CheckSite(ctx, r.URL.Query().Get("url"))
	})
}
