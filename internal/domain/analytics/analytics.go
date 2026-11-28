// Package analytics is the vocabulary of kipitiny's cookieless web
// analytics: what the server (`kipitiny analytics`, its own container and
// SQLite file) records and answers, and what the manager reads through its
// client.
package analytics

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Ports the server listens on, in its container.
const (
	PublicPort  = 8080
	PrivatePort = 8081
)

// ErrNotFound is a visit that isn't there, or not on the hosts asked.
var ErrNotFound = errors.New("not found")

// ErrInvalid is a tracker the server refuses: the message says why.
var ErrInvalid = errors.New("invalid")

// Paths are where a site's own domain serves the tracker script and takes
// hits, instead of /a.js and /e of the analytics domain (which keep
// working): ad blockers skip a first-party path they don't know.
type Paths struct {
	Script string `json:"script"`
	Hit    string `json:"hit"`
}

// Event is one pageview (Name empty) or custom event, as the server
// prepared it from a hit.
type Event struct {
	Hostname, Name, Path, Query, Title, Referrer           string
	UTMSource, UTMMedium, UTMCampaign, UTMTerm, UTMContent string
	Browser, OS, Device, Screen, Language, Country, Props  string
	// Identity is what the page passed to kipitiny.identify, kept on the visit.
	Identity      string
	IP, UserAgent string // hashed into the visitor, never stored
}

// IdentifyEvent is the hit of kipitiny.identify: it names the visit's
// visitor (its props kept with it) instead of being counted.
const IdentifyEvent = "$identify"

// Query selects the events of Stats.
type Query struct {
	Hosts    []string
	From, To time.Time
	// Interval is the series' bucket; Offset (seconds east of UTC) aligns
	// day buckets to the viewer's midnight.
	Interval time.Duration
	Offset   int
	Filters  []Filter
}

// Filter narrows stats to a value of a breakdown (props aside): the
// matching events for a pageview column, the visits with it otherwise.
type Filter struct{ Key, Value string }

// VisitFilters are on the visits' own columns.
var VisitFilters = map[string]string{"entries": "entry", "exits": "exit", "identities": "identity"}

// PageviewBreakdowns are columns of pageviews; Stats counts each value's
// pageviews and visitors.
var PageviewBreakdowns = map[string]string{
	"pages": "path", "titles": "title", "queries": "query", "referrers": "referrer",
	"utm_source": "utm_source", "utm_medium": "utm_medium", "utm_campaign": "utm_campaign",
	"utm_term": "utm_term", "utm_content": "utm_content",
	"browsers": "browser", "os": "os", "devices": "device", "screens": "screen",
	"languages": "language", "countries": "country",
}

// ParseFilters reads "<breakdown>:<value>" filters.
func ParseFilters(vs []string) ([]Filter, error) {
	if len(vs) > 10 {
		return nil, errors.New("at most 10 filters")
	}
	fs := []Filter{}
	for _, v := range vs {
		k, val, ok := strings.Cut(v, ":")
		_, pv := PageviewBreakdowns[k]
		_, vi := VisitFilters[k]
		if !ok || !pv && !vi && k != "events" || len(val) > 512 {
			return nil, fmt.Errorf("filter %q: want <breakdown>:<value>", v)
		}
		fs = append(fs, Filter{k, val})
	}
	return fs, nil
}

// Summary is a range's totals.
type Summary struct {
	Pageviews int `json:"pageviews"`
	Visitors  int `json:"visitors"`
	Visits    int `json:"visits"`
	Bounces   int `json:"bounces"`
	// Duration sums the visits' lengths, in seconds.
	Duration int64 `json:"duration"`
}

type Stats struct {
	Summary
	// Previous is the same span just before, to compare with.
	Previous Summary `json:"previous"`
	// Active counts visitors of the last 30 minutes, whatever the range.
	Active     int              `json:"active"`
	Series     []Point          `json:"series"`
	Breakdowns map[string][]Row `json:"breakdowns"`
}

type Point struct {
	T         int64 `json:"t"`
	Pageviews int   `json:"pageviews"`
	Visitors  int   `json:"visitors"`
}

type Row struct {
	Value    string `json:"value"`
	Count    int    `json:"count"`
	Visitors int    `json:"visitors"`
}

// Visit is one visit, as its first event saw the visitor.
type Visit struct {
	ID        string `json:"id"`
	StartedAt int64  `json:"startedAt"`
	LastAt    int64  `json:"lastAt"`
	Pageviews int    `json:"pageviews"`
	// Events counts the custom events.
	Events      int            `json:"events"`
	Entry       string         `json:"entry"`
	Exit        string         `json:"exit"`
	Referrer    string         `json:"referrer"`
	UTMSource   string         `json:"utmSource"`
	UTMMedium   string         `json:"utmMedium"`
	UTMCampaign string         `json:"utmCampaign"`
	Country     string         `json:"country"`
	Browser     string         `json:"browser"`
	OS          string         `json:"os"`
	Device      string         `json:"device"`
	Screen      string         `json:"screen"`
	Language    string         `json:"language"`
	Identity    string         `json:"identity"`
	IdentityRaw string         `json:"-" bun:"identity_props"`
	Props       map[string]any `json:"identityProps,omitempty" bun:"-"`
}

// VisitEvent is a step of a visit's journey: a pageview (Name empty) or a
// custom event.
type VisitEvent struct {
	At    int64          `json:"at"`
	Name  string         `json:"name"`
	Path  string         `json:"path"`
	Query string         `json:"query"`
	Title string         `json:"title"`
	Raw   string         `json:"-" bun:"props"`
	Props map[string]any `json:"props,omitempty" bun:"-"`
}

type VisitDetail struct {
	Visit
	Journey []VisitEvent `json:"journey"`
}

// Step of a funnel: a pageview of a path (Kind "page") or a custom event
// (Kind "event").
type Step struct{ Kind, Value string }

func (s Step) String() string { return s.Kind + ":" + s.Value }

// maxSteps caps a funnel.
const maxSteps = 8

// ParseSteps reads "page:<path>" and "event:<name>" steps, in order.
func ParseSteps(vs []string) ([]Step, error) {
	if len(vs) < 2 || len(vs) > maxSteps {
		return nil, fmt.Errorf("a funnel has 2 to %d steps", maxSteps)
	}
	steps := make([]Step, 0, len(vs))
	for _, v := range vs {
		k, val, ok := strings.Cut(v, ":")
		if !ok || (k != "page" && k != "event") || val == "" || len(val) > 512 {
			return nil, fmt.Errorf("step %q: want page:<path> or event:<name>", v)
		}
		steps = append(steps, Step{k, val})
	}
	return steps, nil
}

// FunnelStep is how many of the range's visits reached a step, each after
// the ones before.
type FunnelStep struct {
	Step   string `json:"step"`
	Visits int    `json:"visits"`
}
