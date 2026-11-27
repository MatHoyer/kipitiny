package monitoring

import (
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/analytics"
)

func TestAnalyticsCSV(t *testing.T) {
	s := analytics.Stats{
		Summary:  analytics.Summary{Visitors: 3, Visits: 4, Pageviews: 9, Bounces: 1, Duration: 120},
		Previous: analytics.Summary{Visitors: 1},
		Series:   []analytics.Point{{T: 86400, Pageviews: 9, Visitors: 3}},
		Breakdowns: map[string][]analytics.Row{
			"pages":     {{Value: "/", Count: 5, Visitors: 3}, {Value: "=HYPERLINK(1)", Count: 1, Visitors: 1}},
			"countries": {{Value: "FR, BE", Count: 2, Visitors: 2}},
		},
	}
	for table, want := range map[string]string{
		"summary":    "metric,value,previous\nvisitors,3,1\nvisits,4,0\npageviews,9,0\nbounces,1,0\nduration_seconds,120,0\n",
		"series":     "time,pageviews,visitors\n1970-01-02T00:00:00Z,9,3\n",
		"breakdowns": "breakdown,value,visitors,count\ncountries,\"FR, BE\",2,2\npages,/,3,5\npages,'=HYPERLINK(1),1,1\n",
	} {
		var b strings.Builder
		if err := statsCSV(&b, s, table); err != nil || b.String() != want {
			t.Errorf("%s: %v\n%s\nwant\n%s", table, err, b.String(), want)
		}
	}
	var b strings.Builder
	visitsCSV(&b, []analytics.Visit{{ID: "01V", StartedAt: 60, LastAt: 120, Pageviews: 2, Entry: "/", Exit: "-x", Identity: "u1"}})
	if lines := strings.Split(b.String(), "\n"); len(lines) != 3 || lines[1] != "01V,1970-01-01T00:01:00Z,1970-01-01T00:02:00Z,2,0,/,'-x,,,,,,,,,,,u1" {
		t.Errorf("visits:\n%s", b.String())
	}
}
