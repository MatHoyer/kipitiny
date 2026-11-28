package collector

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"

	"github.com/kipit-labs/kipitiny/internal/usecase/ingest"
)

// Limits of what an SDK may send: a request's body as sent and once
// decompressed; errorCooldown is the seconds an SDK waits once its project
// is rate limited (ingest.Service.Allow).
const (
	maxEnvelope   = 1 << 20
	maxDecoded    = 4 << 20
	errorCooldown = 10
)

// errorsCORS lets browser SDKs post from any page: the key is public
// anyway, and only checked against its project.
func errorsCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Content-Encoding, X-Sentry-Auth, sentry-trace, baggage")
	h.Set("Access-Control-Expose-Headers", "X-Sentry-Rate-Limits, Retry-After")
}

// ingestErrors takes the errors of Sentry SDKs at /api/{project}/envelope/
// (and the older /store/, one event as JSON).
func ingestErrors(svc *ingest.Service, log *slog.Logger, envelope bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		errorsCORS(w)
		project, err := strconv.ParseInt(r.PathValue("project"), 10, 64)
		want := svc.ErrorKey(project)
		if err != nil || want == "" {
			sentryError(w, http.StatusForbidden, "unknown project")
			return
		}
		body, status, err := readBody(w, r)
		if err != nil {
			sentryError(w, status, err.Error())
			return
		}
		var hdr envelopeHeader
		var events [][]byte
		if envelope {
			if hdr, events, err = parseEnvelope(body); err != nil {
				sentryError(w, http.StatusBadRequest, err.Error())
				return
			}
		} else {
			events = [][]byte{body}
		}
		key, err := sentryKey(r, hdr.DSN)
		if err != nil {
			sentryError(w, http.StatusBadRequest, err.Error())
			return
		}
		if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(want)) != 1 {
			sentryError(w, http.StatusForbidden, "wrong key for this project")
			return
		}
		if !svc.Allow(project) {
			w.Header().Set("Retry-After", strconv.Itoa(errorCooldown))
			w.Header().Set("X-Sentry-Rate-Limits", strconv.Itoa(errorCooldown)+"::key")
			sentryError(w, http.StatusTooManyRequests, "too many errors, try again later")
			return
		}
		id := hdr.EventID
		for _, raw := range events {
			p, err := svc.ParseError(raw)
			if err != nil {
				sentryError(w, http.StatusBadRequest, err.Error())
				return
			}
			if p.Event.ID == "" {
				p.Event.ID = strings.ReplaceAll(hdr.EventID, "-", "")
			}
			if c := ingest.Country(r.Header.Get("CF-IPCountry")); c != "" {
				p.Event.Country = c
			}
			if _, err := svc.RecordError(r.Context(), project, p); err != nil {
				log.Error("record error", "err", err)
				sentryError(w, http.StatusInternalServerError, "not recorded")
				return
			}
			id = p.Event.ID
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
	}
}

func sentryError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("X-Sentry-Error", msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"detail": msg})
}

// readBody reads the request's body, decompressed, within the limits.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, int, error) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEnvelope))
	if err != nil {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("body over %d bytes", maxEnvelope)
	}
	var rd io.Reader
	switch enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); enc {
	case "", "identity":
		return raw, 0, nil
	case "gzip":
		rd, err = gzip.NewReader(bytes.NewReader(raw))
	case "deflate":
		rd, err = zlib.NewReader(bytes.NewReader(raw))
	case "zstd":
		var d *zstd.Decoder
		if d, err = zstd.NewReader(bytes.NewReader(raw), zstd.WithDecoderMaxMemory(maxDecoded)); err == nil {
			defer d.Close()
			rd = d
		}
	default:
		// br (sentry-python with brotli installed): set
		// transport_compression_algo to gzip.
		return nil, http.StatusUnsupportedMediaType, fmt.Errorf("content encoding %q: send gzip, deflate or zstd", enc)
	}
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("decompress: %w", err)
	}
	out, err := io.ReadAll(io.LimitReader(rd, maxDecoded+1))
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("decompress: %w", err)
	}
	if len(out) > maxDecoded {
		return nil, http.StatusRequestEntityTooLarge, fmt.Errorf("body over %d bytes decompressed", maxDecoded)
	}
	return out, 0, nil
}

// sentryKey is the DSN key the SDK authenticates with: X-Sentry-Auth's,
// the query's (browsers) or the envelope header's DSN. Several must agree.
func sentryKey(r *http.Request, dsn string) (string, error) {
	var keys []string
	if h := r.Header.Get("X-Sentry-Auth"); h != "" {
		h = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(h), "Sentry "))
		for _, kv := range strings.Split(h, ",") {
			if k, v, ok := strings.Cut(strings.TrimSpace(kv), "="); ok && k == "sentry_key" {
				keys = append(keys, strings.TrimSpace(v))
			}
		}
	}
	if k := r.URL.Query().Get("sentry_key"); k != "" {
		keys = append(keys, k)
	}
	if dsn != "" {
		if u, err := url.Parse(dsn); err == nil && u.User != nil {
			keys = append(keys, u.User.Username())
		}
	}
	for _, k := range keys[min(1, len(keys)):] {
		if k != keys[0] {
			return "", errors.New("the request's keys differ")
		}
	}
	if len(keys) == 0 {
		return "", nil
	}
	return keys[0], nil
}

type envelopeHeader struct {
	EventID string `json:"event_id"`
	DSN     string `json:"dsn"`
}

// parseEnvelope reads an envelope: a header line, then items of a header
// line and a payload (length bytes, else up to the next newline). Only
// events are returned; transactions, sessions, attachments are dropped.
func parseEnvelope(b []byte) (envelopeHeader, [][]byte, error) {
	var hdr envelopeHeader
	line, rest, _ := bytes.Cut(b, []byte("\n"))
	if err := json.Unmarshal(line, &hdr); err != nil {
		return hdr, nil, fmt.Errorf("envelope header: %w", err)
	}
	var events [][]byte
	for len(bytes.TrimSpace(rest)) > 0 {
		var item struct {
			Type   string `json:"type"`
			Length *int   `json:"length"`
		}
		line, rest, _ = bytes.Cut(rest, []byte("\n"))
		if err := json.Unmarshal(line, &item); err != nil {
			return hdr, nil, fmt.Errorf("item header: %w", err)
		}
		var payload []byte
		if item.Length != nil {
			n := *item.Length
			if n < 0 || n > len(rest) {
				return hdr, nil, errors.New("item longer than the envelope")
			}
			payload, rest = rest[:n], rest[n:]
			if len(rest) > 0 {
				if rest[0] != '\n' {
					return hdr, nil, errors.New("item not followed by a newline")
				}
				rest = rest[1:]
			}
		} else {
			payload, rest, _ = bytes.Cut(rest, []byte("\n"))
		}
		if item.Type == "event" {
			events = append(events, payload)
		}
	}
	return hdr, events, nil
}
