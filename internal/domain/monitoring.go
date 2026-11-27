package domain

import (
	"context"
	"time"
)

// MonitoringRepository stores uptime checks and their results, and the audit log.
type MonitoringRepository interface {
	ListUptimeChecks(ctx context.Context) ([]UptimeCheck, error)
	GetUptimeCheck(ctx context.Context, serviceID string) (UptimeCheck, error)
	// SaveUptimeCheck creates or replaces a service's check settings,
	// keeping its state.
	SaveUptimeCheck(ctx context.Context, c UptimeCheck) (UptimeCheck, error)
	SetUptimeState(ctx context.Context, serviceID string, down bool, since time.Time) error
	// DeleteUptimeCheck also deletes its results.
	DeleteUptimeCheck(ctx context.Context, serviceID string) error
	// SetUptimeRecent replaces the last results of a service's check.
	SetUptimeRecent(ctx context.Context, serviceID string, recent []UptimeResult) error
	// AddUptimeResult counts one check in the hour of at.
	AddUptimeResult(ctx context.Context, serviceID string, at time.Time, ok bool, latency time.Duration) error
	// ListUptimeHours returns the hours from since on, oldest first.
	ListUptimeHours(ctx context.Context, serviceID string, since time.Time) ([]UptimeHour, error)
	// PruneUptime deletes the hours before before.
	PruneUptime(ctx context.Context, before time.Time) error
	AddAudit(ctx context.Context, e AuditEntry) error
	ListAudit(ctx context.Context, limit int) ([]AuditEntry, error)
	// PruneAudit deletes entries older than before.
	PruneAudit(ctx context.Context, before time.Time) error
}

// UptimeCheck requests a service's public URL on an interval and notifies
// when it goes down and when it recovers.
type UptimeCheck struct {
	ServiceID   string `bun:"service_id,pk" json:"serviceId"`
	Path        string `bun:"path" json:"path"`
	IntervalSec int    `bun:"interval_sec" json:"intervalSec"`
	TimeoutSec  int    `bun:"timeout_sec" json:"timeoutSec"`
	// ExpectedStatus is the status that means up; 0 accepts any below 400.
	ExpectedStatus int  `bun:"expected_status" json:"expectedStatus"`
	Enabled        bool `bun:"enabled" json:"enabled"`
	// Down is the state last notified, since ChangedAt.
	Down      bool       `bun:"down" json:"down"`
	ChangedAt *time.Time `bun:"changed_at" json:"changedAt,omitempty"`
	// Recent are the last results, oldest first.
	Recent    []UptimeResult `bun:"recent" json:"-"`
	CreatedAt time.Time      `bun:"created_at" json:"createdAt"`
}

// UptimeResult is the outcome of one check.
type UptimeResult struct {
	At time.Time `json:"at"`
	OK bool      `json:"ok"`
	// Status is 0 when no response arrived.
	Status    int    `json:"status,omitempty"`
	LatencyMS int64  `json:"latencyMs"`
	Error     string `json:"error,omitempty"`
}

// UptimeHour counts the checks of a service in one hour.
type UptimeHour struct {
	ServiceID string    `bun:"service_id,pk" json:"-"`
	Hour      time.Time `bun:"hour,pk" json:"hour"`
	Checks    int       `bun:"checks" json:"checks"`
	Failures  int       `bun:"failures" json:"failures"`
	// LatencyMS sums the response times of the successful checks.
	LatencyMS int64 `bun:"latency_ms" json:"latencyMs"`
}

type AuditEntry struct {
	ID string `bun:"id,pk" json:"id"`
	// Actor is "user:<name>" or "token:<name>".
	Actor     string    `bun:"actor" json:"actor"`
	Action    string    `bun:"action" json:"action"`
	Target    string    `bun:"target" json:"target"`
	Status    int       `bun:"status" json:"status"`
	Error     string    `bun:"error" json:"error,omitempty"`
	CreatedAt time.Time `bun:"created_at" json:"createdAt"`
}
