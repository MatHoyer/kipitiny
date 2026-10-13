// Package mailtest runs a fake SMTP server for tests.
package mailtest

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// Received is one message the server accepted.
type Received struct {
	From string   // MAIL FROM line
	To   []string // RCPT TO lines
	// Data is the message as sent, dot-stuffing removed.
	Data string
}

// Server speaks just enough SMTP in clear text, without sign-in.
type Server struct {
	Port int
	// Messages receives each accepted message.
	Messages chan Received
}

// NewServer listens on 127.0.0.1 until the test ends. Extensions are
// advertised in the EHLO reply (STARTTLS…).
func NewServer(t testing.TB, extensions ...string) *Server {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := &Server{Port: l.Addr().(*net.TCPAddr).Port, Messages: make(chan Received, 16)}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, extensions)
		}
	}()
	return s
}

func (s *Server) serve(conn net.Conn, extensions []string) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	say("220 mailtest")
	var m Received
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch cmd, _, _ := strings.Cut(line, " "); strings.ToUpper(cmd) {
		case "EHLO", "HELO":
			say("250-mailtest")
			for _, e := range extensions {
				say("250-" + e)
			}
			say("250 8BITMIME")
		case "MAIL":
			m = Received{From: line}
			say("250 ok")
		case "RCPT":
			m.To = append(m.To, line)
			say("250 ok")
		case "DATA":
			say("354 go on")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				data.WriteString(strings.TrimPrefix(l, "."))
			}
			m.Data = data.String()
			s.Messages <- m
			say("250 queued")
		case "RSET", "NOOP":
			say("250 ok")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}
