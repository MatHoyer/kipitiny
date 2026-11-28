package domain

// The JSON protocol between the data browser and the `kipitiny sqlite`
// helper (infrastructure/sqlitehelper): one SQLiteRequest on stdin, one
// SQLiteResponse on stdout (CSV for an export).

// The ops a SQLiteRequest runs.
const (
	SQLiteOpFind  = "find"  // list the SQLite files under Roots
	SQLiteOpQuery = "query" // run SQL, return the last statement's rows
	SQLiteOpCSV   = "csv"   // run one SELECT, stream its rows as CSV
	SQLiteOpApply = "apply" // run Statements in one transaction
)

type SQLiteRequest struct {
	Op string `json:"op"`
	// Root is the volume File must stay in, symlinks resolved; Roots are
	// the volumes a find walks.
	Root  string   `json:"root,omitempty"`
	Roots []string `json:"roots,omitempty"`
	File  string   `json:"file,omitempty"`
	SQL   string   `json:"sql,omitempty"`
	// Write opens the file read-write; otherwise SQLite refuses any write.
	Write bool `json:"write,omitempty"`
	// Rows caps the rows kept (more are read but dropped, so writes run to
	// the end); Cell the bytes of a cell (0: whole); Bytes all the cells.
	Rows  int `json:"rows,omitempty"`
	Cell  int `json:"cell,omitempty"`
	Bytes int `json:"bytes,omitempty"`
	// Statements of an apply; those with One must change exactly one row.
	Statements []SQLiteStatement `json:"statements,omitempty"`
	TimeoutMS  int               `json:"timeoutMs,omitempty"`
}

type SQLiteStatement struct {
	SQL string `json:"sql"`
	One bool   `json:"one,omitempty"`
}

type SQLiteResponse struct {
	// Error is SQLite's (or the helper's) message.
	Error string `json:"error,omitempty"`
	// Failed is the 1-based statement of an apply whose row wasn't there.
	Failed int `json:"failed,omitempty"`
	// Columns is set when the last statement returns rows (even none).
	Columns   []string    `json:"columns,omitempty"`
	Rows      [][]*string `json:"rows,omitempty"`
	Truncated [][2]int    `json:"truncated,omitempty"`
	More      bool        `json:"more,omitempty"`
	// Changes is the rows the last statement changed.
	Changes int64        `json:"changes,omitempty"`
	Files   []SQLiteFile `json:"files,omitempty"`
}

// SQLiteFile is a SQLite database found by a find, its path absolute.
type SQLiteFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}
