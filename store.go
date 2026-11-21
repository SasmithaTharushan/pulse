package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Series is the stored history and alert state for one watcher.
type Series struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Label       string   `json:"label"`
	Target      string   `json:"target"`
	Results     []Result `json:"results"`
	Down        bool     `json:"down,omitempty"`        // site currently alerted as down
	CertAlerted bool     `json:"certAlerted,omitempty"` // cert-expiry alert already sent
	AlertedDay  string   `json:"alertedDay,omitempty"`  // stock: trading day already alerted
}

func (s *Series) consecutiveFailures() int {
	n := 0
	for i := len(s.Results) - 1; i >= 0 && !s.Results[i].OK; i-- {
		n++
	}
	return n
}

// Store is everything persisted between runs (history.json).
type Store struct {
	Updated time.Time          `json:"updated"`
	Series  map[string]*Series `json:"series"`
}

func LoadStore(path string) (*Store, error) {
	st := &Store{Series: map[string]*Series{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || len(b) == 0 {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, err
	}
	if st.Series == nil {
		st.Series = map[string]*Series{}
	}
	return st, nil
}

func (st *Store) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Add appends a result, trimming history to keep entries.
func (st *Store) Add(w Watcher, r Result, keep int) *Series {
	s, ok := st.Series[w.ID()]
	if !ok {
		s = &Series{ID: w.ID()}
		st.Series[w.ID()] = s
	}
	s.Kind, s.Label, s.Target = w.Kind(), w.Label(), w.Target()
	s.Results = append(s.Results, r)
	if len(s.Results) > keep {
		s.Results = append([]Result(nil), s.Results[len(s.Results)-keep:]...)
	}
	return s
}

// Prune removes series for watchers no longer in the config.
func (st *Store) Prune(ws []Watcher) {
	live := map[string]bool{}
	for _, w := range ws {
		live[w.ID()] = true
	}
	for id := range st.Series {
		if !live[id] {
			delete(st.Series, id)
		}
	}
}
