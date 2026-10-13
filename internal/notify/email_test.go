package notify

import (
	"context"
	"errors"
	netmail "net/mail"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/mail"
)

func TestEmailRecipients(t *testing.T) {
	for _, to := range []string{"", "ops", "ops@example.com, nope"} {
		if _, err := New("email", map[string]string{"to": to}, Deps{}); !errors.Is(err, ErrConfig) {
			t.Errorf("%q: got %v", to, err)
		}
	}
	if _, err := New("email", map[string]string{"to": "ops@example.com, Oncall <oncall@example.com>"}, Deps{}); err != nil {
		t.Fatal(err)
	}
}

func TestEmailSend(t *testing.T) {
	var gotTo []*netmail.Address
	var got mail.Body
	var subject string
	s, err := New("email", map[string]string{"to": "ops@example.com"}, Deps{
		Mail: func(_ context.Context, to []*netmail.Address, b mail.Body, subj string) error {
			gotTo, got, subject = to, b, subj
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(context.Background(), Event{
		Type:    "deploy.failed",
		Level:   Error,
		Title:   "shop/web deployment failed",
		Message: "exit 1",
		Fields:  []Field{{"Project", "shop"}},
		URL:     "https://kipitiny.example.com/services/1",
		Time:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotTo) != 1 || subject != "[kipitiny] shop/web deployment failed" || got.Accent != emailColors[Error] {
		t.Errorf("to %v, subject %q, body %+v", gotTo, subject, got)
	}
	if got.Button == nil || got.Button.URL != "https://kipitiny.example.com/services/1" || got.Footer != "kipitiny · deploy.failed · 2026-01-02 03:04 UTC" {
		t.Errorf("body = %+v", got)
	}

	noMail, _ := New("email", map[string]string{"to": "ops@example.com"}, Deps{})
	if err := noMail.Send(context.Background(), Event{}); err == nil {
		t.Error("sent without an SMTP server")
	}
}
