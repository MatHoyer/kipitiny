package notify

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/mail"
)

var emailKind = Kind{
	Name:  "email",
	Label: "Email",
	Fields: []ConfigField{{
		Key:         "to",
		Label:       "Recipients",
		Placeholder: "ops@example.com, oncall@example.com",
		Required:    true,
	}},
	New: newEmail,
}

func newEmail(cfg map[string]string, d Deps) (Sender, error) {
	to, err := mail.ParseList(cfg["to"])
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrConfig, strings.TrimPrefix(err.Error(), mail.ErrConfig.Error()+": "))
	}
	return &email{to: to, send: d.Mail}, nil
}

type email struct {
	to   []*netmail.Address
	send func(ctx context.Context, to []*netmail.Address, b mail.Body, subject string) error
}

var emailColors = map[Level]string{
	Info:    "#5865f2",
	Success: "#22c55e",
	Warning: "#f59e0b",
	Error:   "#ef4444",
}

func emailBody(e Event) mail.Body {
	b := mail.Body{
		Title:  e.Title,
		Accent: emailColors[e.Level],
		Footer: "kipitiny · " + e.Type + " · " + e.Time.UTC().Format("2006-01-02 15:04 UTC"),
	}
	if e.Message != "" {
		b.Paragraphs = []string{truncate(e.Message, 8000)}
	}
	for _, f := range e.Fields {
		b.Fields = append(b.Fields, mail.Field{Name: f.Name, Value: f.Value})
	}
	if e.URL != "" {
		b.Button = &mail.Link{Label: "Open in kipitiny", URL: e.URL}
	}
	return b
}

func (m *email) Send(ctx context.Context, e Event) error {
	if m.send == nil {
		return errors.New("email: no SMTP server")
	}
	if err := m.send(ctx, m.to, emailBody(e), "[kipitiny] "+e.Title); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	return nil
}
