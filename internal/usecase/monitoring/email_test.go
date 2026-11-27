package monitoring

import (
	"context"
	"errors"
	netmail "net/mail"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/mail"
	"github.com/kipit-labs/kipitiny/internal/mail/mailtest"
)

// localProvider is an email provider sending to a fake server.
func localProvider(name string, srv *mailtest.Server) EmailProviderInput {
	return EmailProviderInput{Name: name, Config: mail.Config{Host: "127.0.0.1", Port: srv.Port, Security: mail.None, From: "ops@example.com"}}
}

func TestEmailProviders(t *testing.T) {
	ctx := context.Background()
	c := newTestMonitoring(t, config.Config{Domain: "kipitiny.example.com"})
	if v, err := c.Email(ctx); err != nil || len(v.Providers) != 0 || v.Invites != "" {
		t.Fatalf("default = %+v, %v", v, err)
	}

	a, b := mailtest.NewServer(t), mailtest.NewServer(t)
	clear := localProvider("relay", a)
	clear.Username, clear.Password = "u", "p"
	if _, err := c.CreateEmailProvider(ctx, clear); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("password in clear text: %v", err)
	}
	if _, err := c.CreateEmailProvider(ctx, EmailProviderInput{Config: localProvider("", a).Config}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("no name: %v", err)
	}
	in := localProvider(" invites ", a)
	in.From = " ops@example.com "
	pa, err := c.CreateEmailProvider(ctx, in)
	if err != nil || pa.Name != "invites" || pa.From != "ops@example.com" {
		t.Fatalf("created = %+v, %v", pa, err)
	}
	if _, err := c.CreateEmailProvider(ctx, localProvider("invites", b)); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("duplicate name: %v", err)
	}
	pb, err := c.CreateEmailProvider(ctx, localProvider("alerts", b))
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Email(ctx); len(v.Providers) != 2 || v.Invites != pa.ID {
		t.Errorf("invites default to the oldest: %+v", v)
	}

	if err := c.TestEmailProvider(ctx, pb.ID, "nope"); !errors.Is(err, domain.ErrInvalid) {
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
	if _, err := c.SetEmailInvites(ctx, "nope"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown provider: %v", err)
	}
	if err := c.SendMail(ctx, "", []*netmail.Address{{Address: "b@example.com"}}, mail.Body{Title: "hi"}, ""); err != nil {
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
	c := newTestMonitoring(t, config.Config{})
	// Stored directly: signing in needs TLS, which the fake server lacks.
	p, err := c.store.CreateEmailProvider(ctx, domain.EmailProvider{Name: "x", Host: "smtp.example.com", Port: 587, Security: "starttls", Username: "u", Password: "secret", From: "ops@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := c.Email(ctx); v.Providers[0].Password != identity.SecretMask {
		t.Errorf("password leaked: %+v", v)
	}
	// A masked password keeps the stored one; the server check fails, nothing is saved.
	if _, err := c.UpdateEmailProvider(ctx, p.ID, EmailProviderInput{Name: "x", Config: mail.Config{Host: "127.0.0.1", Port: 1, Security: mail.StartTLS, Username: "u", Password: identity.SecretMask, From: "ops@example.com"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unreachable: %v", err)
	}
	if got, _ := c.store.GetEmailProvider(ctx, p.ID); got.Password != "secret" || got.Host != "smtp.example.com" {
		t.Errorf("stored = %+v", got)
	}
}

func TestEmailChannel(t *testing.T) {
	ctx := context.Background()
	c := newTestMonitoring(t, config.Config{})
	if _, err := c.CreateChannel(ctx, ChannelInput{Name: "ops", Kind: "email", Config: map[string]string{"to": "ops@example.com", "provider": "nope"}, Enabled: true}); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "Settings › Email") {
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
	if err := c.DeleteEmailProvider(ctx, pb.ID); !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), "ops") {
		t.Errorf("delete while used: %v", err)
	}
}

func TestMigrateSMTPSetting(t *testing.T) {
	ctx := context.Background()
	c := newTestMonitoring(t, config.Config{})
	if err := c.store.SetSetting(ctx, smtpSetting, `{"host":"smtp.resend.com","port":465,"security":"tls","username":"resend","password":"re_x","from":"ops@example.com"}`); err != nil {
		t.Fatal(err)
	}
	ch, err := c.store.CreateNotificationChannel(ctx, domain.NotificationChannel{Name: "ops", Kind: "email", Config: map[string]string{"to": "ops@example.com"}, Events: []string{}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // a rerun changes nothing
		if err := c.MigrateSMTPSetting(ctx); err != nil {
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
