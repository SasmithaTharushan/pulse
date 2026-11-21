package handler

import (
	"context"
	"net/http"

	"github.com/SasmithaTharushan/pulse/lookup"
)

// Quote handles GET /api/quote?symbol=AAPL
func Quote(w http.ResponseWriter, r *http.Request) {
	lookup.Serve(w, r, 60, func(ctx context.Context, r *http.Request) (any, error) {
		return lookup.GetQuote(ctx, r.URL.Query().Get("symbol"))
	})
}
