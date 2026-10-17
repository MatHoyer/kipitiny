package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Push notifications reach the browsers admins turned them on in (Account),
// through the push channels of Settings › Notifications. Members get none:
// events are about every project.

// pushSetting holds the VAPID key pair, generated on first use.
const pushSetting = "webpush"

// pushSubject is who push services contact about the manager's pushes when it
// has no public address.
const pushSubject = "https://github.com/MatHoyer/kipitiny"

func (c *Core) vapid(ctx context.Context) (notify.VAPID, error) {
	c.pushMu.Lock()
	defer c.pushMu.Unlock()
	var v notify.VAPID
	if s, err := c.store.GetSetting(ctx, pushSetting); err == nil && s != "" {
		if err := json.Unmarshal([]byte(s), &v); err == nil && v.PrivateKey != "" {
			return v, nil
		}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	v, err := notify.NewVAPID()
	if err != nil {
		return v, err
	}
	b, _ := json.Marshal(v)
	return v, c.store.SetSetting(ctx, pushSetting, string(b))
}

type PushDevices struct {
	// PublicKey is the applicationServerKey browsers subscribe with.
	PublicKey string                   `json:"publicKey"`
	Devices   []store.PushSubscription `json:"devices"`
}

// PushDevices lists the signed-in admin's browsers that receive pushes.
func (c *Core) PushDevices(ctx context.Context) (PushDevices, error) {
	if err := RequireAdmin(ctx); err != nil {
		return PushDevices{}, err
	}
	u, err := c.currentUser(ctx)
	if err != nil {
		return PushDevices{}, err
	}
	v, err := c.vapid(ctx)
	if err != nil {
		return PushDevices{}, err
	}
	ds, err := c.store.ListPushSubscriptions(ctx, u.ID)
	return PushDevices{PublicKey: v.PublicKey, Devices: ds}, err
}

// PushSubscribeInput is the browser's PushSubscription.toJSON(), and a name.
type PushSubscribeInput struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// SubscribePush adds the browser the signed-in admin uses.
func (c *Core) SubscribePush(ctx context.Context, in PushSubscribeInput) (store.PushSubscription, error) {
	if err := RequireAdmin(ctx); err != nil {
		return store.PushSubscription{}, err
	}
	u, err := c.currentUser(ctx)
	if err != nil {
		return store.PushSubscription{}, err
	}
	if !notify.ValidPushEndpoint(in.Endpoint) {
		return store.PushSubscription{}, fmt.Errorf("%w: not a known push service", ErrInvalid)
	}
	if !pushKeyLen(in.Keys.P256dh, 65) || !pushKeyLen(in.Keys.Auth, 16) {
		return store.PushSubscription{}, fmt.Errorf("%w: invalid browser keys", ErrInvalid)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Browser"
	}
	if r := []rune(name); len(r) > 64 {
		name = string(r[:64])
	}
	return c.store.SavePushSubscription(ctx, store.PushSubscription{
		UserID: u.ID, Name: name, Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth,
	})
}

// pushKeyLen checks a browser key's size; browsers send base64url, with or
// without padding.
func pushKeyLen(s string, n int) bool {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	return err == nil && len(b) == n
}

// UnsubscribePush removes one of the signed-in user's browsers.
func (c *Core) UnsubscribePush(ctx context.Context, id string) error {
	u, err := c.currentUser(ctx)
	if err != nil {
		return err
	}
	return c.store.DeletePushSubscription(ctx, u.ID, id)
}

// pushToAdmins sends m to every admin's browsers, forgetting those that
// unsubscribed. It fails only when no browser got it.
func (c *Core) pushToAdmins(ctx context.Context, m notify.PushMessage) error {
	subs, err := c.store.ListAdminPushSubscriptions(ctx)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return errors.New("no admin turned push notifications on (Account › Push notifications)")
	}
	v, err := c.vapid(ctx)
	if err != nil {
		return err
	}
	subject := pushSubject
	if c.cfg.Domain != "" && c.cfg.Traefik.Enabled {
		subject = "https://" + c.cfg.Domain
	}
	m.Icon = c.Branding(ctx).Logo
	hc := c.notifyHTTP
	if hc == nil {
		hc = &http.Client{Timeout: notifyTimeout}
	}
	var errs []error
	sent := 0
	for _, s := range subs {
		err := notify.WebPush(ctx, hc, v, subject, notify.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}, m)
		switch {
		case err == nil:
			sent++
		case errors.Is(err, notify.ErrPushGone):
			_ = c.store.DeletePushSubscription(context.WithoutCancel(ctx), s.UserID, s.ID)
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
		default:
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
		}
	}
	if sent == 0 {
		return errors.Join(errs...)
	}
	if len(errs) > 0 {
		c.log.Warn("push notification not delivered to every browser", "err", errors.Join(errs...))
	}
	return nil
}
