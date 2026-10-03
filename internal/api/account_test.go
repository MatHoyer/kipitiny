package api

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// doJSON is do, decoding the response body into out.
func (c *client) doJSON(method, path, body string, out any) int {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.srv.URL+path, strings.NewReader(body))
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

// totp computes the code an authenticator app shows, independently of core.
func totp(t *testing.T, secret string, at time.Time) string {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:])&0x7fffffff)%1_000_000)
}

func TestTwoFactorFlow(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	expect(t, "setup",
		c.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)

	expect(t, "totp setup needs the password", c.do("POST", "/api/account/totp", `{"password":"wrong-password"}`), 400)
	var setup struct{ Secret, URI string }
	expect(t, "totp setup", c.doJSON("POST", "/api/account/totp", `{"password":"correct-horse"}`, &setup), 200)
	if !strings.HasPrefix(setup.URI, "otpauth://totp/") || setup.Secret == "" {
		t.Fatalf("setup = %+v", setup)
	}
	expect(t, "wrong confirmation code", c.do("POST", "/api/account/totp/enable", `{"code":"000000"}`), 400)
	now := time.Now()
	var codes struct{ RecoveryCodes []string }
	expect(t, "enable",
		c.doJSON("POST", "/api/account/totp/enable", fmt.Sprintf(`{"code":%q}`, totp(t, setup.Secret, now)), &codes), 200)
	if len(codes.RecoveryCodes) != 10 {
		t.Fatalf("got %d recovery codes", len(codes.RecoveryCodes))
	}
	var acc struct {
		TOTPEnabled   bool
		RecoveryCodes int
	}
	expect(t, "account", c.doJSON("GET", "/api/account", "", &acc), 200)
	if !acc.TOTPEnabled || acc.RecoveryCodes != 10 {
		t.Fatalf("account = %+v", acc)
	}

	// Signing in now takes a second step.
	other := newClient(t, srv)
	var challenge struct {
		MFARequired bool `json:"mfaRequired"`
		Ticket      string
	}
	expect(t, "password", other.doJSON("POST", "/api/auth/login", `{"username":"admin","password":"correct-horse"}`, &challenge), 200)
	if !challenge.MFARequired || challenge.Ticket == "" {
		t.Fatalf("login = %+v", challenge)
	}
	expect(t, "no session before the code", other.do("GET", "/api/projects", ""), 401)
	mfa := func(code string) int {
		return other.do("POST", "/api/auth/login/mfa", fmt.Sprintf(`{"ticket":%q,"code":%q}`, challenge.Ticket, code))
	}
	expect(t, "replayed confirmation code", mfa(totp(t, setup.Secret, now)), 401)
	expect(t, "recovery code", mfa(codes.RecoveryCodes[0]), 200)
	expect(t, "signed in", other.do("GET", "/api/projects", ""), 200)

	third := newClient(t, srv)
	expect(t, "password", third.doJSON("POST", "/api/auth/login", `{"username":"admin","password":"correct-horse"}`, &challenge), 200)
	expect(t, "used recovery code", third.do("POST", "/api/auth/login/mfa",
		fmt.Sprintf(`{"ticket":%q,"code":%q}`, challenge.Ticket, codes.RecoveryCodes[0])), 401)
	expect(t, "next totp code", third.do("POST", "/api/auth/login/mfa",
		fmt.Sprintf(`{"ticket":%q,"code":%q}`, challenge.Ticket, totp(t, setup.Secret, now.Add(30*time.Second)))), 200)
	expect(t, "ticket is single use", third.do("POST", "/api/auth/login/mfa",
		fmt.Sprintf(`{"ticket":%q,"code":%q}`, challenge.Ticket, codes.RecoveryCodes[1])), 401)

	expect(t, "disable", c.do("POST", "/api/account/totp/disable", `{"password":"correct-horse"}`), 204)
	var u struct{ Username string }
	expect(t, "password alone again", newClient(t, srv).doJSON("POST", "/api/auth/login", `{"username":"admin","password":"correct-horse"}`, &u), 200)
	if u.Username != "admin" {
		t.Fatalf("login = %+v", u)
	}
}

func TestChangePassword(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	expect(t, "setup",
		c.do("POST", "/api/auth/setup", `{"setupToken":"setup-secret","username":"admin","password":"correct-horse"}`), 201)
	other := newClient(t, srv)
	expect(t, "login", other.do("POST", "/api/auth/login", `{"username":"admin","password":"correct-horse"}`), 200)

	expect(t, "wrong current", c.do("PUT", "/api/account/password", `{"current":"nope-nope-nope","password":"battery-staple"}`), 400)
	expect(t, "change", c.do("PUT", "/api/account/password", `{"current":"correct-horse","password":"battery-staple"}`), 204)
	expect(t, "caller stays signed in", c.do("GET", "/api/projects", ""), 200)
	expect(t, "other sessions are signed out", other.do("GET", "/api/projects", ""), 401)
}

func TestAccountNeedsSession(t *testing.T) {
	srv := newServer(t)
	c := newClient(t, srv)
	expect(t, "account without auth", c.do("GET", "/api/account", ""), 401)
}
