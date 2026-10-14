package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MatHoyer/kipitiny/internal/mail"
	"github.com/MatHoyer/kipitiny/internal/notify"
	"github.com/MatHoyer/kipitiny/internal/store"
)

// Email goes out through SMTP servers the admin configures, its email
// providers: invites use one (the email_invites setting, else the oldest),
// each email notification channel names its own.

const (
	// emailInvitesSetting holds the ID of the provider invites go through.
	emailInvitesSetting = "email_invites"
	// smtpSetting is the single SMTP server before providers; Bootstrap
	// turns it into one.
	smtpSetting = "smtp"
	mailTimeout = 20 * time.Second
)

// ErrNoEmail is returned when something should be emailed but no email
// provider is set up.
var ErrNoEmail = fmt.Errorf("%w: email isn't set up; add an email provider in Settings › Email", ErrInvalid)

// EmailView lists the providers, passwords masked, and the one invites use.
type EmailView struct {
	Providers []store.EmailProvider `json:"providers"`
	// Invites is the ID of the provider invites go through; empty without
	// providers.
	Invites string `json:"invites"`
}

// EmailProviderInput configures a provider. A masked password keeps the
// stored one.
type EmailProviderInput struct {
	Name string `json:"name"`
	mail.Config
}

func maskedEmailProvider(p store.EmailProvider) store.EmailProvider {
	if p.Password != "" {
		p.Password = SecretMask
	}
	return p
}

func mailConfig(p store.EmailProvider) mail.Config {
	return mail.Config{Host: p.Host, Port: p.Port, Security: mail.Security(p.Security), Username: p.Username, Password: p.Password, From: p.From}
}

func (c *Core) Email(ctx context.Context) (EmailView, error) {
	ps, err := c.store.ListEmailProviders(ctx)
	if err != nil {
		return EmailView{}, err
	}
	v := EmailView{Providers: make([]store.EmailProvider, len(ps))}
	for i, p := range ps {
		v.Providers[i] = maskedEmailProvider(p)
	}
	if p, ok := invitesProvider(ctx, c.store, ps); ok {
		v.Invites = p.ID
	}
	return v, nil
}

// invitesProvider picks the provider invites go through among ps: the one
// the setting names, else the oldest.
func invitesProvider(ctx context.Context, s store.Store, ps []store.EmailProvider) (store.EmailProvider, bool) {
	id, _ := s.GetSetting(ctx, emailInvitesSetting)
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	if len(ps) == 0 {
		return store.EmailProvider{}, false
	}
	return ps[0], true
}

// CreateEmailProvider checks the server accepts the configuration
// (connection and sign-in, nothing sent) and saves it.
func (c *Core) CreateEmailProvider(ctx context.Context, in EmailProviderInput) (store.EmailProvider, error) {
	p, err := c.checkEmailProvider(ctx, store.EmailProvider{}, in)
	if err != nil {
		return store.EmailProvider{}, err
	}
	p, err = c.store.CreateEmailProvider(ctx, p)
	return maskedEmailProvider(p), emailConflict(err)
}

func (c *Core) UpdateEmailProvider(ctx context.Context, id string, in EmailProviderInput) (store.EmailProvider, error) {
	old, err := c.store.GetEmailProvider(ctx, id)
	if err != nil {
		return store.EmailProvider{}, err
	}
	p, err := c.checkEmailProvider(ctx, old, in)
	if err != nil {
		return store.EmailProvider{}, err
	}
	p, err = c.store.UpdateEmailProvider(ctx, p)
	return maskedEmailProvider(p), emailConflict(err)
}

// DeleteEmailProvider removes a provider no channel sends through. Invites
// move to the oldest one left.
func (c *Core) DeleteEmailProvider(ctx context.Context, id string) error {
	if _, err := c.store.GetEmailProvider(ctx, id); err != nil {
		return err
	}
	chs, err := c.store.ListNotificationChannels(ctx)
	if err != nil {
		return err
	}
	var used []string
	for _, ch := range chs {
		if ch.Kind == "email" && ch.Config[notify.EmailProviderKey] == id {
			used = append(used, ch.Name)
		}
	}
	if len(used) > 0 {
		return fmt.Errorf("%w: notification channels send through it (%s); pick another provider for them first", ErrInvalid, strings.Join(used, ", "))
	}
	return c.store.DeleteEmailProvider(ctx, id)
}

// SetEmailInvites picks the provider invites go through.
func (c *Core) SetEmailInvites(ctx context.Context, id string) (EmailView, error) {
	if _, err := c.store.GetEmailProvider(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return EmailView{}, fmt.Errorf("%w: unknown email provider", ErrInvalid)
		}
		return EmailView{}, err
	}
	if err := c.store.SetSetting(ctx, emailInvitesSetting, id); err != nil {
		return EmailView{}, err
	}
	return c.Email(ctx)
}

// checkEmailProvider validates in over p and signs in to the server.
func (c *Core) checkEmailProvider(ctx context.Context, p store.EmailProvider, in EmailProviderInput) (store.EmailProvider, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return p, fmt.Errorf("%w: name is required (max 64 characters)", ErrInvalid)
	}
	cfg := in.Config
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Username = strings.TrimSpace(cfg.Username)
	cfg.From = strings.TrimSpace(cfg.From)
	if cfg.Password == SecretMask {
		cfg.Password = p.Password
	}
	if err := cfg.Validate(); err != nil {
		return p, mailErr(err)
	}
	cctx, cancel := context.WithTimeout(ctx, mailTimeout)
	defer cancel()
	if err := mail.Check(cctx, cfg); err != nil {
		return p, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	p.Name, p.Host, p.Port, p.Security = name, cfg.Host, cfg.Port, string(cfg.Security)
	p.Username, p.Password, p.From = cfg.Username, cfg.Password, cfg.From
	return p, nil
}

func emailConflict(err error) error {
	if err != nil && strings.Contains(err.Error(), store.ErrConflict.Error()) {
		return fmt.Errorf("%w: an email provider with this name exists", ErrInvalid)
	}
	return err
}

// TestEmailProvider sends a test message through a provider.
func (c *Core) TestEmailProvider(ctx context.Context, id, to string) error {
	addr, err := mail.ParseAddress(to)
	if err != nil {
		return mailErr(err)
	}
	p, err := c.store.GetEmailProvider(ctx, id)
	if err != nil {
		return err
	}
	err = c.sendMail(ctx, p.ID, []*netmail.Address{addr}, mail.Body{
		Title:      "Test email from " + c.BrandName(ctx),
		Accent:     "#22c55e",
		Paragraphs: []string{"Email from " + c.BrandName(ctx) + " reaches this address through " + p.Name + "."},
		Footer:     c.mailFooter(ctx),
	}, "")
	if err != nil && !errors.Is(err, ErrInvalid) {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return err
}

// emailProvider returns the provider with that ID, or the invites one for
// an empty ID.
func (c *Core) emailProvider(ctx context.Context, id string) (store.EmailProvider, error) {
	if id != "" {
		p, err := c.store.GetEmailProvider(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return p, fmt.Errorf("%w: the email provider was removed; pick another in Settings › Notifications", ErrInvalid)
		}
		return p, err
	}
	ps, err := c.store.ListEmailProviders(ctx)
	if err != nil {
		return store.EmailProvider{}, err
	}
	p, ok := invitesProvider(ctx, c.store, ps)
	if !ok {
		return p, ErrNoEmail
	}
	return p, nil
}

// sendMail renders b and sends it right away through a provider (the
// invites one for an empty ID), within mailTimeout.
func (c *Core) sendMail(ctx context.Context, provider string, to []*netmail.Address, b mail.Body, subject string) error {
	p, err := c.emailProvider(ctx, provider)
	if err != nil {
		return err
	}
	if b.Accent == "" {
		b.Accent = c.brandingSettings(ctx).Accent
	}
	m, err := b.Message(to, subject)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, mailTimeout)
	defer cancel()
	return mail.Send(ctx, mailConfig(p), m)
}

// mailFooter says where an email comes from.
func (c *Core) mailFooter(ctx context.Context) string {
	if c.cfg.Domain != "" {
		return "Sent by " + c.BrandName(ctx) + " on " + c.cfg.Domain
	}
	return "Sent by " + c.BrandName(ctx)
}

func mailErr(err error) error {
	return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), mail.ErrConfig.Error()+": "))
}

// migrateSMTPSetting turns the single SMTP server of earlier versions into
// a provider used by invites and the email channels. Safe to rerun: an
// interrupted run finds its provider by name.
func (c *Core) migrateSMTPSetting(ctx context.Context) error {
	v, err := c.store.GetSetting(ctx, smtpSetting)
	if errors.Is(err, store.ErrNotFound) || (err == nil && v == "") {
		return nil
	} else if err != nil {
		return err
	}
	var cfg mail.Config
	if err := json.Unmarshal([]byte(v), &cfg); err != nil {
		c.log.Warn("dropping unreadable SMTP settings", "err", err)
		return c.store.SetSetting(ctx, smtpSetting, "")
	}
	name := cfg.Host
	if cfg.Host == "smtp.resend.com" {
		name = "Resend"
	}
	p := store.EmailProvider{Name: name, Host: cfg.Host, Port: cfg.Port, Security: string(cfg.Security), Username: cfg.Username, Password: cfg.Password, From: cfg.From}
	if p, err = c.store.CreateEmailProvider(ctx, p); errors.Is(err, store.ErrConflict) {
		ps, lerr := c.store.ListEmailProviders(ctx)
		if lerr != nil {
			return lerr
		}
		for _, q := range ps {
			if q.Name == name {
				p, err = q, nil
			}
		}
	}
	if err != nil {
		return fmt.Errorf("email provider from SMTP settings: %w", err)
	}
	chs, err := c.store.ListNotificationChannels(ctx)
	if err != nil {
		return err
	}
	for _, ch := range chs {
		if ch.Kind != "email" || ch.Config[notify.EmailProviderKey] != "" {
			continue
		}
		if ch.Config == nil {
			ch.Config = map[string]string{}
		}
		ch.Config[notify.EmailProviderKey] = p.ID
		if _, err := c.store.UpdateNotificationChannel(ctx, ch); err != nil {
			return err
		}
	}
	if err := c.store.SetSetting(ctx, emailInvitesSetting, p.ID); err != nil {
		return err
	}
	c.log.Info("SMTP settings moved to an email provider", "name", p.Name)
	return c.store.SetSetting(ctx, smtpSetting, "")
}
