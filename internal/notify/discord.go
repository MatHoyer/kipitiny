package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var discordKind = Kind{
	Name:  "discord",
	Label: "Discord",
	Fields: []ConfigField{{
		Key:         "webhookUrl",
		Label:       "Webhook URL",
		Placeholder: "https://discord.com/api/webhooks/…",
		Required:    true,
		Secret:      true,
	}},
	New: newDiscord,
}

// Discord hosts that serve webhooks; anything else is refused so a channel
// can't be used to make the manager call arbitrary URLs.
var discordHosts = map[string]bool{
	"discord.com":        true,
	"discordapp.com":     true,
	"ptb.discord.com":    true,
	"canary.discord.com": true,
}

func newDiscord(cfg map[string]string, hc *http.Client) (Sender, error) {
	u, err := url.Parse(strings.TrimSpace(cfg["webhookUrl"]))
	if err != nil || u.Scheme != "https" || !discordHosts[u.Hostname()] || !strings.HasPrefix(u.Path, "/api/webhooks/") {
		return nil, fmt.Errorf("%w: not a Discord webhook URL (https://discord.com/api/webhooks/…)", ErrConfig)
	}
	return &discord{url: u.String(), hc: hc}, nil
}

type discord struct {
	url string
	hc  *http.Client
}

var discordColors = map[Level]int{
	Info:    0x5865f2,
	Success: 0x22c55e,
	Warning: 0xf59e0b,
	Error:   0xef4444,
}

type discordMessage struct {
	Username string         `json:"username"`
	Embeds   []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	URL         string         `json:"url,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Timestamp   string         `json:"timestamp"`
	Footer      discordFooter  `json:"footer"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordFooter struct {
	Text string `json:"text"`
}

// discordPayload respects Discord's embed limits.
func discordPayload(e Event) discordMessage {
	em := discordEmbed{
		Title:       truncate(e.Title, 256),
		Description: truncate(e.Message, 4096),
		URL:         e.URL,
		Color:       discordColors[e.Level],
		Timestamp:   e.Time.UTC().Format(time.RFC3339),
		Footer:      discordFooter{Text: e.Type},
	}
	for i, f := range e.Fields {
		if i == 25 {
			break
		}
		if f.Value == "" {
			continue
		}
		em.Fields = append(em.Fields, discordField{Name: truncate(f.Name, 256), Value: truncate(f.Value, 1024), Inline: true})
	}
	return discordMessage{Username: "kipitiny", Embeds: []discordEmbed{em}}
}

func (d *discord) Send(ctx context.Context, e Event) error {
	body, err := json.Marshal(discordPayload(e))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := d.hc.Do(req)
	if err != nil {
		// The URL holds the webhook token: keep it out of errors and logs.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return fmt.Errorf("discord: request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("discord: %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
