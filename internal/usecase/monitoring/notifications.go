package monitoring

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/usecase/identity"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/notify"
)

// EventType describes an event a channel can subscribe to.
type EventType struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	// Default events are preselected for a new channel.
	Default bool `json:"default"`
}

var eventTypes = []EventType{
	{domain.EventDeployFailed, "Deployment failed", true},
	{domain.EventDeploySucceeded, "Deployment succeeded", false},
	{domain.EventGitSyncFailed, "Git sync failed", true},
	{domain.EventGitSyncSucceeded, "Git sync applied changes", false},
	{domain.EventServiceRestarted, "Stopped service restarted", true},
	{domain.EventServiceUnhealthy, "Service unhealthy", true},
	{domain.EventServiceHealthy, "Service healthy again", true},
	{domain.EventServiceOOMKilled, "Service ran out of memory", true},
	{domain.EventMemoryHigh, "Service memory high", true},
	{domain.EventMemoryOK, "Service memory back to normal", true},
	{domain.EventCPUHigh, "Service CPU high", true},
	{domain.EventCPUOK, "Service CPU back to normal", true},
	{domain.EventErrorNew, "New error in an app", true},
	{domain.EventErrorRegression, "Resolved error back", true},
	{domain.EventErrorSpike, "Errors up after a deploy", true},
	{domain.EventUptimeDown, "Uptime check failing", true},
	{domain.EventUptimeUp, "Uptime check recovered", true},
	{domain.EventJobFailed, "Scheduled job failed", true},
	{domain.EventJobSucceeded, "Scheduled job succeeded", false},
	{domain.EventBackupFailed, "Backup failed", true},
	{domain.EventBackupSucceeded, "Backup succeeded", false},
	{domain.EventVerifyFailed, "Restore test failed", true},
	{domain.EventVerifySucceeded, "Restore test passed", false},
	{domain.EventRestoreFailed, "Restore failed", true},
	{domain.EventRestoreSucceeded, "Restore succeeded", true},
	{domain.EventAnonymizeFailed, "Anonymized preview data failed", true},
	{domain.EventCleanupFailed, "Cleanup had errors", true},
	{domain.EventUpdateAvailable, "New kipitiny version", true},
	{domain.EventServerUpdates, "Server has security updates", true},
	{domain.EventServerReboot, "Server needs a reboot", true},
	{domain.EventDiskLow, "Server disk almost full", true},
	{domain.EventDiskOK, "Server disk space recovered", true},
}

const (
	notifyTimeout = 15 * time.Second
	// NotifyCooldown silences repeats of a keyed event (a crash-looping
	// service) so a channel isn't flooded.
	NotifyCooldown = 15 * time.Minute
)

type NotificationsView struct {
	Channels []domain.NotificationChannel `json:"channels"`
	Kinds    []notify.Kind                `json:"kinds"`
	Events   []EventType                  `json:"events"`
	// Thresholds of the resource alerts.
	Thresholds domain.ResourceAlerts `json:"thresholds"`
}

func (c *Monitoring) Notifications(ctx context.Context) (NotificationsView, error) {
	chs, err := c.store.ListNotificationChannels(ctx)
	if err != nil {
		return NotificationsView{}, err
	}
	for i := range chs {
		chs[i] = c.maskedChannel(chs[i])
		if identity.IsMember(ctx) { // where alerts go is the admins'
			chs[i].Config = map[string]string{}
		}
	}
	kinds := c.channels.Kinds()
	if !identity.IsMember(ctx) {
		if kinds, err = c.withEmailProviders(ctx, kinds); err != nil {
			return NotificationsView{}, err
		}
	}
	return NotificationsView{Channels: chs, Kinds: kinds, Events: eventTypes, Thresholds: c.mgr.ResourceAlerts(ctx)}, nil
}

// withEmailProviders offers the email providers in the email kind's
// provider field, the invites one first.
func (c *Monitoring) withEmailProviders(ctx context.Context, kinds []notify.Kind) ([]notify.Kind, error) {
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

func (c *Monitoring) CreateChannel(ctx context.Context, in ChannelInput) (domain.NotificationChannel, error) {
	ch := domain.NotificationChannel{Kind: in.Kind, Config: map[string]string{}}
	if err := c.applyChannelInput(&ch, in); err != nil {
		return domain.NotificationChannel{}, err
	}
	if err := c.checkChannelProvider(ctx, ch); err != nil {
		return domain.NotificationChannel{}, err
	}
	ch, err := c.store.CreateNotificationChannel(ctx, ch)
	return c.maskedChannel(ch), err
}

// UpdateChannel replaces a channel's settings; masked secrets are kept.
func (c *Monitoring) UpdateChannel(ctx context.Context, id string, in ChannelInput) (domain.NotificationChannel, error) {
	ch, err := c.store.GetNotificationChannel(ctx, id)
	if err != nil {
		return domain.NotificationChannel{}, err
	}
	if err := c.applyChannelInput(&ch, in); err != nil {
		return domain.NotificationChannel{}, err
	}
	if err := c.checkChannelProvider(ctx, ch); err != nil {
		return domain.NotificationChannel{}, err
	}
	ch, err = c.store.UpdateNotificationChannel(ctx, ch)
	return c.maskedChannel(ch), err
}

func (c *Monitoring) DeleteChannel(ctx context.Context, id string) error {
	return c.store.DeleteNotificationChannel(ctx, id)
}

// TestChannel sends a test message right away and reports delivery errors.
func (c *Monitoring) TestChannel(ctx context.Context, id string) error {
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
		return fmt.Errorf("%w: %s", domain.ErrInvalid, err.Error())
	}
	return nil
}

// applyChannelInput validates in and copies it onto ch, keeping the stored
// value of secrets sent back masked.
func (c *Monitoring) applyChannelInput(ch *domain.NotificationChannel, in ChannelInput) error {
	kind, ok := c.channels.Lookup(ch.Kind)
	if !ok {
		return fmt.Errorf("%w: unknown notification kind %q", domain.ErrInvalid, ch.Kind)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 64 {
		return fmt.Errorf("%w: name is required (max 64 characters)", domain.ErrInvalid)
	}
	cfg := map[string]string{}
	for _, f := range kind.Fields {
		v := strings.TrimSpace(in.Config[f.Key])
		if f.Secret && v == identity.SecretMask {
			v = ch.Config[f.Key]
		}
		if v != "" {
			cfg[f.Key] = v
		}
	}
	if _, err := c.channels.New(ch.Kind, cfg, notify.Deps{}); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalid, strings.TrimPrefix(err.Error(), notify.ErrConfig.Error()+": "))
	}
	events := []string{}
	for _, e := range in.Events {
		if !slices.ContainsFunc(eventTypes, func(t EventType) bool { return t.Type == e }) {
			return fmt.Errorf("%w: unknown event %q", domain.ErrInvalid, e)
		}
		if !slices.Contains(events, e) {
			events = append(events, e)
		}
	}
	ch.Name, ch.Config, ch.Events, ch.Enabled = name, cfg, events, in.Enabled
	return nil
}

// checkChannelProvider makes sure an email channel's provider exists.
func (c *Monitoring) checkChannelProvider(ctx context.Context, ch domain.NotificationChannel) error {
	if ch.Kind != "email" {
		return nil
	}
	if _, err := c.store.GetEmailProvider(ctx, ch.Config[notify.EmailProviderKey]); errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: unknown email provider; add one in Settings › Email", domain.ErrInvalid)
	} else if err != nil {
		return err
	}
	return nil
}

func (c *Monitoring) maskedChannel(ch domain.NotificationChannel) domain.NotificationChannel {
	kind, _ := c.channels.Lookup(ch.Kind)
	cfg := make(map[string]string, len(ch.Config))
	for k, v := range ch.Config {
		cfg[k] = v
	}
	for _, f := range kind.Fields {
		if f.Secret && cfg[f.Key] != "" {
			cfg[f.Key] = identity.SecretMask
		}
	}
	ch.Config = cfg
	return ch
}

// Notify sends e to every enabled channel subscribed to its type, in the
// background. A non-empty key silences repeats of it for NotifyCooldown.
func (c *Monitoring) Notify(e notify.Event, path, key string) {
	if key != "" {
		now := time.Now()
		if last, ok := c.notified.Load(key); ok && now.Sub(last.(time.Time)) < NotifyCooldown {
			return
		}
		c.notified.Store(key, now)
	}
	e = c.event(e, path)
	err := c.mgr.GoBackground(func() {
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

func (c *Monitoring) send(ctx context.Context, ch domain.NotificationChannel, e notify.Event) error {
	s, err := c.channels.New(ch.Kind, ch.Config, notify.Deps{HTTP: c.NotifyHTTP, Mail: c.SendMail, Push: c.pushToAdmins, Brand: c.mgr.BrandName(ctx)})
	if err != nil {
		return err
	}
	return s.Send(ctx, e)
}

// event stamps e and links it to path in the UI when the manager has a
// public address.
func (c *Monitoring) event(e notify.Event, path string) notify.Event {
	e.Time = time.Now()
	if path != "" && c.cfg.Domain != "" && c.cfg.Traefik.Enabled {
		e.URL = "https://" + c.cfg.Domain + path
	}
	return e
}
