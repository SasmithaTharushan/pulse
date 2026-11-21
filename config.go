package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config is loaded from pulse.json.
type Config struct {
	Title           string        `json:"title"`
	IntervalMinutes int           `json:"intervalMinutes"` // only used by `pulse serve`
	Keep            int           `json:"keep"`            // results kept per watcher
	Sites           []SiteConfig  `json:"sites"`
	Stocks          []StockConfig `json:"stocks"`
}

type SiteConfig struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	FailThreshold  int    `json:"failThreshold"` // consecutive failures before a DOWN alert
}

type StockConfig struct {
	Symbol       string  `json:"symbol"`
	Name         string  `json:"name"`
	AlertPercent float64 `json:"alertPercent"` // alert when |daily change| >= this
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.applyDefaults(); err != nil {
		return nil, err
	}
	return &c, nil
}

// applyDefaults fills in optional fields and validates required ones.
func (c *Config) applyDefaults() error {
	if c.Title == "" {
		c.Title = "Pulse"
	}
	if c.IntervalMinutes <= 0 {
		c.IntervalMinutes = 15
	}
	if c.Keep <= 0 {
		c.Keep = 672 // 7 days at 15-minute checks
	}
	for i := range c.Sites {
		s := &c.Sites[i]
		if s.URL == "" {
			return fmt.Errorf("sites[%d]: url is required", i)
		}
		if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			s.URL = "https://" + s.URL
		}
		if s.Name == "" {
			s.Name = s.URL
		}
		if s.TimeoutSeconds <= 0 {
			s.TimeoutSeconds = 10
		}
		if s.FailThreshold <= 0 {
			s.FailThreshold = 2
		}
	}
	for i := range c.Stocks {
		s := &c.Stocks[i]
		s.Symbol = strings.ToUpper(strings.TrimSpace(s.Symbol))
		if s.Symbol == "" {
			return fmt.Errorf("stocks[%d]: symbol is required", i)
		}
		if s.Name == "" {
			s.Name = s.Symbol
		}
		if s.AlertPercent <= 0 {
			s.AlertPercent = 3
		}
	}
	return nil
}
