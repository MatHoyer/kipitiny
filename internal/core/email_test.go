package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/mail"
	"github.com/MatHoyer/kipitiny/internal/mail/mailtest"
	"github.com/MatHoyer/kipitiny/internal/store"
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

func TestEmailChannel(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	ch, err := c.CreateChannel(ctx, ChannelInput{Name: "ops", Kind: "email", Config: map[string]string{"to": "ops@example.com"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TestChannel(ctx, ch.ID); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Settings › Email") {
		t.Errorf("without SMTP: %v", err)
	}

	srv := mailtest.NewServer(t)
	if _, err := c.SetEmail(ctx, mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, From: "ops@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := c.TestChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if m := <-srv.Messages; m.To[0] != "RCPT TO:<ops@example.com>" || !strings.Contains(m.Data, "Test notification") {
		t.Errorf("sent = %+v", m)
	}
}

func TestInviteEmail(t *testing.T) {
	c := newTestCore(t, config.Config{Domain: "kipitiny.example.com"})
	admin, err := c.store.CreateUser(context.Background(), store.User{Username: "boss", PasswordHash: "x", Role: store.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithActor(context.Background(), Actor{Kind: "user", UserID: admin.ID, Role: store.RoleAdmin, Scope: store.ScopeAdmin})
	in := InviteInput{UserAccess: UserAccess{Role: store.RoleAdmin}, Email: "new@example.com", Link: "https://kipitiny.example.com/invite"}
	if _, err := c.CreateInvite(ctx, in); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("without SMTP: %v", err)
	}
	srv := mailtest.NewServer(t)
	if _, err := c.SetEmail(ctx, mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, From: "ops@example.com"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []InviteInput{
		{UserAccess: in.UserAccess, Email: "nope", Link: in.Link},
		{UserAccess: in.UserAccess, Email: in.Email, Link: "javascript:alert(1)"},
		{UserAccess: in.UserAccess, Email: in.Email, Link: in.Link + "#x"},
	} {
		if _, err := c.CreateInvite(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	inv, err := c.CreateInvite(ctx, in)
	if err != nil || inv.EmailError != "" || inv.Email != "new@example.com" {
		t.Fatalf("invite = %+v, %v", inv, err)
	}
	m := <-srv.Messages
	if m.To[0] != "RCPT TO:<new@example.com>" || !strings.Contains(m.Data, "boss invited you to the kipitiny server kipitiny.example.com") {
		t.Errorf("sent = %+v", m)
	}
	if !strings.Contains(strings.ReplaceAll(m.Data, "=\r\n", ""), "https://kipitiny.example.com/invite#"+inv.Token) {
		t.Error("link missing from the email")
	}

	// Without an email, nothing is sent: the admin copies the link.
	if inv, err := c.CreateInvite(ctx, InviteInput{UserAccess: in.UserAccess}); err != nil || inv.Email != "" {
		t.Fatalf("plain invite = %+v, %v", inv, err)
	}
	select {
	case m := <-srv.Messages:
		t.Errorf("sent %+v", m)
	default:
	}
}
