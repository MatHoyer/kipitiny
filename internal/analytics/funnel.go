package analytics

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

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

// Funnel counts the visits of q's range (by their first step's time) that
// went through steps in order, within the visit; q's filters aside. Event
// ids order the steps: they grow as they're recorded.
func (s *Store) Funnel(ctx context.Context, q Query, steps []Step) ([]FunnelStep, error) {
	if len(steps) == 0 {
		return nil, errors.New("no steps")
	}
	out := make([]FunnelStep, len(steps))
	for i, st := range steps {
		out[i].Step = st.String()
	}
	if len(q.Hosts) == 0 {
		return out, nil
	}
	match := func(st Step) (string, any) {
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
	args := []any{bun.In(q.Hosts), q.From.Unix(), q.To.Unix(), v}
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
