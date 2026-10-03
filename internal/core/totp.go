package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP per RFC 6238 with the parameters every authenticator app supports:
// SHA-1, 6 digits, 30-second steps.
const (
	totpPeriod = 30
	totpDigits = 6
	// totpSkew accepts the previous and next step, for clock drift.
	totpSkew = 1
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return totpEncoding.EncodeToString(b)
}

// totpURI is what the QR code holds for an authenticator app to scan.
func totpURI(secret, username string) string {
	q := url.Values{"secret": {secret}, "issuer": {"kipitiny"}}
	return "otpauth://totp/" + url.PathEscape("kipitiny:"+username) + "?" + q.Encode()
}

func totpCode(secret string, step int64) (string, error) {
	key, err := totpEncoding.DecodeString(secret)
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, n%1_000_000), nil
}

// totpMatch returns the time step code is valid for at t, if any.
func totpMatch(secret, code string, t time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0, false
	}
	now := t.Unix() / totpPeriod
	for step := now - totpSkew; step <= now+totpSkew; step++ {
		want, err := totpCode(secret, step)
		if err == nil && hmac.Equal([]byte(want), []byte(code)) {
			return step, true
		}
	}
	return 0, false
}
