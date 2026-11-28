package analyticsdb

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
	"github.com/uptrace/bun"
)

// Funnel counts the visits of q's range (by their first step's time) that
// went through steps in order, within the visit; q's filters aside. Event
// ids order the steps: they grow as they're recorded.
func (s *Store) Funnel(ctx context.Context, q analytics.Query, steps []analytics.Step) ([]analytics.FunnelStep, error) {
	if len(steps) == 0 {
		return nil, errors.New("no steps")
	}
	out := make([]analytics.FunnelStep, len(steps))
	for i, st := range steps {
		out[i].Step = st.String()
	}
	if len(q.Hosts) == 0 {
		return out, nil
	}
	match := func(st analytics.Step) (string, any) {
		if st.Kind == "page" {
			return "e.name = '' AND e.path = ?", st.Value
		}
		return "e.name = ?", st.Value
	}
	// s1 is each visit's first step 1 in the range; s<k> its first step k
	// after s<k-1>. Plain CTEs and MIN: SQLite and Postgres alike.
	m, v := match(steps[0])
	ctes := []string{`s1 AS (SELECT e.visit_id, MIN(e.id) AS id FROM events e
		WHERE e.hostname IN (?) AND e.created_at >= ? AND e.created_at < ? AND ` + m + ` GROUP BY e.visit_id)`}
	args := []any{bun.List(q.Hosts), q.From.Unix(), q.To.Unix(), v}
	counts := []string{"(SELECT COUNT(*) FROM s1)"}
	for i := 1; i < len(steps); i++ {
		m, v := match(steps[i])
		ctes = append(ctes, fmt.Sprintf(`s%d AS (SELECT e.visit_id, MIN(e.id) AS id FROM events e
			JOIN s%d p ON e.visit_id = p.visit_id WHERE e.id > p.id AND %s GROUP BY e.visit_id)`, i+1, i, m))
		args = append(args, v)
		counts = append(counts, fmt.Sprintf("(SELECT COUNT(*) FROM s%d)", i+1))
	}
	dest := make([]any, len(steps))
	for i := range out {
		dest[i] = &out[i].Visits
	}
	err := s.db.NewRaw("WITH "+strings.Join(ctes, ", ")+" SELECT "+strings.Join(counts, ", "), args...).Scan(ctx, dest...)
	return out, err
}
