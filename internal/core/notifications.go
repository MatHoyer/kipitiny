package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Event types sent to notification channels.
const (
	EventDeploySucceeded  = "deploy.succeeded"
	EventDeployFailed     = "deploy.failed"
	EventBackupSucceeded  = "backup.succeeded"
	EventBackupFailed     = "backup.failed"
	EventRestoreSucceeded = "restore.succeeded"
	EventRestoreFailed    = "restore.failed"
	EventVerifySucceeded  = "verify.succeeded"
	EventVerifyFailed     = "verify.failed"
	EventServiceRestarted = "service.restarted"
	EventCleanupFailed    = "cleanup.failed"
	EventUpdateAvailable  = "update.available"
	EventServiceUnhealthy = "service.unhealthy"
	EventServiceHealthy   = "service.healthy"
	EventUptimeDown       = "uptime.down"
	EventUptimeUp         = "uptime.up"
	EventServerUpdates    = "server.updates_pending"
	EventServerReboot     = "server.reboot_required"
	EventJobFailed        = "job.failed"
	EventJobSucceeded     = "job.succeeded"
	EventAnonymizeFailed  = "anonymize.failed"
)

// EventType describes an event a channel can subscribe to.
type EventType struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	// Default events are preselected for a new channel.
	Default bool `json:"default"`
}

var eventTypes = []EventType{
	{EventDeployFailed, "Deployment failed", true},
	{EventDeploySucceeded, "Deployment succeeded", false},
	{EventGitSyncFailed, "Git sync failed", true},
	{EventGitSyncSucceeded, "Git sync applied changes", false},
	{EventServiceRestarted, "Stopped service restarted", true},
	{EventServiceUnhealthy, "Service unhealthy", true},
	{EventServiceHealthy, "Service healthy again", true},
	{EventUptimeDown, "Uptime check failing", true},
	{EventUptimeUp, "Uptime check recovered", true},
	{EventJobFailed, "Scheduled job failed", true},
	{EventJobSucceeded, "Scheduled job succeeded", false},
	{EventBackupFailed, "Backup failed", true},
	{EventBackupSucceeded, "Backup succeeded", false},
	{EventVerifyFailed, "Restore test failed", true},
	{EventVerifySucceeded, "Restore test passed", false},
	{EventRestoreFailed, "Restore failed", true},
	{EventRestoreSucceeded, "Restore succeeded", true},
	{EventAnonymizeFailed, "Anonymized preview data failed", true},
	{EventCleanupFailed, "Cleanup had errors", true},
	{EventUpdateAvailable, "New kipitiny version", true},
	{EventServerUpdates, "Server has security updates", true},
	{EventServerReboot, "Server needs a reboot", true},
}

const (
	notifyTimeout = 15 * time.Second
	// notifyCooldown silences repeats of a keyed event (a crash-looping
	// service) so a channel isn't flooded.
	notifyCooldown = 15 * time.Minute
)

type NotificationsView struct {
	Channels []store.NotificationChannel `json:"channels"`
	Kinds    []notify.Kind               `json:"kinds"`
	Events   []EventType                 `json:"events"`
}

func (c *Core) Notifications(ctx context.Context) (NotificationsView, error) {
	chs, err := c.store.ListNotificationChannels(ctx)
	if err != nil {
		return NotificationsView{}, err
	}
	for i := range chs {
		chs[i] = maskedChannel(chs[i])
		if isMember(ctx) { // where alerts go is the admins'
			chs[i].Config = map[string]string{}
		}
	}
	kinds := notify.Kinds()
	if !isMember(ctx) {
		if kinds, err = c.withEmailProviders(ctx, kinds); err != nil {
			return NotificationsView{}, err
		}
	}
	return NotificationsView{Channels: chs, Kinds: kinds, Events: eventTypes}, nil
}

// withEmailProviders offers the email providers in the email kind's
// provider field, the invites one first.
func (c *Core) withEmailProviders(ctx context.Context, kinds []notify.Kind) ([]notify.Kind, error) {
	ps, err := c.store.ListEmailProviders(ctx)
	if err != nil {
		return nil, err
	}
	opts := []notify.Option{}
	inv, _ := invitesProvider(ctx, c.store, ps)
	for _, p := range ps {
		o := notify.Option{Value: p.ID, Label: p.Name}
		if p.ID == inv.ID {
			opts = slices.Insert(opts, 0, o)
		} else {
			opts = append(opts, o)
		}
	}
	for i, k := range kinds {
		if k.Name != "email" {
			continue
		}
		fields := slices.Clone(k.Fields)
		for j := range fields {
			if fields[j].Key == notify.EmailProviderKey {
				fields[j].Options = opts
			}
		}
		kinds[i].Fields = fields
	}
	return kinds, nil
}

type ChannelInput struct {
	Name    string            `json:"name"`
	Kind    string            `json:"kind"` // only on creation
	Config  map[string]string `json:"config"`
	Events  []string          `json:"events"`
	Enabled bool              `json:"enabled"`
}

func (c *Core) CreateChannel(ctx context.Context, in ChannelInput) (store.NotificationChannel, error) {
	ch := store.NotificationChannel{Kind: in.Kind, Config: map[string]string{}}
	if err := applyChannelInput(&ch, in); err != nil {
		return store.NotificationChannel{}, err
	}
	if err := c.checkChannelProvider(ctx, ch); err != nil {
		return store.NotificationChannel{}, err
	}
	ch, err := c.store.CreateNotificationChannel(ctx, ch)
	return maskedChannel(ch), err
}

// UpdateChannel replaces a channel's settings; masked secrets are kept.
func (c *Core) UpdateChannel(ctx context.Context, id string, in ChannelInput) (store.NotificationChannel, error) {
	ch, err := c.store.GetNotificationChannel(ctx, id)
	if err != nil {
		return store.NotificationChannel{}, err
	}
	if err := applyChannelInput(&ch, in); err != nil {
		return store.NotificationChannel{}, err
	}
	if err := c.checkChannelProvider(ctx, ch); err != nil {
		return store.NotificationChannel{}, err
	}
	ch, err = c.store.UpdateNotificationChannel(ctx, ch)
	return maskedChannel(ch), err
}

func (c *Core) DeleteChannel(ctx context.Context, id string) error {
	return c.store.DeleteNotificationChannel(ctx, id)
}

// TestChannel sends a test message right away and reports delivery errors.
func (c *Core) TestChannel(ctx context.Context, id string) error {
	ch, err := c.store.GetNotificationChannel(ctx, id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	err = c.send(ctx, ch, c.event(notify.Event{
		Type:    "test",
		Level:   notify.Info,
		Title:   "Test notification",
		Message: fmt.Sprintf("Notifications from kipitiny reach the channel %s.", ch.Name),
	}, ""))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

// applyChannelInput validates in and copies it onto ch, keeping the stored
// value of secrets sent back masked.
func applyChannelInput(ch *store.NotificationChannel, in ChannelInput) error {
	kind, ok := notify.Lookup(ch.Kind)
	if !ok {
		return fmt.Errorf("%w: unknown notification kind %q", ErrInvalid, ch.Kind)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		return fmt.Errorf("%w: name is required (max 64 characters)", ErrInvalid)
	}
	cfg := map[string]string{}
	for _, f := range kind.Fields {
		v := strings.TrimSpace(in.Config[f.Key])
		if f.Secret && v == SecretMask {
			v = ch.Config[f.Key]
		}
		if v != "" {
			cfg[f.Key] = v
		}
	}
	if _, err := notify.New(ch.Kind, cfg, notify.Deps{}); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), notify.ErrConfig.Error()+": "))
	}
	events := []string{}
	for _, e := range in.Events {
		if !slices.ContainsFunc(eventTypes, func(t EventType) bool { return t.Type == e }) {
			return fmt.Errorf("%w: unknown event %q", ErrInvalid, e)
		}
		if !slices.Contains(events, e) {
			events = append(events, e)
		}
	}
	ch.Name, ch.Config, ch.Events, ch.Enabled = name, cfg, events, in.Enabled
	return nil
}

// checkChannelProvider makes sure an email channel's provider exists.
func (c *Core) checkChannelProvider(ctx context.Context, ch store.NotificationChannel) error {
	if ch.Kind != "email" {
		return nil
	}
	if _, err := c.store.GetEmailProvider(ctx, ch.Config[notify.EmailProviderKey]); errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: unknown email provider; add one in Settings › Email", ErrInvalid)
	} else if err != nil {
		return err
	}
	return nil
}

func maskedChannel(ch store.NotificationChannel) store.NotificationChannel {
	kind, _ := notify.Lookup(ch.Kind)
	cfg := make(map[string]string, len(ch.Config))
	for k, v := range ch.Config {
		cfg[k] = v
	}
	for _, f := range kind.Fields {
		if f.Secret && cfg[f.Key] != "" {
			cfg[f.Key] = SecretMask
		}
	}
	ch.Config = cfg
	return ch
}

// notify sends e to every enabled channel subscribed to its type, in the
// background. A non-empty key silences repeats of it for notifyCooldown.
func (c *Core) notify(e notify.Event, path, key string) {
	if key != "" {
		now := time.Now()
		if last, ok := c.notified.Load(key); ok && now.Sub(last.(time.Time)) < notifyCooldown {
			return
		}
		c.notified.Store(key, now)
	}
	e = c.event(e, path)
	err := c.goBackground(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.bg), notifyTimeout)
		defer cancel()
		chs, err := c.store.ListNotificationChannels(ctx)
		if err != nil {
			c.log.Warn("cannot list notification channels", "err", err)
			return
		}
		for _, ch := range chs {
			if !ch.Enabled || !slices.Contains(ch.Events, e.Type) {
				continue
			}
			if err := c.send(ctx, ch, e); err != nil {
				c.log.Warn("notification not delivered", "channel", ch.Name, "event", e.Type, "err", err)
			}
		}
	})
	if err != nil {
		c.log.Debug("notification dropped", "event", e.Type, "err", err)
	}
}

func (c *Core) send(ctx context.Context, ch store.NotificationChannel, e notify.Event) error {
	s, err := notify.New(ch.Kind, ch.Config, notify.Deps{HTTP: c.notifyHTTP, Mail: c.sendMail, Push: c.pushToAdmins, Brand: c.BrandName(ctx)})
	if err != nil {
		return err
	}
	return s.Send(ctx, e)
}

// event stamps e and links it to path in the UI when the manager has a
// public address.
func (c *Core) event(e notify.Event, path string) notify.Event {
	e.Time = time.Now()
	if path != "" && c.cfg.Domain != "" && c.cfg.Traefik.Enabled {
		e.URL = "https://" + c.cfg.Domain + path
	}
	return e
}
