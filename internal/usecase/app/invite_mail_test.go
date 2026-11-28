package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/infrastructure/smtp/smtptest"
	"github.com/kipit-labs/kipitiny/internal/usecase/identity"
	"github.com/kipit-labs/kipitiny/internal/usecase/monitoring"
)

func TestInviteEmail(t *testing.T) {
	c := newTestCore(t, config.Config{Domain: "kipitiny.example.com"})
	admin, err := c.store.CreateUser(context.Background(), domain.User{Username: "boss", PasswordHash: "x", Role: domain.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithActor(context.Background(), identity.Actor{Kind: "user", UserID: admin.ID, Role: domain.RoleAdmin})
	in := identity.InviteInput{UserAccess: identity.UserAccess{Role: domain.RoleAdmin}, Email: "new@example.com", Link: "https://kipitiny.example.com/invite"}
	if _, err := c.CreateInvite(ctx, in); !errors.Is(err, monitoring.ErrNoEmail) {
		t.Fatalf("without SMTP: %v", err)
	}
	srv := smtptest.NewServer(t)
	if _, err := c.CreateEmailProvider(ctx, localProvider("relay", srv)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []identity.InviteInput{
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
	if inv, err := c.CreateInvite(ctx, identity.InviteInput{UserAccess: in.UserAccess}); err != nil || inv.Email != "" {
		t.Fatalf("plain invite = %+v, %v", inv, err)
	}
	select {
	case m := <-srv.Messages:
		t.Errorf("sent %+v", m)
	default:
	}
}
