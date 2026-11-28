// Package mail is the email the manager sends: the server's settings, the
// addresses, and the messages in kipitiny's layout. infrastructure/smtp
// delivers them.
package mail

import (
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
)

// Security is how the connection to the server is encrypted.
type Security string

const (
	// StartTLS upgrades a plain connection (port 587); refused when the
	// server doesn't offer it.
	StartTLS Security = "starttls"
	// TLS connects with TLS from the start (port 465).
	TLS Security = "tls"
	// None sends in clear text: only for a relay on the same host or
	// network, and never with a password.
	None Security = "none"
)

// MaxRecipients bounds one message's To list.
const MaxRecipients = 50

// ErrConfig wraps every configuration or address error.
var ErrConfig = errors.New("invalid email settings")

// Config is the server email goes through.
type Config struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Security Security `json:"security"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	// From is the sender, an address with an optional name
	// ("kipitiny <ops@example.com>").
	From string `json:"from"`
}

// Validate checks c is complete and consistent.
func (c Config) Validate() error {
	if c.Host == "" || strings.ContainsAny(c.Host, " /:") {
		return fmt.Errorf("%w: host must be a hostname or IP address", ErrConfig)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrConfig)
	}
	if !slices.Contains([]Security{StartTLS, TLS, None}, c.Security) {
		return fmt.Errorf("%w: security must be starttls, tls or none", ErrConfig)
	}
	if c.Security == None && c.Username != "" {
		return fmt.Errorf("%w: signing in needs TLS or STARTTLS, or the password would travel in clear text", ErrConfig)
	}
	if c.Password != "" && c.Username == "" {
		return fmt.Errorf("%w: a password needs a username", ErrConfig)
	}
	if _, err := ParseAddress(c.From); err != nil {
		return fmt.Errorf("%w: sender %q is not an email address", ErrConfig, c.From)
	}
	return nil
}

// ParseAddress parses one address, with an optional display name.
func ParseAddress(s string) (*mail.Address, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("%w: %q is not an email address", ErrConfig, s)
	}
	return a, nil
}

// ParseList parses comma or newline separated addresses.
func ParseList(s string) ([]*mail.Address, error) {
	var out []*mail.Address
	for p := range strings.FieldsFuncSeq(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		a, err := ParseAddress(p)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no recipient", ErrConfig)
	}
	if len(out) > MaxRecipients {
		return nil, fmt.Errorf("%w: at most %d recipients", ErrConfig, MaxRecipients)
	}
	return out, nil
}

// Message is one email, sent with a plain text and an HTML part.
type Message struct {
	To      []*mail.Address
	Subject string
	Text    string
	HTML    string
}
