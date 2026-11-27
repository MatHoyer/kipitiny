package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/config"
	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestSetPreferences(t *testing.T) {
	c := newTestIdentity(t, config.Config{})
	u, err := c.store.CreateUser(context.Background(), domain.User{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "alice", UserID: u.ID, Role: domain.RoleMember})

	if _, err := c.SetPreferences(ctx, domain.Preferences{MapView: "4d"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad view: %v", err)
	}
	if _, err := c.SetPreferences(ctx, domain.Preferences{MapView: "3d"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.store.GetUser(ctx, u.ID); got.Preferences.MapView != "3d" {
		t.Fatalf("saved: %+v", got.Preferences)
	}
	if _, err := c.SetPreferences(context.Background(), domain.Preferences{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("no actor: %v", err)
	}
}
