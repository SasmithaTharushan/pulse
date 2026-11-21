package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"
)

// StockWatcher fetches a quote. It uses Finnhub when FINNHUB_TOKEN is set,
// otherwise Yahoo Finance's public chart endpoint (no key needed).
type StockWatcher struct {
	cfg       StockConfig
	client    *http.Client
	marketDay string // trading day of the last quote, set by Check
}

var (
	yahooBase   = "https://query1.finance.yahoo.com"
	finnhubBase = "https://finnhub.io"
)

// Yahoo rejects requests without a browser-like User-Agent.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

func (w *StockWatcher) ID() string     { return "stock:" + w.cfg.Symbol }
func (w *StockWatcher) Kind() string   { return "stock" }
func (w *StockWatcher) Label() string  { return w.cfg.Name }
func (w *StockWatcher) Target() string { return w.cfg.Symbol }

func (w *StockWatcher) Check(ctx context.Context) Result {
	r := Result{Time: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var q quote
	var err error
	if tok := os.Getenv("FINNHUB_TOKEN"); tok != "" {
		q, err = finnhubQuote(ctx, w.client, w.cfg.Symbol, tok)
	} else {
		q, err = yahooQuote(ctx, w.client, w.cfg.Symbol)
	}
	if err != nil {
		r.Err = err.Error()
		return r
	}
	r.OK = true
	r.Value = q.Price
	if q.PrevClose > 0 {
		r.Change = math.Round((q.Price-q.PrevClose)/q.PrevClose*10000) / 100
	}
	w.marketDay = q.Time.UTC().Format("2006-01-02")
	return r
}

func (w *StockWatcher) Alerts(s *Series) []string {
	last := s.Results[len(s.Results)-1]
	if !last.OK || w.marketDay == "" || s.AlertedDay == w.marketDay {
		return nil
	}
	if math.Abs(last.Change) < w.cfg.AlertPercent {
		return nil
	}
	s.AlertedDay = w.marketDay // at most one alert per trading day
	arrow := "📈"
	if last.Change < 0 {
		arrow = "📉"
	}
	return []string{fmt.Sprintf("%s %s (%s) moved %+.2f%% today, now %.2f",
		arrow, w.cfg.Name, w.cfg.Symbol, last.Change, last.Value)}
}

type quote struct {
	Price, PrevClose float64
	Time             time.Time
}

func yahooQuote(ctx context.Context, c *http.Client, symbol string) (quote, error) {
	u := yahooBase + "/v8/finance/chart/" + url.PathEscape(symbol) + "?range=1d&interval=1d"
	var body struct {
		Chart struct {
			Result []struct {
				Meta struct {
					RegularMarketPrice float64 `json:"regularMarketPrice"`
					ChartPreviousClose float64 `json:"chartPreviousClose"`
					RegularMarketTime  int64   `json:"regularMarketTime"`
				} `json:"meta"`
			} `json:"result"`
			Error *struct {
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	if err := getJSON(ctx, c, u, browserUA, &body); err != nil {
		return quote{}, fmt.Errorf("yahoo: %w", err)
	}
	if body.Chart.Error != nil {
		return quote{}, fmt.Errorf("yahoo: %s", body.Chart.Error.Description)
	}
	if len(body.Chart.Result) == 0 || body.Chart.Result[0].Meta.RegularMarketPrice == 0 {
		return quote{}, errors.New("yahoo: no price in response")
	}
	m := body.Chart.Result[0].Meta
	return quote{m.RegularMarketPrice, m.ChartPreviousClose, time.Unix(m.RegularMarketTime, 0)}, nil
}

func finnhubQuote(ctx context.Context, c *http.Client, symbol, token string) (quote, error) {
	u := finnhubBase + "/api/v1/quote?symbol=" + url.QueryEscape(symbol) + "&token=" + url.QueryEscape(token)
	var body struct {
		C  float64 `json:"c"`
		PC float64 `json:"pc"`
		T  int64   `json:"t"`
	}
	if err := getJSON(ctx, c, u, userAgent, &body); err != nil {
		return quote{}, fmt.Errorf("finnhub: %w", err)
	}
	if body.C == 0 {
		return quote{}, errors.New("finnhub: unknown symbol or no data")
	}
	return quote{body.C, body.PC, time.Unix(body.T, 0)}, nil
}
