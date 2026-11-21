// Pulse watches websites and stock prices on a schedule, alerts you on
// Discord/Telegram, and publishes a static status dashboard.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func usage() {
	fmt.Fprint(os.Stderr, `Pulse - website & stock watcher

Usage:
  pulse run   [-config pulse.json] [-data data/history.json] [-out site]
              Check everything once, update history, render dashboard, send alerts.
  pulse serve [-config pulse.json] [-data data/history.json] [-out site] [-addr :8080]
              Run checks every intervalMinutes and serve the dashboard locally.
  pulse check <url>
              One-off health check of a website (exit code 1 if down).
`)
}

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	client := &http.Client{Timeout: 30 * time.Second}

	switch os.Args[1] {
	case "run":
		o := parseOpts("run", os.Args[2:])
		if err := runOnce(context.Background(), client, o); err != nil {
			log.Fatal(err)
		}
	case "serve":
		o := parseOpts("serve", os.Args[2:])
		serve(client, o)
	case "check":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		os.Exit(checkOne(client, os.Args[2]))
	default:
		usage()
		os.Exit(2)
	}
}

type opts struct{ config, data, out, addr string }

func parseOpts(name string, args []string) opts {
	var o opts
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&o.config, "config", "pulse.json", "config file")
	fs.StringVar(&o.data, "data", "data/history.json", "history file")
	fs.StringVar(&o.out, "out", "site", "dashboard output directory")
	fs.StringVar(&o.addr, "addr", ":8080", "listen address (serve only)")
	_ = fs.Parse(args)
	return o
}

func runOnce(ctx context.Context, client *http.Client, o opts) error {
	cfg, err := LoadConfig(o.config)
	if err != nil {
		return err
	}
	st, err := LoadStore(o.data)
	if err != nil {
		return fmt.Errorf("load %s: %w", o.data, err)
	}
	ws := BuildWatchers(cfg, client)
	results := RunAll(ctx, ws)

	var alerts []string
	for i, w := range ws {
		r := results[i]
		status := "OK  "
		if !r.OK {
			status = "FAIL"
		}
		log.Printf("%s %-6s %-28s %10.2f %s", status, w.Kind(), w.Label(), r.Value, r.Err)
		s := st.Add(w, r, cfg.Keep)
		alerts = append(alerts, w.Alerts(s)...)
	}
	st.Prune(ws)
	st.Updated = time.Now().UTC()

	if err := st.Save(o.data); err != nil {
		return fmt.Errorf("save: %w", err)
	}
	if err := RenderDashboard(cfg, st, ws, o.out); err != nil {
		return fmt.Errorf("dashboard: %w", err)
	}
	Notify(ctx, client, alerts)
	return nil
}

func serve(client *http.Client, o opts) {
	cfg, err := LoadConfig(o.config)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	tick := func() {
		if err := runOnce(ctx, client, o); err != nil {
			log.Printf("run: %v", err)
		}
	}
	tick()
	go func() {
		t := time.NewTicker(time.Duration(cfg.IntervalMinutes) * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick()
			}
		}
	}()

	srv := &http.Server{Addr: o.addr, Handler: http.FileServer(http.Dir(o.out))}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("dashboard on http://localhost%s (checks every %d min)", o.addr, cfg.IntervalMinutes)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func checkOne(client *http.Client, url string) int {
	cfg := &Config{Sites: []SiteConfig{{URL: url}}}
	if err := cfg.applyDefaults(); err != nil {
		fmt.Println(err)
		return 2
	}
	w := &HTTPWatcher{cfg: cfg.Sites[0], client: client}
	r := w.Check(context.Background())
	if !r.OK {
		fmt.Printf("DOWN  %s  %s\n", w.Target(), r.Err)
		return 1
	}
	fmt.Printf("UP    %s  %d  %.0f ms", w.Target(), r.Status, r.Value)
	if r.CertDays != 0 {
		fmt.Printf("  cert %dd", r.CertDays)
	}
	fmt.Println()
	return 0
}
