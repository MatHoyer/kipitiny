package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// Branding is how the manager presents itself: its name, accent colour and
// logo, in the UI, on the sign-in page and in emails. Kept in two settings
// so reading the name doesn't load the logo.
const (
	brandingSetting     = "branding"
	brandingLogoSetting = "branding_logo"

	// DefaultBrandName is the name when none is set.
	DefaultBrandName = "kipitiny"
	maxBrandName     = 40
	// MaxBrandLogo is the largest logo accepted, decoded.
	MaxBrandLogo = 128 << 10
)

var accentRe = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// Branding is what the UI applies; empty fields keep kipitiny's own.
type Branding struct {
	Name   string `json:"name,omitempty"`
	Accent string `json:"accent,omitempty"`
	// Logo is the logo's URL, versioned so browsers cache it until it
	// changes.
	Logo string `json:"logo,omitempty"`
}

// BrandingInput replaces the branding. Logo is a data URL (or bare base64)
// to set one, "" to remove it, or nil to keep the current one.
type BrandingInput struct {
	Name   string  `json:"name"`
	Accent string  `json:"accent"`
	Logo   *string `json:"logo,omitempty"`
}

type brandingSettings struct {
	Name     string `json:"name,omitempty"`
	Accent   string `json:"accent,omitempty"`
	LogoType string `json:"logoType,omitempty"`
	// LogoHash versions the logo URL.
	LogoHash string `json:"logoHash,omitempty"`
}

func (c *Core) brandingSettings(ctx context.Context) brandingSettings {
	var s brandingSettings
	if v, err := c.store.GetSetting(ctx, brandingSetting); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &s)
	}
	return s
}

// Branding returns the manager's branding, shown before sign-in too.
func (c *Core) Branding(ctx context.Context) Branding {
	s := c.brandingSettings(ctx)
	b := Branding{Name: s.Name, Accent: s.Accent}
	if s.LogoHash != "" {
		b.Logo = "/api/branding/logo?v=" + s.LogoHash
	}
	return b
}

// BrandName is the manager's name, kipitiny unless renamed.
func (c *Core) BrandName(ctx context.Context) string {
	if n := c.brandingSettings(ctx).Name; n != "" {
		return n
	}
	return DefaultBrandName
}

// BrandingLogo returns the logo and its content type, or store.ErrNotFound.
func (c *Core) BrandingLogo(ctx context.Context) ([]byte, string, error) {
	s := c.brandingSettings(ctx)
	if s.LogoHash == "" {
		return nil, "", store.ErrNotFound
	}
	v, err := c.store.GetSetting(ctx, brandingLogoSetting)
	if err != nil {
		return nil, "", err
	}
	data, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(data) == 0 {
		return nil, "", store.ErrNotFound
	}
	return data, s.LogoType, nil
}

// SetBranding replaces the manager's branding.
func (c *Core) SetBranding(ctx context.Context, in BrandingInput) (Branding, error) {
	if err := RequireAdmin(ctx); err != nil {
		return Branding{}, err
	}
	s := c.brandingSettings(ctx)
	s.Name = strings.TrimSpace(in.Name)
	if utf8.RuneCountInString(s.Name) > maxBrandName || strings.ContainsAny(s.Name, "\r\n") {
		return Branding{}, fmt.Errorf("%w: the name must be one line of at most %d characters", ErrInvalid, maxBrandName)
	}
	s.Accent = strings.ToLower(strings.TrimSpace(in.Accent))
	if s.Accent != "" && !accentRe.MatchString(s.Accent) {
		return Branding{}, fmt.Errorf("%w: the accent colour must look like #4f46e5", ErrInvalid)
	}
	if in.Logo != nil {
		logo, typ, err := parseLogo(*in.Logo)
		if err != nil {
			return Branding{}, err
		}
		s.LogoType, s.LogoHash = typ, ""
		if logo != nil {
			sum := sha256.Sum256(logo)
			s.LogoHash = hex.EncodeToString(sum[:6])
		}
		if err := c.store.SetSetting(ctx, brandingLogoSetting, base64.StdEncoding.EncodeToString(logo)); err != nil {
			return Branding{}, err
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return Branding{}, err
	}
	if err := c.store.SetSetting(ctx, brandingSetting, string(b)); err != nil {
		return Branding{}, err
	}
	return c.Branding(ctx), nil
}

// ResetBranding goes back to kipitiny's own name, colours and logo.
func (c *Core) ResetBranding(ctx context.Context) error {
	if err := RequireAdmin(ctx); err != nil {
		return err
	}
	if err := c.store.SetSetting(ctx, brandingLogoSetting, ""); err != nil {
		return err
	}
	return c.store.SetSetting(ctx, brandingSetting, "")
}

// parseLogo decodes a data URL or bare base64 image. Its type comes from
// the content, never from the data URL: PNG, JPEG, WebP or SVG.
func parseLogo(v string) ([]byte, string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, "", nil
	}
	if strings.HasPrefix(v, "data:") {
		_, payload, ok := strings.Cut(v, ",")
		if !ok || !strings.Contains(v[:len(v)-len(payload)], ";base64") {
			return nil, "", fmt.Errorf("%w: the logo must be a base64 data URL", ErrInvalid)
		}
		v = payload
	}
	if base64.StdEncoding.DecodedLen(len(v)) > MaxBrandLogo+3 {
		return nil, "", fmt.Errorf("%w: the logo must be at most %d KB", ErrInvalid, MaxBrandLogo>>10)
	}
	data, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, "", fmt.Errorf("%w: the logo isn't valid base64", ErrInvalid)
	}
	if len(data) > MaxBrandLogo {
		return nil, "", fmt.Errorf("%w: the logo must be at most %d KB", ErrInvalid, MaxBrandLogo>>10)
	}
	typ := http.DetectContentType(data)
	switch {
	case typ == "image/png", typ == "image/jpeg", typ == "image/webp":
		return data, typ, nil
	case isSVG(data):
		return data, "image/svg+xml", nil
	}
	return nil, "", fmt.Errorf("%w: the logo must be a PNG, JPEG, WebP or SVG image", ErrInvalid)
}

// isSVG reports whether data is an SVG document: an <svg> root after an
// optional XML declaration, doctype and comments.
func isSVG(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	rest := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
	for {
		switch {
		case bytes.HasPrefix(rest, []byte("<?")):
			_, after, ok := bytes.Cut(rest, []byte("?>"))
			if !ok {
				return false
			}
			rest = after
		case bytes.HasPrefix(rest, []byte("<!--")):
			_, after, ok := bytes.Cut(rest, []byte("-->"))
			if !ok {
				return false
			}
			rest = after
		case bytes.HasPrefix(rest, []byte("<!")):
			_, after, ok := bytes.Cut(rest, []byte(">"))
			if !ok {
				return false
			}
			rest = after
		default:
			return len(rest) > 4 && bytes.HasPrefix(rest, []byte("<svg")) && strings.IndexByte(" \t\r\n>/", rest[4]) >= 0
		}
		rest = bytes.TrimSpace(rest)
	}
}
