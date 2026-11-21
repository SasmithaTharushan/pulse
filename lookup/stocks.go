// Package lookup powers Pulse's live search API: stock search, quotes with a
// one-month chart, and a safe on-demand website check.
package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ErrBadInput is returned for invalid user input (maps to HTTP 400).
var ErrBadInput = errors.New("invalid input")

// YahooBase can be overridden in tests.
var YahooBase = "https://query1.finance.yahoo.com"

// Yahoo rejects requests without a browser-like User-Agent.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

var apiClient = &http.Client{Timeout: 8 * time.Second}

var symbolRe = regexp.MustCompile(`^[A-Za-z0-9.\-=^]{1,20}$`)

// Match is one stock search result.
type Match struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Exchange string `json:"exchange"`
	Type     string `json:"type"`
}

// Search finds tickers by company name or symbol, e.g. "tesla" -> TSLA.
func Search(ctx context.Context, q string) ([]Match, error) {
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 50 {
		return nil, ErrBadInput
	}
	u := YahooBase + "/v1/finance/search?quotesCount=8&newsCount=0&listsCount=0&q=" + url.QueryEscape(q)
	var body struct {
		Quotes []struct {
			Symbol    string `json:"symbol"`
			ShortName string `json:"shortname"`
			LongName  string `json:"longname"`
			ExchDisp  string `json:"exchDisp"`
			QuoteType string `json:"quoteType"`
		} `json:"quotes"`
	}
	if err := getJSON(ctx, u, &body); err != nil {
		return nil, err
	}
	out := []Match{}
	for _, q := range body.Quotes {
		if q.Symbol == "" {
			continue
		}
		name := q.LongName
		if name == "" {
			name = q.ShortName
		}
		out = append(out, Match{q.Symbol, name, q.ExchDisp, strings.ToLower(q.QuoteType)})
	}
	return out, nil
}

// Point is one daily close.
type Point struct {
	T int64   `json:"t"` // unix seconds
	C float64 `json:"c"`
}

// Quote is a current price plus one month of daily closes.
type Quote struct {
	Symbol     string    `json:"symbol"`
	Name       string    `json:"name"`
	Exchange   string    `json:"exchange"`
	Currency   string    `json:"currency"`
	Price      float64   `json:"price"`
	ChangePct  float64   `json:"changePct"`
	MarketTime time.Time `json:"marketTime"`
	History    []Point   `json:"history"`
}

// GetQuote fetches a quote and one-month history for symbol.
func GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if !symbolRe.MatchString(symbol) {
		return nil, ErrBadInput
	}
	u := YahooBase + "/v8/finance/chart/" + url.PathEscape(symbol) + "?range=1mo&interval=1d"
	var body struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol             string   `json:"symbol"`
					LongName           string   `json:"longName"`
					ShortName          string   `json:"shortName"`
					FullExchangeName   string   `json:"fullExchangeName"`
					Currency           string   `json:"currency"`
					RegularMarketPrice float64  `json:"regularMarketPrice"`
					RegularMarketTime  int64    `json:"regularMarketTime"`
					ChangePct          *float64 `json:"regularMarketChangePercent"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []*float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	if err := getJSON(ctx, u, &body); err != nil {
		return nil, err
	}
	if body.Chart.Error != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, body.Chart.Error.Description)
	}
	if len(body.Chart.Result) == 0 || body.Chart.Result[0].Meta.RegularMarketPrice == 0 {
		return nil, ErrNotFound
	}
	res := body.Chart.Result[0]
	m := res.Meta
	q := &Quote{
		Symbol:     m.Symbol,
		Name:       m.LongName,
		Exchange:   m.FullExchangeName,
		Currency:   m.Currency,
		Price:      m.RegularMarketPrice,
		MarketTime: time.Unix(m.RegularMarketTime, 0).UTC(),
		History:    []Point{},
	}
	if q.Name == "" {
		q.Name = m.ShortName
	}
	if len(res.Indicators.Quote) > 0 {
		closes := res.Indicators.Quote[0].Close
		for i, ts := range res.Timestamp {
			if i < len(closes) && closes[i] != nil {
				q.History = append(q.History, Point{ts, round(*closes[i], 4)})
			}
		}
	}
	switch {
	case m.ChangePct != nil:
		q.ChangePct = round(*m.ChangePct, 2)
	case len(q.History) >= 2:
		prev := q.History[len(q.History)-2].C
		q.ChangePct = round((q.Price-prev)/prev*100, 2)
	}
	return q, nil
}

// ErrNotFound means the symbol does not exist (maps to HTTP 404).
var ErrNotFound = errors.New("not found")

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")
	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("upstream: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
