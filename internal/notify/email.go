package notify

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/mail"
)

// EmailProviderKey is the email field naming the provider mail goes
// through; its options are the manager's.
const EmailProviderKey = "provider"

var emailKind = Kind{
	Name:  "email",
	Label: "Email",
	Fields: []ConfigField{{
		Key:         "to",
		Label:       "Recipients",
		Placeholder: "ops@example.com, oncall@example.com",
		Required:    true,
	}, {
		Key:      EmailProviderKey,
		Label:    "Send through",
		Required: true,
	}},
	New: newEmail,
}

func newEmail(cfg map[string]string, d Deps) (Sender, error) {
	to, err := mail.ParseList(cfg["to"])
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrConfig, strings.TrimPrefix(err.Error(), mail.ErrConfig.Error()+": "))
	}
	return &email{provider: cfg[EmailProviderKey], to: to, send: d.Mail, brand: d.brand()}, nil
}

type email struct {
	provider string
	to       []*netmail.Address
	send     func(ctx context.Context, provider string, to []*netmail.Address, b mail.Body, subject string) error
	brand    string
}

var emailColors = map[Level]string{
	Info:    "#5865f2",
	Success: "#22c55e",
	Warning: "#f59e0b",
	Error:   "#ef4444",
}

func emailBody(e Event, brand string) mail.Body {
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

func (m *email) Send(ctx context.Context, e Event) error {
	if m.send == nil {
		return errors.New("email: no email provider")
	}
	if err := m.send(ctx, m.provider, m.to, emailBody(e, m.brand), "["+m.brand+"] "+e.Title); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	return nil
}
