// Package notify delivers events to external channels (Discord now; email,
// in-app or generic webhooks later). It knows nothing about the manager:
// core builds an Event and hands it to the Sender of each channel.
package notify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
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

// Kind is a delivery method. Its Fields describe the configuration, so the
// UI renders the form of a new kind without changes.
type Kind struct {
	Name   string        `json:"name"`
	Label  string        `json:"label"`
	Fields []ConfigField `json:"fields"`
	// New validates a configuration and returns its sender.
	New func(cfg map[string]string, hc *http.Client) (Sender, error) `json:"-"`
}

type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
	// Secret values are write-only: masked whenever a channel is read.
	Secret bool `json:"secret"`
}

// ErrConfig wraps every configuration error.
var ErrConfig = errors.New("invalid notification config")

var kinds = []Kind{discordKind}

// Kinds lists the available delivery methods.
func Kinds() []Kind { return slices.Clone(kinds) }

// Lookup returns the kind with that name.
func Lookup(name string) (Kind, bool) {
	i := slices.IndexFunc(kinds, func(k Kind) bool { return k.Name == name })
	if i < 0 {
		return Kind{}, false
	}
	return kinds[i], true
}

// New returns the sender of a channel, checking required fields first.
func New(kind string, cfg map[string]string, hc *http.Client) (Sender, error) {
	k, ok := Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("%w: unknown kind %q", ErrConfig, kind)
	}
	for _, f := range k.Fields {
		if f.Required && cfg[f.Key] == "" {
			return nil, fmt.Errorf("%w: %s is required", ErrConfig, f.Label)
		}
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return k.New(cfg, hc)
}

// truncate cuts s to at most n runes, marking the cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
