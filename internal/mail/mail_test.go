package mail

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/MatHoyer/kipitiny/internal/mail/mailtest"
)

func TestValidate(t *testing.T) {
	ok := Config{Host: "smtp.example.com", Port: 587, Security: StartTLS, Username: "u", Password: "p", From: "kipitiny <ops@example.com>"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*Config){
		"no host":          func(c *Config) { c.Host = "" },
		"url host":         func(c *Config) { c.Host = "smtp://x" },
		"port":             func(c *Config) { c.Port = 0 },
		"security":         func(c *Config) { c.Security = "ssl" },
		"clear password":   func(c *Config) { c.Security = None },
		"password no user": func(c *Config) { c.Username = "" },
		"from":             func(c *Config) { c.From = "ops" },
	}
	for name, change := range tests {
		c := ok
		change(&c)
		if err := c.Validate(); !errors.Is(err, ErrConfig) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestParseList(t *testing.T) {
	as, err := ParseList("a@example.com, Bob <b@example.com>;\n c@example.com")
	if err != nil || len(as) != 3 || as[1].Name != "Bob" {
		t.Fatalf("got %v, %v", as, err)
	}
	for _, s := range []string{"", " , ", "a@example.com, nope"} {
		if _, err := ParseList(s); !errors.Is(err, ErrConfig) {
			t.Errorf("%q: got %v", s, err)
		}
	}
}

func TestBuild(t *testing.T) {
	from, _ := ParseAddress("kipitiny <ops@example.com>")
	to, _ := ParseList("a@example.com")
	m, err := Body{
		Title:      "shop/web deployment failed",
		Paragraphs: []string{"exit 1"},
		Fields:     []Field{{"Project", "shop"}, {"Empty", ""}},
		Button:     &Link{"Open", "https://kipitiny.example.com/services/1"},
	}.Message(to, "Déploiement\r\nBcc: x@evil.example")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := build(from, m, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Bcc") != "" {
		t.Fatal("subject injected a header")
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if subject != "Déploiement Bcc: x@evil.example" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.HasSuffix(msg.Header.Get("Message-ID"), "@example.com>") || msg.Header.Get("To") != "<a@example.com>" {
		t.Errorf("headers = %v", msg.Header)
	}
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p) // NextPart decodes quoted-printable
		parts = append(parts, strings.ReplaceAll(string(b), "\r\n", "\n"))
	}
	if len(parts) != 2 {
		t.Fatalf("%d parts", len(parts))
	}
	if !strings.Contains(parts[0], "Project: shop\n") || strings.Contains(parts[0], "Empty") ||
		!strings.Contains(parts[0], "Open: https://kipitiny.example.com/services/1") {
		t.Errorf("text = %q", parts[0])
	}
	if !strings.Contains(parts[1], `href="https://kipitiny.example.com/services/1"`) || !strings.Contains(parts[1], "<td") {
		t.Errorf("html = %q", parts[1])
	}
}

func TestLoginAuth(t *testing.T) {
	a := loginAuth{"u", "p"}
	if r, _ := a.Next([]byte("Username:"), true); string(r) != "u" {
		t.Errorf("username: %q", r)
	}
	if r, _ := a.Next([]byte("Password:"), true); string(r) != "p" {
		t.Errorf("password: %q", r)
	}
	if _, err := a.Next([]byte("what"), true); err == nil {
		t.Error("unknown challenge accepted")
	}
}

func TestSend(t *testing.T) {
	srv := mailtest.NewServer(t)
	cfg := Config{Host: "127.0.0.1", Port: srv.Port, Security: None, From: "ops@example.com"}
	to, _ := ParseList("a@example.com, b@example.com")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Send(ctx, cfg, Message{To: to, Subject: "hi", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	m := <-srv.Messages
	if m.From != "MAIL FROM:<ops@example.com> BODY=8BITMIME" || len(m.To) != 2 || m.To[1] != "RCPT TO:<b@example.com>" {
		t.Errorf("envelope = %q %q", m.From, m.To)
	}
	if !strings.Contains(m.Data, "hello") {
		t.Error("body not sent")
	}
}

func TestStartTLSRequired(t *testing.T) {
	srv := mailtest.NewServer(t)
	cfg := Config{Host: "127.0.0.1", Port: srv.Port, Security: StartTLS, From: "ops@example.com"}
	err := Check(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("got %v", err)
	}
}

func TestDialTimeout(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close() // accepts nothing: the greeting never comes
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	cfg := Config{Host: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, Security: None, From: "ops@example.com"}
	start := time.Now()
	if err := Check(ctx, cfg); err == nil {
		t.Fatal("no error")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("ctx didn't bound the dial")
	}
}
