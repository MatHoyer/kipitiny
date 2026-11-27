package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/mail/mailtest"
)

var pngLogo = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

func ptr[T any](v T) *T { return &v }

func TestSetBranding(t *testing.T) {
	c := newTestCore(t, config.Config{})
	ctx := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})
	if got := c.Branding(ctx); got != (Branding{}) || c.BrandName(ctx) != domain.DefaultBrandName {
		t.Fatalf("default: %+v", got)
	}

	b, err := c.SetBranding(ctx, BrandingInput{Name: " Acme ops ", Theme: Theme{Accent: "#E11D48", AccentDark: "#FB7185", Tint: "#be123c", Radius: "large", Font: "serif", Density: "compact"},
		Logo: ptr("data:image/gif;base64," + base64.StdEncoding.EncodeToString(pngLogo))})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Acme ops" || b.Accent != "#e11d48" || b.AccentDark != "#fb7185" || b.Radius != "large" || b.Font != "serif" || b.Density != "compact" || !strings.HasPrefix(b.Logo, "/api/branding/logo?v=") {
		t.Fatalf("set: %+v", b)
	}
	data, typ, err := c.BrandingLogo(ctx)
	if err != nil || typ != "image/png" || !bytes.Equal(data, pngLogo) {
		t.Fatalf("logo: %q %v", typ, err)
	}

	// No logo keeps it; "" removes it.
	if b2, _ := c.SetBranding(ctx, BrandingInput{Name: "Acme"}); b2.Logo != b.Logo || b2.Theme != (Theme{}) {
		t.Errorf("kept logo: %+v", b2)
	}
	if b2, _ := c.SetBranding(ctx, BrandingInput{Name: "Acme", Logo: ptr("")}); b2.Logo != "" {
		t.Errorf("removed logo: %+v", b2)
	}
	if _, _, err := c.BrandingLogo(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("removed logo served: %v", err)
	}

	if err := c.ResetBranding(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.Branding(ctx); got != (Branding{}) {
		t.Errorf("reset: %+v", got)
	}
}

func TestSetBrandingValidates(t *testing.T) {
	c := newTestCore(t, config.Config{})
	ctx := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})
	b64 := func(s string) *string { return ptr(base64.StdEncoding.EncodeToString([]byte(s))) }
	tests := []struct {
		name    string
		in      BrandingInput
		wantErr bool
	}{
		{"empty", BrandingInput{}, false},
		{"svg", BrandingInput{Logo: b64(`<?xml version="1.0"?><!-- logo --><svg xmlns="http://www.w3.org/2000/svg"></svg>`)}, false},
		{"long name", BrandingInput{Name: strings.Repeat("a", 41)}, true},
		{"two lines", BrandingInput{Name: "a\nb"}, true},
		{"full theme", BrandingInput{Theme: Theme{Accent: "#5e81ac", AccentDark: "#88c0d0", Tint: "#4c566a", Radius: "none", Font: "mono", Density: "comfortable"}}, false},
		{"named colour", BrandingInput{Theme: Theme{Accent: "red"}}, true},
		{"short hex", BrandingInput{Theme: Theme{Accent: "#fff"}}, true},
		{"bad dark accent", BrandingInput{Theme: Theme{AccentDark: "blue"}}, true},
		{"bad tint", BrandingInput{Theme: Theme{Tint: "#12345g"}}, true},
		{"unknown radius", BrandingInput{Theme: Theme{Radius: "huge"}}, true},
		{"unknown font", BrandingInput{Theme: Theme{Font: "comic"}}, true},
		{"unknown density", BrandingInput{Theme: Theme{Density: "airy"}}, true},
		{"html", BrandingInput{Logo: b64(`<html><svg></svg></html>`)}, true},
		{"text", BrandingInput{Logo: b64("hello")}, true},
		{"not base64", BrandingInput{Logo: ptr("data:image/png;base64,***")}, true},
		{"not a base64 data URL", BrandingInput{Logo: ptr("data:image/svg+xml,<svg></svg>")}, true},
		{"too big", BrandingInput{Logo: ptr(base64.StdEncoding.EncodeToString(append(pngLogo, make([]byte, MaxBrandLogo)...)))}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := c.SetBranding(ctx, tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalid) {
				t.Errorf("not ErrInvalid: %v", err)
			}
		})
	}

	member := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "alice", Role: domain.RoleMember})
	if _, err := c.SetBranding(member, BrandingInput{Name: "x"}); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("member: %v", err)
	}
	if err := c.ResetBranding(member); !errors.Is(err, identity.ErrForbidden) {
		t.Errorf("member reset: %v", err)
	}
}

func TestBrandedEmail(t *testing.T) {
	c := newTestCore(t, config.Config{Domain: "ops.example.com"})
	ctx := identity.WithActor(context.Background(), identity.Actor{Kind: "user", Name: "admin", Role: domain.RoleAdmin})
	srv := mailtest.NewServer(t)
	p, err := c.CreateEmailProvider(ctx, localProvider("relay", srv))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetBranding(ctx, BrandingInput{Name: "Acme ops", Theme: Theme{Accent: "#e11d48"}}); err != nil {
		t.Fatal(err)
	}

	to := []*netmail.Address{{Address: "a@example.com"}}
	inv := domain.Invite{Role: domain.RoleAdmin, ExpiresAt: time.Now().Add(time.Hour)}
	if err := c.SendMail(ctx, "", to, c.InviteBody(ctx, inv, "https://ops.example.com/invite#t"), ""); err != nil {
		t.Fatal(err)
	}
	m := <-srv.Messages
	for _, want := range []string{"Subject: You're invited to Acme ops", "invited you to Acme ops (ops.example.com)", "Sent by Acme ops on ops.example.com", "background:#e11d48"} {
		if !strings.Contains(m.Data, want) {
			t.Errorf("no %q in\n%s", want, m.Data)
		}
	}

	// Notifications keep their level's colour.
	if err := c.TestEmailProvider(ctx, p.ID, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if m := <-srv.Messages; !strings.Contains(m.Data, "Test email from Acme ops") || strings.Contains(m.Data, "#e11d48") {
		t.Errorf("test email:\n%s", m.Data)
	}
}
