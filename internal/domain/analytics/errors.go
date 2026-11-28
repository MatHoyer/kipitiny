package analytics

// ErrorProject is an app with error tracking on: ID is its DSN's project,
// Key the DSN's public key.
type ErrorProject struct {
	ID  int64  `json:"id"`
	Key string `json:"key"`
}

// Issue statuses.
const (
	IssueOpen     = "open"
	IssueResolved = "resolved"
	IssueIgnored  = "ignored"
)

// Issue is the events of one fingerprint.
type Issue struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Culprit      string `json:"culprit"`
	Level        string `json:"level"`
	Platform     string `json:"platform"`
	Status       string `json:"status"`
	FirstSeen    int64  `json:"firstSeen"`
	LastSeen     int64  `json:"lastSeen"`
	Events       int    `json:"events"`
	Users        int    `json:"users"`
	FirstRelease string `json:"firstRelease"`
	LastRelease  string `json:"lastRelease"`
	ResolvedAt   int64  `json:"resolvedAt,omitempty"`
	RegressedAt  int64  `json:"regressedAt,omitempty"`
	// Spark is its events per hour of the last 24, oldest first (lists only).
	Spark []int `json:"spark,omitempty" bun:"-"`
}

// IssueQuery lists a project's issues.
type IssueQuery struct {
	Project int64
	// Status is open, resolved or ignored; all when empty.
	Status string
	// Search matches the title or the culprit.
	Search string
	// Sort is lastSeen (default), firstSeen, events or users.
	Sort   string
	Limit  int
	Offset int
}

// IssueList is a page of issues, and how many each status has.
type IssueList struct {
	Issues []Issue        `json:"issues"`
	Counts map[string]int `json:"counts"`
}

// IssueEvent is one kept event of an issue, and its neighbours: Older and
// Newer are their ids, empty at the ends.
type IssueEvent struct {
	Event ErrorEvent `json:"event"`
	Older string     `json:"older,omitempty"`
	Newer string     `json:"newer,omitempty"`
	// Index counts from the newest (1), of Kept.
	Index int `json:"index"`
	Kept  int `json:"kept"`
}

// ErrorPoint is a bucket of a project's events.
type ErrorPoint struct {
	T int64 `json:"t"`
	N int   `json:"n"`
}

// ErrorChange is an issue that opened (Kind new) or reopened (regression).
type ErrorChange struct {
	Seq     int64  `json:"seq"`
	Project int64  `json:"project"`
	Issue   int64  `json:"issue"`
	Kind    string `json:"kind"`
	At      int64  `json:"at"`
	Title   string `json:"title"`
	Culprit string `json:"culprit"`
	Release string `json:"release"`
}

// ErrorChanges is a page of changes, and the last seq there is.
type ErrorChanges struct {
	Changes []ErrorChange `json:"changes"`
	Last    int64         `json:"last"`
}

// ReleaseErrors is what a release of a project did in a span.
type ReleaseErrors struct {
	Events int `json:"events"`
	// NewIssues first seen in it, in the span.
	NewIssues int `json:"newIssues"`
}

// Recorded tells what an event did to its issue.
type Recorded struct {
	Issue     int64 `json:"issue"`
	New       bool  `json:"new"`
	Regressed bool  `json:"regressed"`
}

// ErrorRecord is an event to add to its issue: Fingerprint is the issue's
// key, Title and Culprit what it shows, User a hash telling the event's
// user apart (no email or address kept).
type ErrorRecord struct {
	Event                       ErrorEvent
	Fingerprint, Title, Culprit string
	User                        string
}

// ErrorEvent is an error as kept: what the UI shows of the SDK's payload.
// Request bodies, headers, cookies, local variables and extras are dropped.
type ErrorEvent struct {
	ID          string `json:"id"`
	At          int64  `json:"at"`
	Level       string `json:"level,omitempty"`
	Platform    string `json:"platform,omitempty"`
	Release     string `json:"release,omitempty"`
	Environment string `json:"environment,omitempty"`
	ServerName  string `json:"serverName,omitempty"`
	Transaction string `json:"transaction,omitempty"`
	Message     string `json:"message,omitempty"`
	// Exceptions as the SDK sorts them: the last one is the one raised.
	Exceptions  []Exception       `json:"exceptions,omitempty"`
	Breadcrumbs []Breadcrumb      `json:"breadcrumbs,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	// Contexts are e.g. browser: "Firefox 147", os: "Linux", runtime: "node v22.1.0".
	Contexts map[string]string `json:"contexts,omitempty"`
	// User is the id (or else username) the app gave the SDK.
	User string `json:"user,omitempty"`
	// URL is the request's, its query keys only.
	URL     string `json:"url,omitempty"`
	Method  string `json:"method,omitempty"`
	Country string `json:"country,omitempty"`
	SDK     string `json:"sdk,omitempty"`
}

type Exception struct {
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	Module    string `json:"module,omitempty"`
	Mechanism string `json:"mechanism,omitempty"`
	Handled   *bool  `json:"handled,omitempty"`
	// Frames oldest first: the last one threw.
	Frames []Frame `json:"frames,omitempty"`
}

type Frame struct {
	Function string `json:"function,omitempty"`
	Module   string `json:"module,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
	InApp    bool   `json:"inApp,omitempty"`
	// Context is the source around Line, which is Context[ContextLine].
	Context     []string `json:"context,omitempty"`
	ContextLine int      `json:"contextLine,omitempty"`
}

type Breadcrumb struct {
	At       float64           `json:"at,omitempty"`
	Type     string            `json:"type,omitempty"`
	Category string            `json:"category,omitempty"`
	Level    string            `json:"level,omitempty"`
	Message  string            `json:"message,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
}
