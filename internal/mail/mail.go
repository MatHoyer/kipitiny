// Package mail sends email through an SMTP server the admin configures
// (their provider's relay: SES, Postmark, Gmail…). The manager never runs a
// mail server itself: deliverability needs a reputation it doesn't have.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"
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

// Check connects and signs in without sending anything.
func Check(ctx context.Context, cfg Config) error {
	c, err := dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

// Send delivers m through the server; ctx bounds the whole exchange.
func Send(ctx context.Context, cfg Config, m Message) error {
	if len(m.To) == 0 {
		return fmt.Errorf("%w: no recipient", ErrConfig)
	}
	from, err := ParseAddress(cfg.From)
	if err != nil {
		return err
	}
	body, err := build(from, m, time.Now())
	if err != nil {
		return err
	}
	c, err := dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp: sender refused: %w", err)
	}
	for _, to := range m.To {
		if err := c.Rcpt(to.Address); err != nil {
			return fmt.Errorf("smtp: recipient %s refused: %w", to.Address, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: message refused: %w", err)
	}
	return c.Quit()
}

// dial connects, upgrades to TLS as configured and signs in. The
// connection is closed when ctx ends.
func dial(ctx context.Context, cfg Config) (*smtp.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tc := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if cfg.Security == TLS {
		conn, err = (&tls.Dialer{Config: tc}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("smtp: cannot connect to %s: %w", addr, err)
	}
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		stop()
		conn.Close()
		return nil, fmt.Errorf("smtp: %s: %w", addr, err)
	}
	if err := setup(c, cfg, tc); err != nil {
		stop()
		c.Close()
		return nil, err
	}
	return c, nil
}

func setup(c *smtp.Client, cfg Config, tc *tls.Config) error {
	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if cfg.Security == StartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp: %s doesn't offer STARTTLS; pick TLS or another port", cfg.Host)
		}
		if err := c.StartTLS(tc); err != nil {
			return fmt.Errorf("smtp: STARTTLS: %w", err)
		}
	}
	if cfg.Username == "" {
		return nil
	}
	ok, mechs := c.Extension("AUTH")
	if !ok {
		return fmt.Errorf("smtp: %s doesn't accept a sign-in; clear the username", cfg.Host)
	}
	var auth smtp.Auth
	switch fields := strings.Fields(strings.ToUpper(mechs)); {
	case slices.Contains(fields, "PLAIN"):
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	case slices.Contains(fields, "LOGIN"):
		auth = loginAuth{cfg.Username, cfg.Password}
	default:
		return fmt.Errorf("smtp: %s offers no supported sign-in method (%s)", cfg.Host, mechs)
	}
	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp: sign-in refused: %w", err)
	}
	return nil
}

// loginAuth is the LOGIN mechanism, the only one some providers (Office
// 365) offer besides OAuth. Only used over TLS: Validate refuses a username
// without it.
type loginAuth struct{ username, password string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSuffix(string(fromServer), ":")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", fromServer)
}

// build renders m as a MIME message with CRLF line endings.
func build(from *mail.Address, m Message, now time.Time) ([]byte, error) {
	var b bytes.Buffer
	to := make([]string, len(m.To))
	for i, a := range m.To {
		to[i] = a.String()
	}
	mw := multipart.NewWriter(&b)
	h := textproto.MIMEHeader{}
	h.Set("From", from.String())
	h.Set("To", strings.Join(to, ", "))
	h.Set("Subject", mime.QEncoding.Encode("utf-8", oneLine(m.Subject)))
	h.Set("Date", now.Format(time.RFC1123Z))
	h.Set("Message-ID", messageID(from.Address))
	h.Set("MIME-Version", "1.0")
	h.Set("Content-Type", `multipart/alternative; boundary="`+mw.Boundary()+`"`)
	h.Set("Auto-Submitted", "auto-generated")
	for _, k := range []string{"From", "To", "Subject", "Date", "Message-ID", "MIME-Version", "Auto-Submitted", "Content-Type"} {
		b.WriteString(k + ": " + h.Get(k) + "\r\n")
	}
	b.WriteString("\r\n")
	if err := part(mw, "text/plain", m.Text); err != nil {
		return nil, err
	}
	if m.HTML != "" {
		if err := part(mw, "text/html", m.HTML); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func part(mw *multipart.Writer, typ, body string) error {
	w, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {typ + "; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qw := quotedprintable.NewWriter(w)
	if _, err := io.WriteString(qw, strings.ReplaceAll(body, "\r\n", "\n")); err != nil {
		return err
	}
	return qw.Close()
}

func messageID(from string) string {
	domain := "kipitiny"
	if i := strings.LastIndexByte(from, '@'); i >= 0 {
		domain = from[i+1:]
	}
	r := make([]byte, 12)
	_, _ = rand.Read(r)
	return "<" + hex.EncodeToString(r) + "@" + domain + ">"
}

// oneLine keeps a header value on one line, whatever it was given.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
