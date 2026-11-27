package deploy

import (
	"context"
	"errors"
	"slices"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestHashBasicAuth(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	useSecretProvider(t, c, &fakeProvider{})("")
	svc := domain.Service{Middlewares: domain.Middlewares{BasicAuth: []domain.BasicAuthUser{
		{Name: "ann", Hash: "kept"},
		{Name: "bob", Ref: "{{ fake://bob }}"},
	}}}
	stored := slices.Clone(svc.Middlewares.BasicAuth)
	n, err := c.HashBasicAuth(ctx, &svc)
	if err != nil || n != 1 {
		t.Fatalf("hashBasicAuth = %d, %v", n, err)
	}
	if svc.Middlewares.BasicAuth[0].Hash != "kept" ||
		bcrypt.CompareHashAndPassword([]byte(svc.Middlewares.BasicAuth[1].Hash), []byte("value-of-bob")) != nil {
		t.Errorf("users = %+v", svc.Middlewares.BasicAuth)
	}
	if stored[1].Hash != "" {
		t.Error("hashing mutated the stored users")
	}

	if err := c.CheckBasicAuthSchemes(ctx, domain.Middlewares{BasicAuth: []domain.BasicAuthUser{{Name: "x", Ref: "{{ op://v/i/f }}"}}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("unknown scheme accepted: %v", err)
	}
	if m := masked(svc).Middlewares; m.BasicAuth[0].Hash != "" || svc.Middlewares.BasicAuth[0].Hash != "kept" {
		t.Error("masking must hide hashes without touching the service")
	}
}
