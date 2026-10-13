package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/mail"
)

// Email goes out through an SMTP server the admin configures: notification
// channels of kind email and invites use it.

const (
	smtpSetting = "smtp"
	mailTimeout = 20 * time.Second
)

// ErrNoEmail is returned when something should be emailed but no SMTP
// server is configured.
var ErrNoEmail = fmt.Errorf("%w: email isn't set up; configure an SMTP server in Settings › Email", ErrInvalid)

// EmailView is the SMTP configuration shown in Settings, password masked.
type EmailView struct {
	Configured bool        `json:"configured"`
	Settings   mail.Config `json:"settings"`
}

// smtpConfig returns the stored configuration; ok is false when there is none.
func (c *Core) smtpConfig(ctx context.Context) (cfg mail.Config, ok bool) {
	v, err := c.store.GetSetting(ctx, smtpSetting)
	if err != nil || v == "" || json.Unmarshal([]byte(v), &cfg) != nil {
		return mail.Config{Port: 587, Security: mail.StartTLS}, false
	}
	return cfg, true
}

func (c *Core) Email(ctx context.Context) EmailView {
	cfg, ok := c.smtpConfig(ctx)
	if cfg.Password != "" {
		cfg.Password = SecretMask
	}
	return EmailView{Configured: ok, Settings: cfg}
}

// SetEmail checks the server accepts the configuration (connection and
// sign-in, nothing sent) and saves it. A masked password keeps the stored one.
func (c *Core) SetEmail(ctx context.Context, in mail.Config) (EmailView, error) {
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.From = strings.TrimSpace(in.From)
	if in.Password == SecretMask {
		old, _ := c.smtpConfig(ctx)
		in.Password = old.Password
	}
	if err := in.Validate(); err != nil {
		return EmailView{}, mailErr(err)
	}
	cctx, cancel := context.WithTimeout(ctx, mailTimeout)
	defer cancel()
	if err := mail.Check(cctx, in); err != nil {
		return EmailView{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	b, err := json.Marshal(in)
	if err != nil {
		return EmailView{}, err
	}
	if err := c.store.SetSetting(ctx, smtpSetting, string(b)); err != nil {
		return EmailView{}, err
	}
	return c.Email(ctx), nil
}

// DeleteEmail forgets the SMTP server; email channels and invites stop
// sending.
func (c *Core) DeleteEmail(ctx context.Context) error {
	return c.store.SetSetting(ctx, smtpSetting, "")
}

// TestEmail sends a test message to the given address.
func (c *Core) TestEmail(ctx context.Context, to string) error {
	addr, err := mail.ParseAddress(to)
	if err != nil {
		return mailErr(err)
	}
	err = c.sendMail(ctx, []*netmail.Address{addr}, mail.Body{
		Title:      "Test email from kipitiny",
		Accent:     "#22c55e",
		Paragraphs: []string{"Email from kipitiny reaches this address: notification channels and invites can use it."},
		Footer:     c.mailFooter(),
	}, "")
	if err != nil && !errors.Is(err, ErrInvalid) {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return err
}

// sendMail renders b and sends it right away, within mailTimeout.
func (c *Core) sendMail(ctx context.Context, to []*netmail.Address, b mail.Body, subject string) error {
	cfg, ok := c.smtpConfig(ctx)
	if !ok {
		return ErrNoEmail
	}
	m, err := b.Message(to, subject)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, mailTimeout)
	defer cancel()
	return mail.Send(ctx, cfg, m)
}

// mailFooter says where an email comes from.
func (c *Core) mailFooter() string {
	if c.cfg.Domain != "" {
		return "Sent by kipitiny on " + c.cfg.Domain
	}
	return "Sent by kipitiny"
}

func mailErr(err error) error {
	return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimPrefix(err.Error(), mail.ErrConfig.Error()+": "))
}
