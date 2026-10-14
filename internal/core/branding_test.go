package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

var pngLogo = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

func ptr[T any](v T) *T { return &v }

func TestSetBranding(t *testing.T) {
	c := newTestCore(t, config.Config{})
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "admin", Role: store.RoleAdmin})
	if got := c.Branding(ctx); got != (Branding{}) || c.BrandName(ctx) != DefaultBrandName {
		t.Fatalf("default: %+v", got)
	}

	b, err := c.SetBranding(ctx, BrandingInput{Name: " Acme ops ", Accent: "#E11D48",
		Logo: ptr("data:image/gif;base64," + base64.StdEncoding.EncodeToString(pngLogo))})
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Acme ops" || b.Accent != "#e11d48" || !strings.HasPrefix(b.Logo, "/api/branding/logo?v=") {
		t.Fatalf("set: %+v", b)
	}
	data, typ, err := c.BrandingLogo(ctx)
	if err != nil || typ != "image/png" || !bytes.Equal(data, pngLogo) {
		t.Fatalf("logo: %q %v", typ, err)
	}

	// No logo keeps it; "" removes it.
	if b2, _ := c.SetBranding(ctx, BrandingInput{Name: "Acme"}); b2.Logo != b.Logo || b2.Accent != "" {
		t.Errorf("kept logo: %+v", b2)
	}
	if b2, _ := c.SetBranding(ctx, BrandingInput{Name: "Acme", Logo: ptr("")}); b2.Logo != "" {
		t.Errorf("removed logo: %+v", b2)
	}
	if _, _, err := c.BrandingLogo(ctx); !errors.Is(err, store.ErrNotFound) {
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
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "admin", Role: store.RoleAdmin})
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
		{"named colour", BrandingInput{Accent: "red"}, true},
		{"short hex", BrandingInput{Accent: "#fff"}, true},
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

	member := WithActor(context.Background(), Actor{Kind: "user", Name: "alice", Role: store.RoleMember})
	if _, err := c.SetBranding(member, BrandingInput{Name: "x"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("member: %v", err)
	}
	if err := c.ResetBranding(member); !errors.Is(err, ErrForbidden) {
		t.Errorf("member reset: %v", err)
	}
}
