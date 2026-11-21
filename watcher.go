package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Result is one check of one watcher. Short JSON keys keep history.json small.
type Result struct {
	Time     time.Time `json:"t"`
	OK       bool      `json:"ok"`
	Value    float64   `json:"v"`            // latency in ms (sites) or price (stocks)
	Change   float64   `json:"c,omitempty"`  // daily % change (stocks)
	Status   int       `json:"s,omitempty"`  // HTTP status (sites)
	CertDays int       `json:"cd,omitempty"` // days until TLS cert expiry (sites)
	Err      string    `json:"e,omitempty"`
}

// Watcher is anything Pulse can check on a schedule.
// Add a new kind of monitor by implementing this interface.
type Watcher interface {
	ID() string
	Kind() string // "site" or "stock"
	Label() string
	Target() string
	Check(ctx context.Context) Result
	// Alerts inspects the series (with the newest result already appended),
	// updates its alert state and returns any messages to send.
	Alerts(s *Series) []string
}

const userAgent = "Pulse-Monitor/1.0 (+https://github.com/SasmithaTharushan/pulse)"

func BuildWatchers(cfg *Config, client *http.Client) []Watcher {
	var ws []Watcher
	for _, s := range cfg.Sites {
		ws = append(ws, &HTTPWatcher{cfg: s, client: client})
	}
	for _, s := range cfg.Stocks {
		ws = append(ws, &StockWatcher{cfg: s, client: client})
	}
	return ws
}

// RunAll checks every watcher concurrently.
func RunAll(ctx context.Context, ws []Watcher) []Result {
	results := make([]Result, len(ws))
	var wg sync.WaitGroup
	for i, w := range ws {
		wg.Add(1)
		go func(i int, w Watcher) {
			defer wg.Done()
			results[i] = w.Check(ctx)
		}(i, w)
	}
	wg.Wait()
	return results
}

func getJSON(ctx context.Context, client *http.Client, url, ua string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
