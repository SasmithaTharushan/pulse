# Pulse

**Website uptime + stock price watcher written in Go. Runs 24/7 for free on GitHub Actions and publishes a live status dashboard on GitHub Pages.**

🔗 **Live dashboard:** `https://sasmithatharushan.github.io/pulse/`

Pulse combines two of my earlier projects, [websiteHealthChecker](https://github.com/SasmithaTharushan/websiteHealthChecker) and [stockmarketPriceScrapper](https://github.com/SasmithaTharushan/stockmarketPriceScrapper), into one service built around a single `Watcher` interface.

## Features
- **Website monitoring:** status code, response time, uptime %, TLS certificate expiry
- **Stock / crypto prices:** price, daily % change and a price sparkline (Yahoo Finance, no API key needed; Finnhub optional)
- **Alerts on Discord or Telegram:** site down/up (after N consecutive failures), large daily price moves, certificates expiring soon
- **Static dashboard** with light/dark mode, plus raw data at `/history.json`
- **Concurrent checks** with goroutines and per-check timeouts
- **Zero cost, no server:** GitHub Actions runs it every 15 minutes, history lives on a `data` branch, and Pages hosts the dashboard

## How it works
```
 GitHub Actions (cron */15)
        │
        ▼
  pulse run ──► Watchers (goroutines) ──► HTTPWatcher / StockWatcher
        │                                        │
        ├── history.json  ◄──────────────────────┘   (stored on the `data` branch)
        ├── alerts ──► Discord / Telegram
        └── site/index.html ──► GitHub Pages
```

Adding a new kind of monitor means implementing one interface:
```go
type Watcher interface {
    ID() string
    Kind() string
    Label() string
    Target() string
    Check(ctx context.Context) Result
    Alerts(s *Series) []string
}
```

## Deploy your own (free, about 5 minutes)
1. Create a **public** GitHub repo named `pulse` and push this code to `main`.
   (Public repos get unlimited free Actions minutes. A private repo would use up the free quota.)
2. **Settings → Pages → Build and deployment → Source: GitHub Actions.**
3. Edit `pulse.json` with your sites and tickers.
4. *(Optional)* add alert secrets under **Settings → Secrets and variables → Actions**:
   | Secret | What it is |
   |---|---|
   | `DISCORD_WEBHOOK_URL` | Discord channel → Edit → Integrations → Webhooks → New → Copy URL |
   | `TELEGRAM_BOT_TOKEN` + `TELEGRAM_CHAT_ID` | Create a bot with @BotFather and get your chat id from @userinfobot |
   | `FINNHUB_TOKEN` | Free key from finnhub.io, if Yahoo ever starts blocking requests |
5. **Actions → pulse → Run workflow.** Your dashboard will be at `https://<username>.github.io/pulse/`.

## Run locally
```bash
go run . check https://example.com   # one-off health check (exit 1 if down)
go run . run                         # check once, write site/index.html
go run . serve                       # check every 15 min, dashboard at http://localhost:8080
go test ./...
```

## Configuration (`pulse.json`)
```json
{
  "title": "Pulse Status",
  "keep": 672,
  "sites":  [{ "name": "GitHub", "url": "https://github.com", "timeoutSeconds": 10, "failThreshold": 2 }],
  "stocks": [{ "symbol": "AAPL", "name": "Apple", "alertPercent": 3 }]
}
```
`keep` is the number of results stored per watcher (672 is 7 days at 15-minute checks). Any Yahoo symbol works, including `BTC-USD` and `^GSPC`.

## Free-tier notes
- GitHub may delay scheduled runs by a few minutes when it's busy, so checks are roughly every 15 minutes rather than exactly.
- GitHub pauses scheduled workflows in repos with no activity for 60 days. If that happens you'll get an email; re-enable it with one click in the Actions tab.
- Yahoo's chart endpoint is unofficial. If it starts failing, add a free `FINNHUB_TOKEN` secret and Pulse switches to Finnhub automatically.

## Tech
Go standard library only (no dependencies): `net/http`, `html/template`, `embed`, goroutines, `context`.
