package core

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestPushNotifications(t *testing.T) {
	ctx := context.Background()
	c := newTestCore(t, config.Config{})
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/gone" {
			w.WriteHeader(http.StatusGone)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	c.notifyHTTP = &http.Client{Transport: redirect{srv}}

	actor := func(role store.Role) context.Context {
		u, err := c.store.CreateUser(ctx, store.User{Username: string(role), PasswordHash: "x", Role: role})
		if err != nil {
			t.Fatal(err)
		}
		return WithActor(ctx, Actor{Kind: "user", Name: u.Username, UserID: u.ID, Role: role})
	}
	admin, member := actor(store.RoleAdmin), actor(store.RoleMember)
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	in := func(endpoint string) PushSubscribeInput {
		var in PushSubscribeInput
		in.Name, in.Endpoint = "Chrome", endpoint
		in.Keys.P256dh = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
		in.Keys.Auth = base64.URLEncoding.EncodeToString(make([]byte, 16)) // padded, as some browsers send
		return in
	}

	if _, err := c.SubscribePush(member, in("https://fcm.googleapis.com/fcm/send/m")); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member subscribes: %v", err)
	}
	if _, err := c.SubscribePush(admin, in("https://example.com/push")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown push service: %v", err)
	}
	bad := in("https://fcm.googleapis.com/fcm/send/a")
	bad.Keys.Auth = "short"
	if _, err := c.SubscribePush(admin, bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad keys: %v", err)
	}

	// No device yet: a test of the push channel says why nothing arrived.
	ch, err := c.CreateChannel(ctx, ChannelInput{Name: "phones", Kind: "push", Events: []string{EventDeployFailed}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TestChannel(ctx, ch.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("test without devices: %v", err)
	}

	for _, ep := range []string{"https://fcm.googleapis.com/ok", "https://fcm.googleapis.com/gone"} {
		if _, err := c.SubscribePush(admin, in(ep)); err != nil {
			t.Fatal(err)
		}
	}
	v, err := c.PushDevices(admin)
	if err != nil || len(v.Devices) != 2 || v.PublicKey == "" {
		t.Fatalf("devices: %+v %v", v, err)
	}
	if again, _ := c.PushDevices(admin); again.PublicKey != v.PublicKey {
		t.Fatal("the VAPID key changed")
	}

	if err := c.TestChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if hits["/ok"] != 1 || hits["/gone"] != 1 {
		t.Fatalf("hits %v", hits)
	}
	// the browser that unsubscribed is forgotten
	if v, _ := c.PushDevices(admin); len(v.Devices) != 1 || v.Devices[0].Endpoint != "https://fcm.googleapis.com/ok" {
		t.Fatalf("devices after a gone one: %+v", v.Devices)
	}

	if err := c.UnsubscribePush(member, v.Devices[0].ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("someone else's device: %v", err)
	}
	if err := c.UnsubscribePush(admin, v.Devices[0].ID); err != nil {
		t.Fatal(err)
	}
}
