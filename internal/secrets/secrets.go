// Package secrets connects password managers that service env can reference.
// Each provider owns a reference scheme (pass:// for Proton Pass); the manager
// resolves references at deploy time and never stores the values.
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
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

// EnvPrefix names the variables a CLI wrapper resolves for PrintEnv. CLIs
// such as `pass-cli run` and `op run` resolve references found in the
// environment of the command they start: the manager starts itself
// (`kipitiny secrets-env`) to read the results back.
const EnvPrefix = "KIPITINY_SECRET_"

// RefEnv lists refs as EnvPrefix variables, in order.
func RefEnv(refs []string) []string {
	env := make([]string, len(refs))
	for i, r := range refs {
		env[i] = EnvPrefix + strconv.Itoa(i) + "=" + r
	}
	return env
}

// PrintEnv writes the EnvPrefix variables of this process as a JSON array, in
// the order of RefEnv.
func PrintEnv(w io.Writer) error {
	var vals []string
	for i := 0; ; i++ {
		v, ok := os.LookupEnv(EnvPrefix + strconv.Itoa(i))
		if !ok {
			break
		}
		vals = append(vals, v)
	}
	return json.NewEncoder(w).Encode(vals)
}

// ParseEnv maps refs to the values PrintEnv wrote for them.
func ParseEnv(out []byte, refs []string) (map[string]string, error) {
	var vals []string
	if err := json.Unmarshal(out, &vals); err != nil {
		return nil, err
	}
	if len(vals) != len(refs) {
		return nil, errors.New("secrets: resolved values don't match the references")
	}
	m := make(map[string]string, len(refs))
	for i, r := range refs {
		m[r] = vals[i]
	}
	return m, nil
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
