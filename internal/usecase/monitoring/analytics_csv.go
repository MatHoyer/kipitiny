package monitoring

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
)

// AnalyticsExports are the tables ServiceAnalyticsCSV writes.
var AnalyticsExports = []string{"summary", "series", "breakdowns", "visits"}

// maxExportVisits caps a visits export.
// shortcut: 10000 visits, newest first; page through the API's visits if a
// range has more.
const maxExportVisits = 10000

// ServiceAnalyticsCSV writes one table of the app's stats over q, filters
// applied, as CSV: the summary (and the previous span's), the series, the
// breakdowns or the visits.
func (c *Monitoring) ServiceAnalyticsCSV(ctx context.Context, serviceID string, q AnalyticsQuery, table string, w io.Writer) error {
	if !slices.Contains(AnalyticsExports, table) {
		return fmt.Errorf("%w: export one of %s", domain.ErrInvalid, strings.Join(AnalyticsExports, ", "))
	}
	if table == "visits" {
		var all []analytics.Visit
		before := ""
		for len(all) < maxExportVisits {
			vs, err := c.ServiceVisits(ctx, serviceID, q, before, 100)
			if err != nil {
				return err
			}
			all = append(all, vs...)
			if len(vs) < 100 {
				break
			}
			before = vs[len(vs)-1].ID
		}
		return visitsCSV(w, all)
	}
	v, err := c.ServiceAnalytics(ctx, serviceID, q)
	if err != nil {
		return err
	}
	return statsCSV(w, v.Stats, table)
}

// cell keeps a spreadsheet from running visitor-sent text (a path, a title)
// as a formula.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func utc(t int64) string { return time.Unix(t, 0).UTC().Format(time.RFC3339) }

func statsCSV(w io.Writer, s analytics.Stats, table string) error {
	n := strconv.Itoa
	var rows [][]string
	switch table {
	case "summary":
		rows = append(rows, []string{"metric", "value", "previous"})
		for _, m := range []struct {
			name      string
			now, prev int64
		}{
			{"visitors", int64(s.Visitors), int64(s.Previous.Visitors)},
			{"visits", int64(s.Visits), int64(s.Previous.Visits)},
			{"pageviews", int64(s.Pageviews), int64(s.Previous.Pageviews)},
			{"bounces", int64(s.Bounces), int64(s.Previous.Bounces)},
			{"duration_seconds", s.Duration, s.Previous.Duration},
		} {
			rows = append(rows, []string{m.name, strconv.FormatInt(m.now, 10), strconv.FormatInt(m.prev, 10)})
		}
	case "series":
		rows = append(rows, []string{"time", "pageviews", "visitors"})
		for _, p := range s.Series {
			rows = append(rows, []string{utc(p.T), n(p.Pageviews), n(p.Visitors)})
		}
	case "breakdowns":
		rows = append(rows, []string{"breakdown", "value", "visitors", "count"})
		keys := make([]string, 0, len(s.Breakdowns))
		for k := range s.Breakdowns {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			for _, r := range s.Breakdowns[k] {
				rows = append(rows, []string{k, cell(r.Value), n(r.Visitors), n(r.Count)})
			}
		}
	}
	return csv.NewWriter(w).WriteAll(rows)
}

func visitsCSV(w io.Writer, vs []analytics.Visit) error {
	rows := [][]string{{"id", "started_at", "last_at", "pageviews", "events", "entry", "exit", "referrer",
		"utm_source", "utm_medium", "utm_campaign", "country", "browser", "os", "device", "screen", "language", "identity"}}
	for _, v := range vs {
		rows = append(rows, []string{v.ID, utc(v.StartedAt), utc(v.LastAt), strconv.Itoa(v.Pageviews), strconv.Itoa(v.Events),
			cell(v.Entry), cell(v.Exit), cell(v.Referrer), cell(v.UTMSource), cell(v.UTMMedium), cell(v.UTMCampaign),
			v.Country, v.Browser, v.OS, v.Device, v.Screen, cell(v.Language), cell(v.Identity)})
	}
	return csv.NewWriter(w).WriteAll(rows)
}
