package notify

import (
	"context"
	"errors"
	netmail "net/mail"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/mail"

	"github.com/kipit-labs/kipitiny/internal/domain/notify"
)

func TestEmailRecipients(t *testing.T) {
	for _, to := range []string{"", "ops", "ops@example.com, nope"} {
		if _, err := New("email", map[string]string{"to": to, "provider": "p1"}, notify.Deps{}); !errors.Is(err, notify.ErrConfig) {
			t.Errorf("%q: got %v", to, err)
		}
	}
	if _, err := New("email", map[string]string{"to": "ops@example.com"}, notify.Deps{}); !errors.Is(err, notify.ErrConfig) {
		t.Errorf("no provider: got %v", err)
	}
	if _, err := New("email", map[string]string{"to": "ops@example.com, Oncall <oncall@example.com>", "provider": "p1"}, notify.Deps{}); err != nil {
		t.Fatal(err)
	}
}

func TestEmailSend(t *testing.T) {
	var gotTo []*netmail.Address
	var got mail.Body
	var subject, provider string
	s, err := New("email", map[string]string{"to": "ops@example.com", "provider": "p1"}, notify.Deps{
		Mail: func(_ context.Context, p string, to []*netmail.Address, b mail.Body, subj string) error {
			provider, gotTo, got, subject = p, to, b, subj
			return nil
		},
		Brand: "Acme ops",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(context.Background(), notify.Event{
		Type:    "deploy.failed",
		Level:   notify.Error,
		Title:   "shop/web deployment failed",
		Message: "exit 1",
		Fields:  []notify.Field{{Name: "Project", Value: "shop"}},
		URL:     "https://kipitiny.example.com/services/1",
		Time:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider != "p1" || len(gotTo) != 1 || subject != "[Acme ops] shop/web deployment failed" || got.Accent != emailColors[notify.Error] {
		t.Errorf("to %v, subject %q, body %+v", gotTo, subject, got)
	}
	if got.Button == nil || got.Button.URL != "https://kipitiny.example.com/services/1" || got.Footer != "Acme ops · deploy.failed · 2026-01-02 03:04 UTC" || got.Button.Label != "Open in Acme ops" {
		t.Errorf("body = %+v", got)
	}

	noMail, _ := New("email", map[string]string{"to": "ops@example.com", "provider": "p1"}, notify.Deps{})
	if err := noMail.Send(context.Background(), notify.Event{}); err == nil {
		t.Error("sent without a way to mail")
	}
}
