package notify

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RFC 8291 appendix A.
func TestEncryptPushVector(t *testing.T) {
	d := func(s string) []byte {
		b, err := decode64(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	asPriv, err := ecdh.P256().NewPrivateKey(d("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encryptPush(asPriv, d("DGv6ra1nlYgDCS1FRnbzlw"),
		d("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"),
		d("BTBZMqHH6r4Tts7J_aSIgg"), []byte("When I grow up, I want to be a watermelon"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if b64.EncodeToString(got) != want {
		t.Fatalf("got  %s\nwant %s", b64.EncodeToString(got), want)
	}
}

func TestWebPush(t *testing.T) {
	v, err := NewVAPID()
	if err != nil {
		t.Fatal(err)
	}
	ua, err := ecdh.P256().GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusCreated
	var req *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, body = r, must(io.ReadAll(r.Body))
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	sub := Subscription{Endpoint: srv.URL + "/push/abc", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString([]byte("0123456789abcdef"))}
	m := PushMessage{Title: "Deployment failed", Body: "shop/web", Level: Error}
	if err := WebPush(context.Background(), srv.Client(), v, "mailto:ops@example.com", sub, m); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Content-Encoding") != "aes128gcm" || req.Header.Get("Urgency") != "high" || req.Header.Get("TTL") == "" {
		t.Errorf("headers: %v", req.Header)
	}
	// header: salt, record size, key id length, the sender's public key
	if len(body) < 16+4+1+65 || body[20] != 65 {
		t.Fatalf("body of %d bytes", len(body))
	}

	// The VAPID token: signed with the key the browser was given, for the
	// push service's origin.
	auth := req.Header.Get("Authorization")
	token, k, ok := strings.Cut(strings.TrimPrefix(auth, "vapid t="), ", k=")
	if !ok || k != v.PublicKey {
		t.Fatalf("authorization %q", auth)
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), must(decode64(v.PublicKey)))
	if err != nil {
		t.Fatal(err)
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return pub, nil }, jwt.WithValidMethods([]string{"ES256"})); err != nil {
		t.Fatal(err)
	}
	if claims["aud"] != srv.URL || claims["sub"] != "mailto:ops@example.com" {
		t.Errorf("claims %v", claims)
	}
	if exp, _ := claims.GetExpirationTime(); exp == nil || time.Until(exp.Time) > 24*time.Hour {
		t.Errorf("exp %v", exp)
	}

	status = http.StatusGone
	if err := WebPush(context.Background(), srv.Client(), v, "mailto:ops@example.com", sub, m); !errors.Is(err, ErrPushGone) {
		t.Fatalf("gone: %v", err)
	}
	status = http.StatusBadRequest
	if err := WebPush(context.Background(), srv.Client(), v, "mailto:ops@example.com", sub, m); err == nil || errors.Is(err, ErrPushGone) {
		t.Fatalf("bad request: %v", err)
	}
}

func TestPushKindJSON(t *testing.T) {
	// the UI maps over fields
	if b, _ := json.Marshal(pushKind); !strings.Contains(string(b), `"fields":[]`) {
		t.Errorf("kind %s", b)
	}
}

func TestPushSender(t *testing.T) {
	var got PushMessage
	s, err := New("push", nil, Deps{Push: func(_ context.Context, m PushMessage) error { got = m; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	e := Event{Title: "Backup failed", Message: "disk full", Level: Error, URL: "https://m/x", Fields: []Field{{"Project", "shop"}}}
	if err := s.Send(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(got); string(b) != `{"title":"Backup failed","body":"disk full\nProject: shop","url":"https://m/x","level":"error"}` {
		t.Errorf("message %s", b)
	}
	s, _ = New("push", nil, Deps{})
	if err := s.Send(context.Background(), e); err == nil {
		t.Error("sent without a transport")
	}
}

func TestValidPushEndpoint(t *testing.T) {
	for ep, want := range map[string]bool{
		"https://fcm.googleapis.com/fcm/send/abc":                true,
		"https://updates.push.services.mozilla.com/wpush/v2/abc": true,
		"https://web.push.apple.com/QGx":                         true,
		"https://wns2-par02p.notify.windows.com/w/?token=abc":    true,
		"http://fcm.googleapis.com/fcm/send/abc":                 false,
		"https://fcm.googleapis.com:8443/fcm/send/abc":           false,
		"https://evil.com/fcm.googleapis.com":                    false,
		"https://push.apple.com.evil.com/x":                      false,
		"https://evilpush.apple.com/x":                           false,
		"https://user@fcm.googleapis.com/x":                      false,
		"https://169.254.169.254/latest/meta-data":               false,
	} {
		if ValidPushEndpoint(ep) != want {
			t.Errorf("%s: want %v", ep, want)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
