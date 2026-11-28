// Package notify delivers events to external channels (Discord, email, push
// to browsers; generic webhooks later), as monitoring.Channels. It knows
// nothing about the manager: monitoring builds a notify.Event and hands it to
// the Sender of each channel, with the Deps it delivers through.
package notify

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/notify"
)

var kinds = []notify.Kind{discordKind, emailKind, pushKind}

// Channels are the delivery methods and web push, as monitoring.Channels.
type Channels struct{}

func (Channels) Kinds() []notify.Kind { return Kinds() }

func (Channels) Lookup(name string) (notify.Kind, bool) { return Lookup(name) }

func (Channels) New(kind string, cfg map[string]string, d notify.Deps) (notify.Sender, error) {
	return New(kind, cfg, d)
}

func (Channels) WebPush(ctx context.Context, hc *http.Client, v notify.VAPID, subject string, s notify.Subscription, m notify.PushMessage) error {
	return WebPush(ctx, hc, v, subject, s, m)
}

// Kinds lists the available delivery methods.
func Kinds() []notify.Kind { return slices.Clone(kinds) }

// Lookup returns the kind with that name.
func Lookup(name string) (notify.Kind, bool) {
	i := slices.IndexFunc(kinds, func(k notify.Kind) bool { return k.Name == name })
	if i < 0 {
		return notify.Kind{}, false
	}
	return kinds[i], true
}

// New returns the sender of a channel, checking required fields first.
func New(kind string, cfg map[string]string, d notify.Deps) (notify.Sender, error) {
	k, ok := Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("%w: unknown kind %q", notify.ErrConfig, kind)
	}
	for _, f := range k.Fields {
		if f.Required && cfg[f.Key] == "" {
			return nil, fmt.Errorf("%w: %s is required", notify.ErrConfig, f.Label)
		}
	}
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: 15 * time.Second}
	}
	return k.New(cfg, d)
}

// brand is the manager's name senders sign with; kipitiny when empty.
func brand(d notify.Deps) string {
	if d.Brand != "" {
		return d.Brand
	}
	return "kipitiny"
}

// truncate cuts s to at most n runes, marking the cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
