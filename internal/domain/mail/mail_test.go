package mail

import (
	"errors"
	"testing"
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
