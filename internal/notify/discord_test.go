package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscordURL(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://discord.com/api/webhooks/1/abc", true},
		{"https://discordapp.com/api/webhooks/1/abc", true},
		{"https://canary.discord.com/api/webhooks/1/abc", true},
		{"  https://discord.com/api/webhooks/1/abc  ", true},
		{"http://discord.com/api/webhooks/1/abc", false},
		{"https://discord.com/channels/1/2", false},
		{"https://evil.example/api/webhooks/1/abc", false},
		{"https://discord.com.evil.example/api/webhooks/1/abc", false},
		{"", false},
	}
	for _, tt := range tests {
		_, err := New("discord", map[string]string{"webhookUrl": tt.url}, nil)
		if (err == nil) != tt.ok {
			t.Errorf("%q: got %v, want ok=%v", tt.url, err, tt.ok)
		}
		if err != nil && !errors.Is(err, ErrConfig) {
			t.Errorf("%q: error should wrap ErrConfig: %v", tt.url, err)
		}
	}
	if _, err := New("pigeon", nil, nil); !errors.Is(err, ErrConfig) {
		t.Errorf("unknown kind: %v", err)
	}
}

func TestDiscordPayload(t *testing.T) {
	e := Event{
		Type:    "deploy.failed",
		Level:   Error,
		Title:   strings.Repeat("t", 300),
		Message: "boom",
		Fields:  []Field{{"Project", "shop"}, {"Image", ""}},
		Time:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	m := discordPayload(e)
	em := m.Embeds[0]
	if len([]rune(em.Title)) != 256 || !strings.HasSuffix(em.Title, "…") {
		t.Errorf("title not truncated: %d runes", len([]rune(em.Title)))
	}
	if em.Color != discordColors[Error] || em.Timestamp != "2026-01-02T03:04:05Z" || em.Footer.Text != "deploy.failed" {
		t.Errorf("embed = %+v", em)
	}
	if len(em.Fields) != 1 || em.Fields[0].Value != "shop" {
		t.Errorf("empty fields must be skipped: %+v", em.Fields)
	}
}

func TestDiscordSend(t *testing.T) {
	var got discordMessage
	status := http.StatusNoContent
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(status)
		w.Write([]byte(`{"message": "Unknown Webhook"}`))
	}))
	defer srv.Close()
	d := &discord{url: srv.URL + "/api/webhooks/1/secret-token", hc: srv.Client()}

	if err := d.Send(context.Background(), Event{Title: "hi", Level: Info}); err != nil {
		t.Fatal(err)
	}
	if got.Username != "kipitiny" || got.Embeds[0].Title != "hi" {
		t.Errorf("got %+v", got)
	}

	status = http.StatusNotFound
	err := d.Send(context.Background(), Event{Title: "hi"})
	if err == nil || !strings.Contains(err.Error(), "Unknown Webhook") {
		t.Errorf("error status: %v", err)
	}

	srv.Close()
	err = d.Send(context.Background(), Event{Title: "hi"})
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Errorf("transport error must not leak the webhook URL: %v", err)
	}
}
