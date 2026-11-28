package notify

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
)

// PushMessage is what a push notification shows; core delivers it to every
// admin's browsers through Deps.Push.
type PushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	URL   string `json:"url,omitempty"`
	Level Level  `json:"level"`
	// Icon is the manager's logo; the browser shows kipitiny's without one.
	Icon string `json:"icon,omitempty"`
}

// Push service hosts browsers subscribe with; anything else is refused so a
// subscription can't make the manager call arbitrary URLs.
var pushHosts = []string{
	"fcm.googleapis.com",                // Chrome, Brave, Opera, Samsung
	"jmt17.google.com",                  // Chromium builds
	"updates.push.services.mozilla.com", // Firefox
	".push.apple.com",                   // Safari
	".notify.windows.com",               // Edge
}

// ValidPushEndpoint tells whether a browser's endpoint is a known push
// service.
func ValidPushEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range pushHosts {
		if host == h || strings.HasPrefix(h, ".") && strings.HasSuffix(host, h) {
			return true
		}
	}
	return false
}

// VAPID is the manager's key pair push services identify it by, base64url:
// the public key is the browser's applicationServerKey.
type VAPID struct {
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}

func NewVAPID() (VAPID, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return VAPID{}, err
	}
	priv, err := k.Bytes()
	if err != nil {
		return VAPID{}, err
	}
	pub, err := k.PublicKey.Bytes()
	if err != nil {
		return VAPID{}, err
	}
	return VAPID{PublicKey: b64.EncodeToString(pub), PrivateKey: b64.EncodeToString(priv)}, nil
}

var b64 = base64.RawURLEncoding

// Subscription is where one browser receives pushes.
type Subscription struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// ErrPushGone means the browser unsubscribed: forget the subscription.
var ErrPushGone = errors.New("push subscription expired")
