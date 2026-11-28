package notify

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain/mail"

	"github.com/kipit-labs/kipitiny/internal/domain/notify"
)

var emailKind = notify.Kind{
	Name:  "email",
	Label: "Email",
	Fields: []notify.ConfigField{{
		Key:         "to",
		Label:       "Recipients",
		Placeholder: "ops@example.com, oncall@example.com",
		Required:    true,
	}, {
		Key:      notify.EmailProviderKey,
		Label:    "Send through",
		Required: true,
	}},
	New: newEmail,
}

func newEmail(cfg map[string]string, d notify.Deps) (notify.Sender, error) {
	to, err := mail.ParseList(cfg["to"])
	if err != nil {
		return nil, fmt.Errorf("%w: %s", notify.ErrConfig, strings.TrimPrefix(err.Error(), mail.ErrConfig.Error()+": "))
	}
	return &email{provider: cfg[notify.EmailProviderKey], to: to, send: d.Mail, brand: brand(d)}, nil
}

type email struct {
	provider string
	to       []*netmail.Address
	send     func(ctx context.Context, provider string, to []*netmail.Address, b mail.Body, subject string) error
	brand    string
}

var emailColors = map[notify.Level]string{
	notify.Info:    "#5865f2",
	notify.Success: "#22c55e",
	notify.Warning: "#f59e0b",
	notify.Error:   "#ef4444",
}

func emailBody(e notify.Event, brand string) mail.Body {
	b := mail.Body{
		Title:  e.Title,
		Accent: emailColors[e.Level],
		Footer: brand + " · " + e.Type + " · " + e.Time.UTC().Format("2006-01-02 15:04 UTC"),
	}
	if e.Message != "" {
		b.Paragraphs = []string{truncate(e.Message, 8000)}
	}
	for _, f := range e.Fields {
		b.Fields = append(b.Fields, mail.Field{Name: f.Name, Value: f.Value})
	}
	if e.URL != "" {
		b.Button = &mail.Link{Label: "Open in " + brand, URL: e.URL}
	}
	return b
}

func (m *email) Send(ctx context.Context, e notify.Event) error {
	if m.send == nil {
		return errors.New("email: no email provider")
	}
	if err := m.send(ctx, m.provider, m.to, emailBody(e, m.brand), "["+m.brand+"] "+e.Title); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	return nil
}
