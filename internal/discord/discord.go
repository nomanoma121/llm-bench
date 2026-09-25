// Package discord sends link-only notifications to a webhook. It never
// carries review content: the Issue remains the canonical record.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notifier posts messages to a Discord webhook.
type Notifier struct {
	Webhook string
	Client  *http.Client
}

// New builds a notifier for the webhook URL. An empty webhook makes Notify a
// no-op so operators can leave the setting blank.
func New(webhook string) *Notifier {
	return &Notifier{Webhook: webhook, Client: &http.Client{Timeout: 10 * time.Second}}
}

// Notify implements review.Notifier.
func (n *Notifier) Notify(ctx context.Context, message string) error {
	if n.Webhook == "" {
		return nil
	}
	body, err := json.Marshal(map[string]string{"content": message})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.Webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.Client.Do(req)
	if err != nil {
		return fmt.Errorf("discord: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord: unexpected status %d", resp.StatusCode)
	}
	return nil
}
