package handler

import (
	"context"
	"net/http"

	"github.com/SasmithaTharushan/pulse/lookup"
)

// Search handles GET /api/search?q=tesla
func Search(w http.ResponseWriter, r *http.Request) {
	lookup.Serve(w, r, 3600, func(ctx context.Context, r *http.Request) (any, error) {
		return lookup.Search(ctx, r.URL.Query().Get("q"))
	})
}
