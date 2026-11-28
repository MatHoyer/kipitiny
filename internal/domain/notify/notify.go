// Package notify is what the manager tells people about: events, the
// delivery methods (kinds) channels are configured with, and web push
// subscriptions. The senders are in infrastructure/notify
// (monitoring.Channels).
package notify

import (
	"context"
	"errors"
	"net/http"
	netmail "net/mail"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/mail"
)

// Level sets how an event is presented (colour, icon).
type Level string

const (
	Info    Level = "info"
	Success Level = "success"
	Warning Level = "warning"
	Error   Level = "error"
)

// Event is one thing worth telling someone about.
type Event struct {
	// Type identifies the event (deploy.failed); channels subscribe to types.
	Type    string `json:"type"`
	Level   Level  `json:"level"`
	Title   string `json:"title"`
	Message string `json:"message,omitempty"`
	// Fields are short facts shown as a table (Project: shop).
	Fields []Field `json:"fields,omitempty"`
	// URL points at the subject in the UI, when the manager has a public URL.
	URL  string    `json:"url,omitempty"`
	Time time.Time `json:"time"`
}

type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Sender delivers events to one configured destination.
type Sender interface {
	Send(ctx context.Context, e Event) error
}

// Deps are what senders deliver through.
type Deps struct {
	// HTTP calls webhooks; nil uses a client with a 15 s timeout.
	HTTP *http.Client
	// Mail sends an email through one of the manager's email providers; nil
	// when nothing is sent (validating a configuration).
	Mail func(ctx context.Context, provider string, to []*netmail.Address, b mail.Body, subject string) error
	// Push sends a push notification to the admins' browsers; nil when
	// nothing is sent.
	Push func(ctx context.Context, m PushMessage) error
	// Brand is the manager's name senders sign with; kipitiny when empty.
	Brand string
}

// Kind is a delivery method. Its Fields describe the configuration, so the
// UI renders the form of a new kind without changes.
type Kind struct {
	Name   string        `json:"name"`
	Label  string        `json:"label"`
	Fields []ConfigField `json:"fields"`
	// New validates a configuration and returns its sender.
	New func(cfg map[string]string, d Deps) (Sender, error) `json:"-"`
}

type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
	// Secret values are write-only: masked whenever a channel is read.
	Secret bool `json:"secret"`
	// Options makes the field a choice; the manager fills them in when
	// listing kinds (the email providers).
	Options []Option `json:"options,omitempty"`
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// ErrConfig wraps every configuration error.
var ErrConfig = errors.New("invalid notification config")

// EmailProviderKey is the email field naming the provider mail goes
// through; its options are the manager's.
const EmailProviderKey = "provider"
