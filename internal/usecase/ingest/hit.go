package ingest

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
)

// Hit is what the tracker sends.
type Hit struct {
	URL      string         `json:"u"`
	Referrer string         `json:"r"`
	Title    string         `json:"t"`
	Screen   string         `json:"s"`
	Language string         `json:"l"`
	Name     string         `json:"n"`
	Props    map[string]any `json:"p"`
	// Identity is what the page passed to kipitiny.identify.
	Identity string `json:"i"`
}

// Visitor is what a request tells of who sent it, as its headers say:
// Country is Cloudflare's CF-IPCountry; Language and Referrer the
// Accept-Language and Referer of a pixel or a link, which run no script.
type Visitor struct {
	UserAgent, IP, Country, Language, Referrer string
}

// Hit records h, unless it comes from a bot, lacks a page URL or is for a
// host that isn't tracked.
func (s *Service) Hit(ctx context.Context, h Hit, v Visitor) error {
	e, ok := eventOf(h, v)
	if !ok || !s.Tracked(e.Hostname) {
		return nil
	}
	return s.Record(ctx, e)
}

// TrackerHit counts a pixel's view or a link's click, from the request
// itself: no script ran, so no screen size or title. q is the request's
// query, for its UTM values.
func (s *Service) TrackerHit(ctx context.Context, t analytics.Tracker, v Visitor, q url.Values) error {
	ua := v.UserAgent
	if isBot(ua) {
		return nil
	}
	lang, _, _ := strings.Cut(v.Language, ",")
	lang, _, _ = strings.Cut(lang, ";")
	e := analytics.Event{
		Hostname: t.Host(), Path: "/", IP: v.IP, UserAgent: ua,
		Browser: first(ua, browsers), OS: first(ua, systems), Device: device(ua, 0),
		Language: cut(strings.ToLower(strings.TrimSpace(lang)), 35), Country: Country(v.Country),
	}
	if ref, err := url.Parse(v.Referrer); err == nil && ref.Hostname() != "" {
		e.Referrer = strings.TrimPrefix(strings.ToLower(ref.Hostname()), "www.")
	}
	e.UTMSource, e.UTMMedium, e.UTMCampaign = cut(q.Get("utm_source"), 128), cut(q.Get("utm_medium"), 128), cut(q.Get("utm_campaign"), 128)
	e.UTMTerm, e.UTMContent = cut(q.Get("utm_term"), 128), cut(q.Get("utm_content"), 128)
	return s.Record(ctx, e)
}

var (
	screenRe  = regexp.MustCompile(`^\d{2,5}x\d{2,5}$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Country is a CF-IPCountry header's country, "" for none or unknown (XX).
func Country(h string) string {
	if countryRe.MatchString(h) && h != "XX" {
		return h
	}
	return ""
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// eventOf prepares a hit. false for bots and hits without a page URL.
func eventOf(h Hit, v Visitor) (analytics.Event, bool) {
	ua := v.UserAgent
	u, err := url.Parse(h.URL)
	if err != nil || u.Hostname() == "" || isBot(ua) || len(h.Name) > 64 {
		return analytics.Event{}, false
	}
	host := strings.ToLower(u.Hostname())
	e := analytics.Event{
		Hostname: host, Name: h.Name, Path: cut(u.Path, 512), Title: cut(h.Title, 256),
		IP: v.IP, UserAgent: ua,
		Browser: first(ua, browsers), OS: first(ua, systems),
		Language: cut(strings.ToLower(h.Language), 35), Identity: cut(h.Identity, 128),
	}
	if e.Path == "" {
		e.Path = "/"
	}
	if screenRe.MatchString(h.Screen) {
		e.Screen = h.Screen
	}
	w, _ := strconv.Atoi(strings.Split(e.Screen, "x")[0])
	e.Device = device(ua, w)
	e.Country = Country(v.Country)
	if ref, err := url.Parse(h.Referrer); err == nil && ref.Hostname() != "" && !strings.EqualFold(ref.Hostname(), host) {
		e.Referrer = strings.TrimPrefix(strings.ToLower(ref.Hostname()), "www.")
	}
	// Query values may be tokens (reset links, sessions): only the keys,
	// but UTM's values.
	q := u.Query()
	e.UTMSource, e.UTMMedium, e.UTMCampaign = cut(q.Get("utm_source"), 128), cut(q.Get("utm_medium"), 128), cut(q.Get("utm_campaign"), 128)
	e.UTMTerm, e.UTMContent = cut(q.Get("utm_term"), 128), cut(q.Get("utm_content"), 128)
	var keys []string
	for _, kv := range strings.Split(u.RawQuery, "&") {
		k, _, _ := strings.Cut(kv, "=")
		if k, err := url.QueryUnescape(k); err == nil && k != "" && !strings.HasPrefix(k, "utm_") {
			keys = append(keys, k)
		}
	}
	e.Query = cut(strings.Join(keys, "&"), 256)
	if h.Name != "" && len(h.Props) > 0 {
		e.Props = props(h.Props)
	}
	return e, true
}

// props keeps up to 20 scalar props, values cut to 128 bytes.
func props(in map[string]any) string {
	out := map[string]any{}
	for k, v := range in {
		if len(out) == 20 || len(k) > 64 {
			break
		}
		switch v := v.(type) {
		case string:
			out[k] = cut(v, 128)
		case float64, bool:
			out[k] = v
		}
	}
	if len(out) == 0 {
		return ""
	}
	b, _ := json.Marshal(out)
	return string(b)
}
