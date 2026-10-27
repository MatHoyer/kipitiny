package core

import (
	"context"
	"errors"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestSetPreferences(t *testing.T) {
	c := newTestCore(t, config.Config{})
	u, err := c.store.CreateUser(context.Background(), store.User{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithActor(context.Background(), Actor{Kind: "user", Name: "alice", UserID: u.ID, Role: store.RoleMember})

	if _, err := c.SetPreferences(ctx, store.Preferences{MapView: "4d"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad view: %v", err)
	}
	if _, err := c.SetPreferences(ctx, store.Preferences{Theme: "blue"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad theme: %v", err)
	}
	if _, err := c.SetPreferences(ctx, store.Preferences{MapView: "3d", Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.store.GetUser(ctx, u.ID); got.Preferences != (store.Preferences{MapView: "3d", Theme: "dark"}) {
		t.Fatalf("saved: %+v", got.Preferences)
	}
	if _, err := c.SetPreferences(context.Background(), store.Preferences{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("no actor: %v", err)
	}
}
