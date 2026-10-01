package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/notify"
)

const webhook = "https://discord.com/api/webhooks/1/secret"

func TestChannelValidationAndMasking(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	in := ChannelInput{Name: "ops", Kind: "discord", Config: map[string]string{"webhookUrl": webhook}, Events: []string{EventDeployFailed}, Enabled: true}

	bad := []func(*ChannelInput){
		func(in *ChannelInput) { in.Name = " " },
		func(in *ChannelInput) { in.Kind = "pigeon" },
		func(in *ChannelInput) { in.Config = map[string]string{"webhookUrl": "https://example.com/hook"} },
		func(in *ChannelInput) { in.Config = nil },
		func(in *ChannelInput) { in.Events = []string{"nope"} },
	}
	for i, mutate := range bad {
		b := in
		mutate(&b)
		if _, err := c.CreateChannel(ctx, b); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: got %v, want ErrInvalid", i, err)
		}
	}

	ch, err := c.CreateChannel(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Config["webhookUrl"] != SecretMask {
		t.Errorf("created channel leaks its webhook: %v", ch.Config)
	}
	v, err := c.Notifications(ctx)
	if err != nil || len(v.Channels) != 1 || v.Channels[0].Config["webhookUrl"] != SecretMask {
		t.Fatalf("list = %+v, %v", v.Channels, err)
	}

	// Sending the mask back keeps the stored webhook.
	in.Config["webhookUrl"], in.Name, in.Events = SecretMask, "alerts", []string{EventBackupFailed, EventBackupFailed}
	if _, err := c.UpdateChannel(ctx, ch.ID, in); err != nil {
		t.Fatal(err)
	}
	stored, _ := c.store.GetNotificationChannel(ctx, ch.ID)
	if stored.Config["webhookUrl"] != webhook || stored.Name != "alerts" || len(stored.Events) != 1 {
		t.Errorf("stored = %+v", stored)
	}
}

// redirect sends every request to srv, whatever its URL.
type redirect struct{ srv *httptest.Server }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.srv.URL)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return r.srv.Client().Transport.RoundTrip(req)
}

func TestNotifyDispatch(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var titles []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct{ Embeds []struct{ Title, URL string } }
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		titles = append(titles, r.URL.Path+" "+m.Embeds[0].Title+" "+m.Embeds[0].URL)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newTestCore(t, config.Config{Domain: "kipi.example.com", Traefik: config.Traefik{Enabled: true}})
	c.notifyHTTP = &http.Client{Transport: redirect{srv}}

	add := func(name, hook string, enabled bool, events ...string) {
		_, err := c.CreateChannel(ctx, ChannelInput{Name: name, Kind: "discord", Config: map[string]string{"webhookUrl": hook}, Events: events, Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("a", "https://discord.com/api/webhooks/a/x", true, EventDeployFailed, EventServiceRestarted)
	add("b", "https://discord.com/api/webhooks/b/x", true, EventBackupFailed)
	add("off", "https://discord.com/api/webhooks/off/x", false, EventDeployFailed)

	c.notify(notify.Event{Type: EventDeployFailed, Title: "deploy"}, "/services/S1", "")
	c.notify(notify.Event{Type: EventServiceRestarted, Title: "crash 1"}, "", "restarted:S1")
	c.notify(notify.Event{Type: EventServiceRestarted, Title: "crash 2"}, "", "restarted:S1")
	c.ops.Wait()

	want := map[string]bool{
		"/api/webhooks/a/x deploy https://kipi.example.com/services/S1": true,
		"/api/webhooks/a/x crash 1 ":                                    true,
	}
	if len(titles) != len(want) {
		t.Fatalf("sent %q, want %d messages", titles, len(want))
	}
	for _, got := range titles {
		if !want[got] {
			t.Errorf("unexpected message %q", got)
		}
	}

	// The cooldown ends.
	c.notified.Store("restarted:S1", time.Now().Add(-notifyCooldown))
	c.notify(notify.Event{Type: EventServiceRestarted, Title: "crash 3"}, "", "restarted:S1")
	c.ops.Wait()
	if len(titles) != 3 {
		t.Errorf("after cooldown: %q", titles)
	}
}

func TestTestChannel(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := newTestCore(t, config.Config{})
	c.notifyHTTP = &http.Client{Transport: redirect{srv}}
	ch, err := c.CreateChannel(ctx, ChannelInput{Name: "ops", Kind: "discord", Config: map[string]string{"webhookUrl": webhook}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TestChannel(ctx, ch.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("failed delivery: got %v, want ErrInvalid", err)
	}
}
