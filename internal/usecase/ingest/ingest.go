// Package ingest is what the analytics server (`kipitiny analytics`) does
// with what it receives: a hit becomes an event of a tracked site, an SDK's
// error joins its issue, a project's errors are rate limited, old data is
// pruned. The queries the manager reads pass through to the store.
package ingest

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kipit-labs/kipitiny/internal/domain/analytics"
)

// Store keeps the events, in the server's own SQLite file
// (infrastructure/analyticsdb).
type Store interface {
	// Tracked is true for a hostname the manager sent; SitePaths are its own
	// paths, if it has some.
	Tracked(host string) bool
	SitePaths(host string) (analytics.Paths, bool)
	SetSites(ctx context.Context, hosts []string, paths map[string]analytics.Paths) error
	// Record stores e, in the visit its visitor is on or a new one.
	Record(ctx context.Context, e analytics.Event) error
	// Prune deletes what is older than keep.
	Prune(ctx context.Context, keep time.Duration) error

	Stats(ctx context.Context, q analytics.Query) (analytics.Stats, error)
	Visits(ctx context.Context, q analytics.Query, before string, limit int) ([]analytics.Visit, error)
	Visit(ctx context.Context, hosts []string, id string) (analytics.VisitDetail, error)
	Funnel(ctx context.Context, q analytics.Query, steps []analytics.Step) ([]analytics.FunnelStep, error)

	// TrackerBySlug is the pixel or link the public port serves at slug.
	TrackerBySlug(kind, slug string) (analytics.Tracker, bool)
	Trackers(ctx context.Context) ([]analytics.Tracker, error)
	Tracker(ctx context.Context, id string) (analytics.Tracker, error)
	CreateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error)
	UpdateTracker(ctx context.Context, t analytics.Tracker) (analytics.Tracker, error)
	DeleteTracker(ctx context.Context, id string) error

	// ErrorKey is project's DSN key, "" when it takes no errors.
	ErrorKey(project int64) string
	SetErrorProjects(ctx context.Context, ps []analytics.ErrorProject) error
	// AddError adds r to its issue in project, opening one for a new
	// fingerprint and reopening a resolved one.
	AddError(ctx context.Context, project int64, r analytics.ErrorRecord) (analytics.Recorded, error)
	Issues(ctx context.Context, q analytics.IssueQuery) ([]analytics.Issue, map[string]int, error)
	Issue(ctx context.Context, project, id int64) (analytics.Issue, error)
	SetIssueStatus(ctx context.Context, project, id int64, status string) (analytics.Issue, error)
	Event(ctx context.Context, project, issue int64, id string) (analytics.IssueEvent, error)
	ErrorSeries(ctx context.Context, project int64, from, to time.Time, interval time.Duration) ([]analytics.ErrorPoint, error)
	ErrorChanges(ctx context.Context, after int64) ([]analytics.ErrorChange, int64, error)
	ReleaseErrors(ctx context.Context, project int64, release string, from, to time.Time) (analytics.ReleaseErrors, error)
}

// Service is the server's use cases, over its store.
type Service struct {
	Store
	now func() time.Time

	mu     sync.Mutex
	limits map[int64]*bucket
}

func New(st Store, now func() time.Time) *Service {
	return &Service{Store: st, now: now}
}

// A project's error requests: a bucket of ErrorBurst refilled at errorRate
// a second.
const (
	errorRate  = 20
	ErrorBurst = 200
)

type bucket struct {
	tokens float64
	at     time.Time
}

// Allow takes a token of project's bucket.
func (s *Service) Allow(project int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.limits == nil {
		s.limits = map[int64]*bucket{}
	}
	b := s.limits[project]
	if b == nil {
		b = &bucket{tokens: ErrorBurst, at: now}
		s.limits[project] = b
	}
	b.tokens = min(ErrorBurst, b.tokens+now.Sub(b.at).Seconds()*errorRate)
	b.at = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RunPrune deletes what is older than keep, now and every hour, until ctx
// is done.
func (s *Service) RunPrune(ctx context.Context, keep time.Duration, log *slog.Logger) {
	for t := time.NewTicker(time.Hour); ; {
		if err := s.Prune(ctx, keep); err != nil && ctx.Err() == nil {
			log.Error("prune", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
