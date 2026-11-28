package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
)

// sentryEvent is the part of an SDK's event payload read.
type sentryEvent struct {
	EventID     string          `json:"event_id"`
	Timestamp   json.RawMessage `json:"timestamp"`
	Level       string          `json:"level"`
	Platform    string          `json:"platform"`
	Release     string          `json:"release"`
	Environment string          `json:"environment"`
	ServerName  string          `json:"server_name"`
	Transaction string          `json:"transaction"`
	Message     json.RawMessage `json:"message"`
	Logentry    *struct {
		Formatted string `json:"formatted"`
		Message   string `json:"message"`
	} `json:"logentry"`
	Exception   json.RawMessage `json:"exception"`
	Stacktrace  *sentryStack    `json:"stacktrace"`
	Fingerprint []string        `json:"fingerprint"`
	User        *struct {
		ID        json.RawMessage `json:"id"`
		Username  string          `json:"username"`
		Email     string          `json:"email"`
		IPAddress string          `json:"ip_address"`
	} `json:"user"`
	Request *struct {
		URL    string `json:"url"`
		Method string `json:"method"`
	} `json:"request"`
	Tags        json.RawMessage           `json:"tags"`
	Breadcrumbs json.RawMessage           `json:"breadcrumbs"`
	Contexts    map[string]map[string]any `json:"contexts"`
	SDK         *struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"sdk"`
}

type sentryException struct {
	Type      string       `json:"type"`
	Value     string       `json:"value"`
	Module    string       `json:"module"`
	Stack     *sentryStack `json:"stacktrace"`
	Mechanism *struct {
		Type    string `json:"type"`
		Handled *bool  `json:"handled"`
	} `json:"mechanism"`
}

type sentryStack struct {
	Frames []struct {
		Function    string   `json:"function"`
		Module      string   `json:"module"`
		Filename    string   `json:"filename"`
		AbsPath     string   `json:"abs_path"`
		Lineno      int      `json:"lineno"`
		Colno       int      `json:"colno"`
		InApp       *bool    `json:"in_app"`
		ContextLine *string  `json:"context_line"`
		PreContext  []string `json:"pre_context"`
		PostContext []string `json:"post_context"`
	} `json:"frames"`
}

// Caps on what an event keeps.
const (
	maxFrames      = 50
	maxExceptions  = 5
	maxBreadcrumbs = 50
	maxTags        = 50
	maxContextRows = 5
)

// ParsedEvent is an SDK's event read: what is kept, its own fingerprint
// if any, and a hash telling its user apart (no email or address kept).
type ParsedEvent struct {
	Event       analytics.ErrorEvent
	Fingerprint []string
	User        string
}

// ParseError reads an SDK's event.
func (s *Service) ParseError(raw []byte) (ParsedEvent, error) { return parseEvent(raw, s.now()) }

// RecordError adds p to its issue in project, opening one for a new
// fingerprint and reopening a resolved one.
func (s *Service) RecordError(ctx context.Context, project int64, p ParsedEvent) (analytics.Recorded, error) {
	fp, title, culprit := fingerprint(p.Event, p.Fingerprint)
	return s.AddError(ctx, project, analytics.ErrorRecord{Event: p.Event, Fingerprint: fp, Title: title, Culprit: culprit, User: p.User})
}

// parseEvent reads an SDK's event; now stamps one without a timestamp.
func parseEvent(raw []byte, now time.Time) (ParsedEvent, error) {
	var in sentryEvent
	if err := json.Unmarshal(raw, &in); err != nil {
		return ParsedEvent{}, fmt.Errorf("event: %w", err)
	}
	p := ParsedEvent{Fingerprint: in.Fingerprint}
	ev := analytics.ErrorEvent{
		ID: cut(strings.ReplaceAll(in.EventID, "-", ""), 32), At: timestamp(in.Timestamp, now),
		Level: cut(in.Level, 16), Platform: cut(in.Platform, 32),
		Release: cut(in.Release, 200), Environment: cut(in.Environment, 64),
		ServerName: cut(in.ServerName, 128), Transaction: cut(in.Transaction, 256),
		Message: cut(message(in), 2048),
	}
	if ev.Level == "" {
		ev.Level = "error"
	}
	if in.SDK != nil {
		ev.SDK = cut(in.SDK.Name+" "+in.SDK.Version, 64)
	}
	if in.User != nil {
		u := in.User
		ev.User = cut(scalar(u.ID), 128)
		if ev.User == "" {
			ev.User = cut(u.Username, 128)
		}
		for _, v := range []string{scalar(u.ID), u.Username, u.Email, u.IPAddress} {
			if v != "" && v != "{{auto}}" {
				h := sha256.Sum256([]byte(v))
				p.User = hex.EncodeToString(h[:12])
				break
			}
		}
	}
	if in.Request != nil {
		ev.URL, ev.Method = keysOnly(in.Request.URL), cut(in.Request.Method, 10)
	}
	ev.Exceptions = exceptions(in.Exception)
	if len(ev.Exceptions) == 0 && in.Stacktrace != nil {
		// captureMessage with attach_stacktrace.
		ev.Exceptions = []analytics.Exception{{Frames: frames(in.Stacktrace)}}
	}
	ev.Tags = tags(in.Tags)
	ev.Breadcrumbs = breadcrumbs(in.Breadcrumbs)
	for _, k := range []string{"browser", "os", "runtime", "device"} {
		c := in.Contexts[k]
		if c == nil {
			continue
		}
		v := strings.TrimSpace(fmt.Sprint(c["name"]) + " " + fmt.Sprint(c["version"]))
		v = strings.TrimSpace(strings.ReplaceAll(v, "<nil>", ""))
		if v != "" {
			if ev.Contexts == nil {
				ev.Contexts = map[string]string{}
			}
			ev.Contexts[k] = cut(v, 64)
		}
	}
	p.Event = ev
	return p, nil
}

func timestamp(raw json.RawMessage, now time.Time) int64 {
	var f float64
	if json.Unmarshal(raw, &f) == nil && f > 0 {
		return int64(f)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Unix()
			}
		}
	}
	return now.Unix()
}

// scalar is a string or number JSON value as text.
func scalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func message(in sentryEvent) string {
	if in.Logentry != nil {
		if in.Logentry.Formatted != "" {
			return in.Logentry.Formatted
		}
		return in.Logentry.Message
	}
	var s string
	if json.Unmarshal(in.Message, &s) == nil {
		return s
	}
	var m struct{ Formatted, Message string }
	if json.Unmarshal(in.Message, &m) == nil {
		if m.Formatted != "" {
			return m.Formatted
		}
		return m.Message
	}
	return ""
}

// exceptions reads {"values": [...]} or a bare list.
func exceptions(raw json.RawMessage) []analytics.Exception {
	var list []sentryException
	var wrapped struct {
		Values []sentryException `json:"values"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Values != nil {
		list = wrapped.Values
	} else {
		_ = json.Unmarshal(raw, &list)
	}
	if len(list) > maxExceptions {
		list = list[len(list)-maxExceptions:]
	}
	var out []analytics.Exception
	for _, e := range list {
		if e.Type == "" && e.Value == "" {
			continue
		}
		x := analytics.Exception{Type: cut(e.Type, 200), Value: cut(e.Value, 2048), Module: cut(e.Module, 200), Frames: frames(e.Stack)}
		if e.Mechanism != nil {
			x.Mechanism, x.Handled = cut(e.Mechanism.Type, 64), e.Mechanism.Handled
		}
		out = append(out, x)
	}
	return out
}

func frames(st *sentryStack) []analytics.Frame {
	if st == nil {
		return nil
	}
	fs := st.Frames
	if len(fs) > maxFrames {
		fs = fs[len(fs)-maxFrames:] // the ones nearest the error
	}
	out := make([]analytics.Frame, 0, len(fs))
	for _, f := range fs {
		file := f.Filename
		if file == "" {
			file = f.AbsPath
		}
		fr := analytics.Frame{Function: cut(f.Function, 200), Module: cut(f.Module, 200), File: cut(file, 300), Line: f.Lineno, Col: f.Colno}
		fr.InApp = f.InApp != nil && *f.InApp
		if f.ContextLine != nil {
			pre := tail(f.PreContext, maxContextRows)
			for _, l := range pre {
				fr.Context = append(fr.Context, cut(l, 200))
			}
			fr.ContextLine = len(fr.Context)
			fr.Context = append(fr.Context, cut(*f.ContextLine, 200))
			for _, l := range head(f.PostContext, maxContextRows) {
				fr.Context = append(fr.Context, cut(l, 200))
			}
		}
		out = append(out, fr)
	}
	return out
}

func head(s []string, n int) []string { return s[:min(n, len(s))] }
func tail(s []string, n int) []string { return s[max(0, len(s)-n):] }

// tags reads an object or a list of [key, value] pairs.
func tags(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	add := func(k string, v any) {
		if len(out) < maxTags && k != "" {
			out[cut(k, 64)] = cut(strings.TrimSpace(fmt.Sprint(v)), 200)
		}
	}
	var m map[string]any
	var pairs [][]any
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			add(k, v)
		}
	} else if json.Unmarshal(raw, &pairs) == nil {
		for _, p := range pairs {
			if len(p) == 2 {
				k, _ := p[0].(string)
				add(k, p[1])
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// breadcrumbData keeps these keys of a breadcrumb's data, URLs without
// their query values.
var breadcrumbData = map[string]bool{"method": true, "status_code": true, "url": true, "from": true, "to": true, "reason": true}

func breadcrumbs(raw json.RawMessage) []analytics.Breadcrumb {
	type crumb struct {
		Timestamp json.RawMessage `json:"timestamp"`
		Type      string          `json:"type"`
		Category  string          `json:"category"`
		Level     string          `json:"level"`
		Message   string          `json:"message"`
		Data      map[string]any  `json:"data"`
	}
	var list []crumb
	var wrapped struct {
		Values []crumb `json:"values"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Values != nil {
		list = wrapped.Values
	} else {
		_ = json.Unmarshal(raw, &list)
	}
	if len(list) > maxBreadcrumbs {
		list = list[len(list)-maxBreadcrumbs:]
	}
	var out []analytics.Breadcrumb
	for _, c := range list {
		b := analytics.Breadcrumb{Type: cut(c.Type, 32), Category: cut(c.Category, 64), Level: cut(c.Level, 16), Message: cut(c.Message, 512)}
		var f float64
		if json.Unmarshal(c.Timestamp, &f) == nil {
			b.At = f
		} else if s := scalar(c.Timestamp); s != "" {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				b.At = float64(t.UnixMilli()) / 1000
			}
		}
		for k, v := range c.Data {
			if !breadcrumbData[k] {
				continue
			}
			s := cut(fmt.Sprint(v), 300)
			if k == "url" || k == "from" || k == "to" {
				s = keysOnly(s)
			}
			if b.Data == nil {
				b.Data = map[string]string{}
			}
			b.Data[k] = s
		}
		out = append(out, b)
	}
	return out
}

// keysOnly drops a URL's query values (tokens) and fragment.
func keysOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	var keys []string
	for _, kv := range strings.Split(u.RawQuery, "&") {
		if k, _, _ := strings.Cut(kv, "="); k != "" {
			keys = append(keys, k)
		}
	}
	u.RawQuery, u.Fragment, u.User = strings.Join(keys, "&"), "", nil
	return cut(u.String(), 512)
}

var (
	uuidRe   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	hexRe    = regexp.MustCompile(`(?i)\b(0x)?[0-9a-f]{8,}\b`)
	numRe    = regexp.MustCompile(`\d+(\.\d+)?`)
	chunkRe  = regexp.MustCompile(`[.-][0-9a-f]{6,}(\.[a-z]+)$`)
	schemeRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^/]*`)
)

// normalized is a message with what varies between occurrences (ids,
// numbers) replaced, for grouping.
func normalized(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	s = uuidRe.ReplaceAllString(s, "<id>")
	s = hexRe.ReplaceAllString(s, "<hex>")
	return cut(numRe.ReplaceAllString(s, "<n>"), 200)
}

// frameFile is a frame's file without what changes per build: the
// scheme and host of a script's URL, the query, a bundle's content hash.
func frameFile(f analytics.Frame) string {
	if f.Module != "" {
		return f.Module
	}
	file, _, _ := strings.Cut(f.File, "?")
	file = schemeRe.ReplaceAllString(file, "")
	return chunkRe.ReplaceAllString(file, "$1")
}

// grouped are the parts of ev's default fingerprint, its title and the
// place it was thrown: the raised exception's type and its 3 innermost
// frames of the app's own code (any frames when none is marked as such),
// or its normalized message when it has no frames.
func grouped(ev analytics.ErrorEvent) (parts []string, title, culprit string) {
	if len(ev.Exceptions) == 0 {
		title = firstLine(ev.Message)
		if title == "" {
			title = "(no message)"
		}
		culprit = ev.Transaction
		return []string{"message", normalized(ev.Message)}, cut(title, 200), cut(culprit, 200)
	}
	x := ev.Exceptions[len(ev.Exceptions)-1]
	title = x.Type
	if v := firstLine(x.Value); v != "" {
		if title != "" {
			title += ": "
		}
		title += v
	}
	var fs []analytics.Frame
	for _, f := range x.Frames {
		if f.InApp {
			fs = append(fs, f)
		}
	}
	if len(fs) == 0 {
		fs = x.Frames
	}
	parts = []string{"exception", x.Type}
	for i := len(fs) - 1; i >= 0 && i >= len(fs)-3; i-- {
		parts = append(parts, fs[i].Function+"@"+frameFile(fs[i]))
	}
	if len(fs) == 0 {
		parts = append(parts, normalized(x.Value))
	} else {
		top := fs[len(fs)-1]
		culprit = top.Function
		if f := top.File; f != "" {
			if culprit != "" {
				culprit += " · "
			}
			culprit += f
			if top.Line > 0 {
				culprit += ":" + strconv.Itoa(top.Line)
			}
		}
	}
	if culprit == "" {
		culprit = ev.Transaction
	}
	return parts, cut(title, 200), cut(culprit, 200)
}

// fingerprint is the issue key of ev: its SDK's fingerprint when set,
// where "{{ default }}" stands for the default one.
func fingerprint(ev analytics.ErrorEvent, custom []string) (fp, title, culprit string) {
	parts, title, culprit := grouped(ev)
	if len(custom) > 0 {
		var out []string
		for _, c := range custom {
			if strings.ReplaceAll(c, " ", "") == "{{default}}" {
				out = append(out, parts...)
			} else {
				out = append(out, "custom", c)
			}
		}
		parts = out
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:16]), title, culprit
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
