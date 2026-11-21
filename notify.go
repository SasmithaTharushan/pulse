package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

// Notify sends alert messages to Discord and/or Telegram, configured with
// environment variables (GitHub Actions secrets):
//
//	DISCORD_WEBHOOK_URL
//	TELEGRAM_BOT_TOKEN + TELEGRAM_CHAT_ID
func Notify(ctx context.Context, client *http.Client, msgs []string) {
	if len(msgs) == 0 {
		return
	}
	text := strings.Join(msgs, "\n")
	log.Printf("alerts:\n%s", text)

	sent := false
	if hook := os.Getenv("DISCORD_WEBHOOK_URL"); hook != "" {
		sent = true
		if err := postJSON(ctx, client, hook, map[string]string{"content": text}); err != nil {
			log.Printf("discord: %v", err)
		}
	}
	tok, chat := os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID")
	if tok != "" && chat != "" {
		sent = true
		u := "https://api.telegram.org/bot" + tok + "/sendMessage"
		if err := postJSON(ctx, client, u, map[string]string{"chat_id": chat, "text": text}); err != nil {
			log.Printf("telegram: %v", err) // error text never includes the token
		}
	}
	if !sent {
		log.Print("no notifier configured (set DISCORD_WEBHOOK_URL or TELEGRAM_BOT_TOKEN/TELEGRAM_CHAT_ID)")
	}
}

func postJSON(ctx context.Context, client *http.Client, url string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("bad url")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed") // avoid echoing a URL containing secrets
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
