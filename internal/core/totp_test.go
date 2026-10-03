package core

import (
	"encoding/base32"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 secret, truncated to 6 digits.
func TestTOTPCodeRFCVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want := range map[int64]string{
		59:          "287082",
		1111111109:  "081804",
		1111111111:  "050471",
		1234567890:  "005924",
		2000000000:  "279037",
		20000000000: "353130",
	} {
		got, err := totpCode(secret, unix/totpPeriod)
		if err != nil || got != want {
			t.Errorf("t=%d: got %q, %v; want %q", unix, got, err, want)
		}
	}
}

func TestTOTPMatch(t *testing.T) {
	secret := newTOTPSecret()
	now := time.Unix(1_700_000_000, 0)
	step := now.Unix() / totpPeriod
	for _, d := range []int64{-1, 0, 1} {
		code, _ := totpCode(secret, step+d)
		if got, ok := totpMatch(secret, code, now); !ok || got != step+d {
			t.Errorf("drift %d: got %d, %v", d, got, ok)
		}
	}
	old, _ := totpCode(secret, step-2)
	if _, ok := totpMatch(secret, old, now); ok {
		t.Error("a code two steps old was accepted")
	}
	if _, ok := totpMatch(secret, "12345", now); ok {
		t.Error("a 5-digit code was accepted")
	}
}
