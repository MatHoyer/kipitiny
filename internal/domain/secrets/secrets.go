// Package secrets connects password managers that service env can reference.
// Each provider owns a reference scheme (e.g. op:// for 1Password); the manager
// resolves references at deploy time and never stores the values.
package secrets

import (
	"context"
	"strings"
)

// Provider is a password manager the manager can log into.
type Provider interface {
	// Info describes the provider for Settings.
	Info() Info
	// Available reports whether it can run here (e.g. its CLI is installed).
	Available() bool
	// Connect logs in with token, replacing any previous session.
	Connect(ctx context.Context, token string) error
	Disconnect(ctx context.Context) error
	// Resolve returns the value of each reference (scheme://...).
	Resolve(ctx context.Context, refs []string) (map[string]string, error)
}

type Info struct {
	// ID names the provider in settings and the API.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Scheme is the reference scheme it resolves, without "://".
	Scheme string `json:"scheme"`
	// TokenLabel names the credential Connect takes.
	TokenLabel string `json:"tokenLabel"`
	// Example is a sample reference, for help text.
	Example string `json:"example"`
	// Help says how to get a token, as plain text.
	Help string `json:"help"`
}

// SchemeOf returns the scheme of a reference, "" if it has none.
func SchemeOf(ref string) string {
	s, _, ok := strings.Cut(ref, "://")
	if !ok {
		return ""
	}
	return s
}

// Browser is a Provider that can list what it can reference, for pickers.
// Listings carry names and references, never values.
type Browser interface {
	Vaults(ctx context.Context) ([]string, error)
	Items(ctx context.Context, vault string) ([]Item, error)
}

type Item struct {
	Title  string  `json:"title"`
	Fields []Field `json:"fields"`
}

type Field struct {
	Name string `json:"name"`
	// Ref is the full reference, ready to use in env.
	Ref string `json:"ref"`
}
