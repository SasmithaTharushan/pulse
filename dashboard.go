package main

import (
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

var dashboardTmpl = template.Must(template.New("dash").Parse(dashboardHTML))

const tickCount = 60

type cardView struct {
	Label, Target string
	OK            bool
	Headline      string
	Sub           string
	Change        float64
	Err           string
	Spark         template.HTML
	Ticks         []string // "ok" | "fail" | "none", oldest first
}

type pageView struct {
	Title      string
	Updated    string
	UpdatedISO string
	DownCount  int
	Sites      []cardView
	Stocks     []cardView
}

// RenderDashboard writes index.html and history.json into outDir.
func RenderDashboard(cfg *Config, st *Store, ws []Watcher, outDir string) error {
	pv := pageView{
		Title:      cfg.Title,
		Updated:    st.Updated.UTC().Format("2006-01-02 15:04 UTC"),
		UpdatedISO: st.Updated.UTC().Format(time.RFC3339),
	}
	for _, w := range ws {
		s := st.Series[w.ID()]
		if s == nil || len(s.Results) == 0 {
			continue
		}
		switch w.Kind() {
		case "site":
			c := siteCard(s)
			if !c.OK {
				pv.DownCount++
			}
			pv.Sites = append(pv.Sites, c)
		case "stock":
			pv.Stocks = append(pv.Stocks, stockCard(s))
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(outDir, "index.html"))
	if err != nil {
		return err
	}
	if err := dashboardTmpl.Execute(f, pv); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Publish the raw data too: a free JSON API at /history.json.
	return st.Save(filepath.Join(outDir, "history.json"))
}

func siteCard(s *Series) cardView {
	last := s.Results[len(s.Results)-1]
	c := cardView{Label: s.Label, Target: s.Target, OK: last.OK, Err: last.Err}

	var ok, n int
	var sum float64
	var lat []float64
	for _, r := range s.Results {
		if r.OK {
			ok++
			sum += r.Value
			lat = append(lat, r.Value)
		}
		n++
	}
	if last.OK {
		c.Headline = fmt.Sprintf("%.0f ms", last.Value)
	} else {
		c.Headline = "Down"
	}
	parts := []string{fmt.Sprintf("uptime %.2f%%", float64(ok)/float64(n)*100)}
	if ok > 0 {
		parts = append(parts, fmt.Sprintf("avg %.0f ms", sum/float64(ok)))
	}
	if last.CertDays != 0 {
		parts = append(parts, fmt.Sprintf("cert %dd", last.CertDays))
	}
	c.Sub = strings.Join(parts, " · ")
	c.Spark = sparkline(tail(lat, 96))

	c.Ticks = make([]string, tickCount)
	recent := s.Results
	if len(recent) > tickCount {
		recent = recent[len(recent)-tickCount:]
	}
	pad := tickCount - len(recent)
	for i := range c.Ticks {
		switch {
		case i < pad:
			c.Ticks[i] = "none"
		case recent[i-pad].OK:
			c.Ticks[i] = "ok"
		default:
			c.Ticks[i] = "fail"
		}
	}
	return c
}

func stockCard(s *Series) cardView {
	last := s.Results[len(s.Results)-1]
	c := cardView{Label: s.Label, Target: s.Target, OK: last.OK, Err: last.Err}
	// Fall back to the most recent good price if this check failed.
	var good *Result
	var prices []float64
	for i := range s.Results {
		if s.Results[i].OK {
			good = &s.Results[i]
			prices = append(prices, s.Results[i].Value)
		}
	}
	if good == nil {
		c.Headline = "—"
		return c
	}
	c.Headline = fmt.Sprintf("%.2f", good.Value)
	c.Change = good.Change
	c.Sub = fmt.Sprintf("%+.2f%% today", good.Change)
	c.Spark = sparkline(tail(prices, 96))
	return c
}

func tail(v []float64, n int) []float64 {
	if len(v) > n {
		return v[len(v)-n:]
	}
	return v
}

// sparkline renders values as a small inline SVG line.
func sparkline(vals []float64) template.HTML {
	if len(vals) < 2 {
		return ""
	}
	const w, h = 200.0, 40.0
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		lo, hi = min(lo, v), max(hi, v)
	}
	span := hi - lo
	if span == 0 {
		span = 1
	}
	var pts strings.Builder
	for i, v := range vals {
		x := float64(i) / float64(len(vals)-1) * w
		y := h - 2 - (v-lo)/span*(h-4)
		fmt.Fprintf(&pts, "%.1f,%.1f ", x, y)
	}
	return template.HTML(fmt.Sprintf(
		`<svg class="spark" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" aria-hidden="true">`+
			`<polyline points="%s" fill="none" stroke="currentColor" stroke-width="1.5" vector-effect="non-scaling-stroke" stroke-linejoin="round"/></svg>`,
		w, h, strings.TrimSpace(pts.String())))
}
