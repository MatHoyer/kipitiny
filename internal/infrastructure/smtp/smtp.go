// Package smtp sends email through an SMTP server the admin configures
// (their provider's relay: SES, Postmark, Gmail…), as monitoring.SMTP. The
// manager never runs a mail server itself: deliverability needs a
// reputation it doesn't have.
package smtp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/mail"
)

// Check connects and signs in without sending anything.
func Check(ctx context.Context, cfg mail.Config) error {
	c, err := dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

// Send delivers m through the server; ctx bounds the whole exchange.
func Send(ctx context.Context, cfg mail.Config, m mail.Message) error {
	if len(m.To) == 0 {
		return fmt.Errorf("%w: no recipient", mail.ErrConfig)
	}
	from, err := mail.ParseAddress(cfg.From)
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
func dial(ctx context.Context, cfg mail.Config) (*smtp.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tc := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if cfg.Security == mail.TLS {
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

func setup(c *smtp.Client, cfg mail.Config, tc *tls.Config) error {
	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	if cfg.Security == mail.StartTLS {
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
func build(from *netmail.Address, m mail.Message, now time.Time) ([]byte, error) {
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

// Client is the admin's mail server, as monitoring.SMTP.
type Client struct{}

func (Client) Check(ctx context.Context, cfg mail.Config) error { return Check(ctx, cfg) }

func (Client) Send(ctx context.Context, cfg mail.Config, m mail.Message) error {
	return Send(ctx, cfg, m)
}
