package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/kipit-labs/kipitiny/internal/domain/notify"
)

// Web Push (RFC 8030): the payload is encrypted for the browser (RFC 8291),
// so the push service relaying it (Google, Mozilla, Apple, Microsoft) sees
// only its size and timing, and signed with the manager's VAPID key
// (RFC 8292).

var pushKind = notify.Kind{
	Name:   "push",
	Label:  "Push",
	Fields: []notify.ConfigField{}, // nothing to set: it goes to the admins' browsers
	New:    newPush,
}

func newPush(_ map[string]string, d notify.Deps) (notify.Sender, error) {
	return &push{send: d.Push}, nil
}

type push struct {
	send func(ctx context.Context, m notify.PushMessage) error
}

func (p *push) Send(ctx context.Context, e notify.Event) error {
	if p.send == nil {
		return errors.New("push notifications are not available")
	}
	body := e.Message
	for _, f := range e.Fields {
		body += "\n" + f.Name + ": " + f.Value
	}
	// a push message holds about 4 KB once encrypted
	return p.send(ctx, notify.PushMessage{Title: truncate(e.Title, 200), Body: truncate(strings.TrimSpace(body), 1000), URL: e.URL, Level: e.Level})
}

var b64 = base64.RawURLEncoding

// decode64 accepts base64url with or without padding, as browsers vary.
func decode64(s string) ([]byte, error) {
	return b64.DecodeString(strings.TrimRight(s, "="))
}

// WebPush delivers m to one browser. subject is a mailto: or https: URL
// push services may contact the sender at.
func WebPush(ctx context.Context, hc *http.Client, v notify.VAPID, subject string, s notify.Subscription, m notify.PushMessage) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	uaPub, err := decode64(s.P256dh)
	if err != nil {
		return fmt.Errorf("browser key: %w", err)
	}
	auth, err := decode64(s.Auth)
	if err != nil {
		return fmt.Errorf("browser secret: %w", err)
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	body, err := encryptPush(asPriv, salt, uaPub, auth, payload)
	if err != nil {
		return err
	}
	vapid, err := vapidHeader(v, subject, s.Endpoint, time.Now())
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", vapid)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(24*60*60))
	urgency := "normal"
	if m.Level == notify.Error || m.Level == notify.Warning {
		urgency = "high"
	}
	req.Header.Set("Urgency", urgency)
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return notify.ErrPushGone
	case resp.StatusCode >= 300:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("push service answered %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// encryptPush is RFC 8291's aes128gcm content encoding, in a single record.
func encryptPush(asPriv *ecdh.PrivateKey, salt, uaPub, auth, plaintext []byte) ([]byte, error) {
	ua, err := ecdh.P256().NewPublicKey(uaPub)
	if err != nil {
		return nil, fmt.Errorf("browser key: %w", err)
	}
	secret, err := asPriv.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := asPriv.PublicKey().Bytes()
	prkKey, err := hkdf.Extract(sha256.New, secret, auth)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(uaPub)+string(asPub), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	const recordSize = 4096
	header := make([]byte, 0, 16+4+1+len(asPub))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPub)))
	header = append(header, asPub...)
	padded := append(append([]byte{}, plaintext...), 2) // 2: last record, no padding
	return gcm.Seal(header, nonce, padded, nil), nil
}

// vapidHeader signs a VAPID token for the endpoint's push service.
func vapidHeader(v notify.VAPID, subject, endpoint string, now time.Time) (string, error) {
	raw, err := decode64(v.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("VAPID key: %w", err)
	}
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return "", fmt.Errorf("VAPID key: %w", err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(12 * time.Hour).Unix(),
		"sub": subject,
	}).SignedString(key)
	if err != nil {
		return "", err
	}
	return "vapid t=" + token + ", k=" + v.PublicKey, nil
}
