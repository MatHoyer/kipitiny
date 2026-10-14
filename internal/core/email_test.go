package core

import (
	"context"
	"errors"
	netmail "net/mail"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/mail"
	"github.com/MatHoyer/kipitiny/internal/mail/mailtest"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// localProvider is an email provider sending to a fake server.
func localProvider(name string, srv *mailtest.Server) EmailProviderInput {
	return EmailProviderInput{Name: name, Config: mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, From: "ops@example.com"}}
}

func TestEmailProviders(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{Domain: "kipitiny.example.com"})
	if v, err := c.Email(ctx); err != nil || len(v.Providers) != 0 || v.Invites != "" {
		t.Fatalf("default = %+v, %v", v, err)
	}

	a, b := mailtest.NewServer(t), mailtest.NewServer(t)
	clear := localProvider("relay", a)
	clear.Username, clear.Password = "u", "p"
	if _, err := c.CreateEmailProvider(ctx, clear); !errors.Is(err, ErrInvalid) {
		t.Errorf("password in clear text: %v", err)
	}
	if _, err := c.CreateEmailProvider(ctx, EmailProviderInput{Config: localProvider("", a).Config}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no name: %v", err)
	}
	in := localProvider(" invites ", a)
	in.From = " ops@example.com "
	pa, err := c.CreateEmailProvider(ctx, in)
	if err != nil || pa.Name != "invites" || pa.From != "ops@example.com" {
		t.Fatalf("created = %+v, %v", pa, err)
	}
	if _, err := c.CreateEmailProvider(ctx, localProvider("invites", b)); !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate name: %v", err)
	}
	pb, err := c.CreateEmailProvider(ctx, localProvider("alerts", b))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Email(ctx); len(v.Providers) != 2 || v.Invites != pa.ID {
		t.Errorf("invites default to the oldest: %+v", v)
	}

	if err := c.TestEmailProvider(ctx, pb.ID, "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad recipient: %v", err)
	}
	if err := c.TestEmailProvider(ctx, pb.ID, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	m := <-b.Messages
	if m.To[0] != "RCPT TO:<a@example.com>" || !strings.Contains(m.Data, "Sent by kipitiny on kipitiny.example.com") || !strings.Contains(m.Data, "through alerts") {
		t.Errorf("sent = %+v", m)
	}

	if v, err := c.SetEmailInvites(ctx, pb.ID); err != nil || v.Invites != pb.ID {
		t.Fatalf("invites = %+v, %v", v, err)
	}
	if _, err := c.SetEmailInvites(ctx, "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown provider: %v", err)
	}
	if err := c.sendMail(ctx, "", []*netmail.Address{{Address: "b@example.com"}}, mail.Body{Title: "hi"}, ""); err != nil {
		t.Fatal(err)
	}
	if m := <-b.Messages; m.To[0] != "RCPT TO:<b@example.com>" {
		t.Errorf("invites went elsewhere: %+v", m)
	}

	// Deleting the invites provider falls back to the other.
	if err := c.DeleteEmailProvider(ctx, pb.ID); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Email(ctx); len(v.Providers) != 1 || v.Invites != pa.ID {
		t.Errorf("after delete: %+v", v)
	}
}

func TestEmailPasswordMasked(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	// Stored directly: signing in needs TLS, which the fake server lacks.
	p, err := c.store.CreateEmailProvider(ctx, store.EmailProvider{Name: "x", Host: "smtp.example.com", Port: 587, Security: "starttls", Username: "u", Password: "secret", From: "ops@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Email(ctx); v.Providers[0].Password != SecretMask {
		t.Errorf("password leaked: %+v", v)
	}
	// A masked password keeps the stored one; the server check fails, nothing is saved.
	if _, err := c.UpdateEmailProvider(ctx, p.ID, EmailProviderInput{Name: "x", Config: mail.Config{Host: "127.0.0.1", Port: 1, Security: mail.StartTLS, Username: "u", Password: SecretMask, From: "ops@example.com"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unreachable: %v", err)
	}
	if got, _ := c.store.GetEmailProvider(ctx, p.ID); got.Password != "secret" || got.Host != "smtp.example.com" {
		t.Errorf("stored = %+v", got)
	}
}

func TestEmailChannel(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if _, err := c.CreateChannel(ctx, ChannelInput{Name: "ops", Kind: "email", Config: map[string]string{"to": "ops@example.com", "provider": "nope"}, Enabled: true}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Settings › Email") {
		t.Errorf("unknown provider: %v", err)
	}

	a, b := mailtest.NewServer(t), mailtest.NewServer(t)
	if _, err := c.CreateEmailProvider(ctx, localProvider("invites", a)); err != nil {
		t.Fatal(err)
	}
	pb, err := c.CreateEmailProvider(ctx, localProvider("alerts", b))
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Notifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range v.Kinds {
		for _, f := range k.Fields {
			if f.Key == "provider" && (len(f.Options) != 2 || f.Options[0].Label != "invites") {
				t.Errorf("options = %+v", f.Options)
			}
		}
	}
	ch, err := c.CreateChannel(ctx, ChannelInput{Name: "ops", Kind: "email", Config: map[string]string{"to": "ops@example.com", "provider": pb.ID}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TestChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if m := <-b.Messages; m.To[0] != "RCPT TO:<ops@example.com>" || !strings.Contains(m.Data, "Test notification") {
		t.Errorf("sent = %+v", m)
	}
	if err := c.DeleteEmailProvider(ctx, pb.ID); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "ops") {
		t.Errorf("delete while used: %v", err)
	}
}

func TestMigrateSMTPSetting(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	if err := c.store.SetSetting(ctx, smtpSetting, `{"host":"smtp.resend.com","port":465,"security":"tls","username":"resend","password":"re_x","from":"ops@example.com"}`); err != nil {
		t.Fatal(err)
	}
	ch, err := c.store.CreateNotificationChannel(ctx, store.NotificationChannel{Name: "ops", Kind: "email", Config: map[string]string{"to": "ops@example.com"}, Events: []string{}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // a rerun changes nothing
		if err := c.migrateSMTPSetting(ctx); err != nil {
			t.Fatal(err)
		}
	}
	v, _ := c.Email(ctx)
	if len(v.Providers) != 1 || v.Providers[0].Name != "Resend" || v.Invites != v.Providers[0].ID {
		t.Fatalf("email = %+v", v)
	}
	if got, _ := c.store.GetNotificationChannel(ctx, ch.ID); got.Config["provider"] != v.Providers[0].ID {
		t.Errorf("channel = %+v", got.Config)
	}
	if s, _ := c.store.GetSetting(ctx, smtpSetting); s != "" {
		t.Errorf("smtp setting kept: %q", s)
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
	if _, err := c.CreateEmailProvider(ctx, localProvider("relay", srv)); err != nil {
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
