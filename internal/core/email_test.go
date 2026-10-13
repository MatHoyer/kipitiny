package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/mail"
	"github.com/MatHoyer/kipitiny/internal/mail/mailtest"
)

func TestEmailSettings(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Domain: "kipitiny.example.com"})
	if v := c.Email(ctx); v.Configured || v.Settings.Port != 587 || v.Settings.Security != mail.StartTLS {
		t.Fatalf("default = %+v", v)
	}
	if err := c.TestEmail(ctx, "a@example.com"); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("test without settings: %v", err)
	}

	srv := mailtest.NewServer(t)
	in := mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, From: " ops@example.com "}
	if _, err := c.SetEmail(ctx, mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, Username: "u", Password: "p", From: "ops@example.com"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("password in clear text: %v", err)
	}
	v, err := c.SetEmail(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Configured || v.Settings.From != "ops@example.com" {
		t.Errorf("saved = %+v", v)
	}

	if err := c.TestEmail(ctx, "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad recipient: %v", err)
	}
	if err := c.TestEmail(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	m := <-srv.Messages
	if m.To[0] != "RCPT TO:<a@example.com>" || !strings.Contains(m.Data, "Sent by kipitiny on kipitiny.example.com") {
		t.Errorf("sent = %+v", m)
	}

	if err := c.DeleteEmail(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Email(ctx).Configured {
		t.Error("still configured")
	}
}

func TestEmailPasswordMasked(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	// Stored directly: signing in needs TLS, which the fake server lacks.
	if err := c.store.SetSetting(ctx, smtpSetting, `{"host":"smtp.example.com","port":587,"security":"starttls","username":"u","password":"secret","from":"ops@example.com"}`); err != nil {
		t.Fatal(err)
	}
	if v := c.Email(ctx); v.Settings.Password != SecretMask {
		t.Errorf("password leaked: %+v", v)
	}
}
