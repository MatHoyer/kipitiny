// Package sqlitehelper is the manager binary's `sqlite` command: the client
// the data browser runs, over exec, in a helper container of the manager's
// own image that mounts an app's volumes. Most images have no sqlite3, so
// the manager brings its own (modernc.org/sqlite, already in the binary).
//
// It reads one Request as JSON on stdin and writes one Response as JSON on
// stdout (CSV for an export). The manager builds the SQL; this side only
// runs it, bounded: rows, cells and bytes capped, a timeout, read-only
// unless asked, and never on a file outside the volume it was given.
package sqlitehelper

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Header starts every SQLite database file.
const Header = "SQLite format 3\x00"

// Ops a Request runs.
const (
	OpFind  = "find"  // list the SQLite files under Roots
	OpQuery = "query" // run SQL, return the last statement's rows
	OpCSV   = "csv"   // run one SELECT, stream its rows as CSV
	OpApply = "apply" // run Statements in one transaction
)

type Request struct {
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
	Statements []Statement `json:"statements,omitempty"`
	TimeoutMS  int         `json:"timeoutMs,omitempty"`
}

type Statement struct {
	SQL string `json:"sql"`
	One bool   `json:"one,omitempty"`
}

type Response struct {
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
	Changes int64  `json:"changes,omitempty"`
	Files   []File `json:"files,omitempty"`
}

// File is a SQLite database found by a find, its path absolute.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Limits of a find: a volume of uploads shouldn't take long to scan.
const (
	findDepth   = 8
	findVisited = 50_000
	findMax     = 200
)

// Main is the command: request on stdin, response on stdout. "idle" waits
// for a stop signal, as the helper container's own process.
func Main(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 1 && args[0] == "idle" {
		// Not select{}: with no goroutine left, the runtime calls that a
		// deadlock and exits.
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
		<-stop
		return 0
	}
	var req Request
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return reply(stdout, Response{Error: "request: " + err.Error()})
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if req.Op == OpFind {
		files, err := find(ctx, req.Roots)
		if err != nil {
			return reply(stdout, Response{Error: err.Error()})
		}
		return reply(stdout, Response{Files: files})
	}
	db, err := open(req)
	if err != nil {
		return reply(stdout, Response{Error: err.Error()})
	}
	defer db.Close()
	switch req.Op {
	case OpQuery:
		res, err := query(ctx, db, req)
		if err != nil {
			return reply(stdout, Response{Error: message(ctx, err)})
		}
		return reply(stdout, res)
	case OpApply:
		failed, err := apply(ctx, db, req.Statements)
		if err != nil {
			return reply(stdout, Response{Error: message(ctx, err), Failed: failed})
		}
		return reply(stdout, Response{})
	case OpCSV:
		// Rows already went out: an error can only go to stderr.
		if err := export(ctx, db, req.SQL, stdout); err != nil {
			fmt.Fprintln(os.Stderr, message(ctx, err))
			return 1
		}
		return 0
	}
	return reply(stdout, Response{Error: fmt.Sprintf("unknown op %q", req.Op)})
}

func reply(w io.Writer, res Response) int {
	if err := json.NewEncoder(w).Encode(res); err != nil {
		return 1
	}
	return 0
}

// message is what the caller reads: SQLite's own words, or the timeout.
func message(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "the query took too long and was stopped"
	}
	msg := err.Error()
	// modernc prefixes "SQL logic error: ", keeps the code in parentheses.
	if i := strings.LastIndex(msg, " ("); i > 0 && strings.HasSuffix(msg, ")") {
		msg = msg[:i]
	}
	if strings.Contains(msg, "too many attached databases") {
		return "ATTACH and VACUUM aren't available here: the data browser opens one file only"
	}
	return strings.TrimPrefix(msg, "SQL logic error: ")
}

// open checks the request's file and opens it as its owner: read-only
// unless Write (SQLite enforces it, ATTACH included), with a busy timeout
// so a write by the app is waited for, not failed on.
func open(req Request) (*sql.DB, error) {
	path, err := Resolve(req.Root, req.File)
	if err != nil {
		return nil, err
	}
	if err := becomeOwner(path); err != nil {
		return nil, err
	}
	mode := "ro"
	if req.Write {
		mode = "rw"
	}
	q := url.Values{}
	q.Set("mode", mode)
	q.Add("_pragma", "busy_timeout(5000)")
	if !req.Write {
		q.Add("_pragma", "query_only(1)")
	}
	db, err := sql.Open("sqlite", "file:"+(&url.URL{Path: path}).EscapedPath()+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// One connection: an apply's transaction and changes() see the same one.
	db.SetMaxOpenConns(1)
	return db, nil
}

// connect takes the connection a run uses, unable to attach a database:
// mode=ro binds the file opened, not others, and ATTACH (VACUUM INTO, which
// attaches its target) would create or change any file of the volumes.
func connect(ctx context.Context, db *sql.DB) (*sql.Conn, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_ATTACHED, 0); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Resolve returns file with symlinks resolved, if it stays inside root and
// is a SQLite database.
func Resolve(root, file string) (string, error) {
	if root == "" || !filepath.IsAbs(file) {
		return "", errors.New("no database file given")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("volume: %w", err)
	}
	p, err := filepath.EvalSymlinks(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", errors.New("no such file")
		}
		return "", err
	}
	if p != root && !strings.HasPrefix(p, root+"/") {
		return "", errors.New("the file leads outside its volume")
	}
	if !isSQLite(p) {
		return "", errors.New("not a SQLite database")
	}
	return p, nil
}

func isSQLite(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return false
	}
	head := make([]byte, len(Header))
	_, err = io.ReadFull(f, head)
	return err == nil && string(head) == Header
}

// becomeOwner switches this process to the uid and gid owning path, so the
// journal, -wal and -shm files SQLite creates beside it are the app's, not
// root's (which would lock the app out of its own database).
func becomeOwner(path string) error {
	if os.Getuid() != 0 {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid == 0 {
		return nil
	}
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(int(sys.Gid)); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(int(sys.Uid)); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	return nil
}

// find walks roots for SQLite files, without following symlinks.
func find(ctx context.Context, roots []string) ([]File, error) {
	files := []File{}
	visited := 0
	for _, root := range roots {
		depth0 := strings.Count(filepath.Clean(root), "/")
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: skipped
			}
			if visited++; visited > findVisited || len(files) == findMax || ctx.Err() != nil {
				return fs.SkipAll
			}
			if d.IsDir() {
				if strings.Count(p, "/")-depth0 >= findDepth {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") || strings.HasSuffix(p, "-journal") {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() < 100 || !isSQLite(p) {
				return nil
			}
			files = append(files, File{Path: p, Size: info.Size()})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// query runs req.SQL (one or more statements; the last one's rows are
// returned), in one transaction when writing. Every row is read, so a
// write's statement runs to the end, but only req.Rows are kept.
func query(ctx context.Context, db *sql.DB, req Request) (Response, error) {
	conn, err := connect(ctx, db)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if req.Write {
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			return Response{}, err
		}
		defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") //nolint:errcheck // no-op after COMMIT
	}
	res, err := readRows(ctx, conn, req)
	if err != nil {
		return Response{}, err
	}
	if res.Columns == nil {
		if err := conn.QueryRowContext(ctx, "SELECT changes()").Scan(&res.Changes); err != nil {
			return Response{}, err
		}
	}
	if req.Write {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return Response{}, err
		}
	}
	return res, nil
}

func readRows(ctx context.Context, conn *sql.Conn, req Request) (Response, error) {
	rows, err := conn.QueryContext(ctx, req.SQL)
	if err != nil {
		return Response{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return Response{}, err
	}
	res := Response{}
	if len(cols) > 0 {
		res.Columns, res.Rows, res.Truncated = cols, [][]*string{}, [][2]int{}
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	size := 0
	for rows.Next() {
		if len(cols) == 0 {
			continue
		}
		if (req.Rows > 0 && len(res.Rows) == req.Rows) || (req.Bytes > 0 && size > req.Bytes) {
			res.More = true
			continue
		}
		if err := rows.Scan(ptrs...); err != nil {
			return Response{}, err
		}
		row := make([]*string, len(cols))
		for j, v := range vals {
			s, ok, cut := Text(v, req.Cell)
			if !ok {
				continue
			}
			if cut {
				res.Truncated = append(res.Truncated, [2]int{len(res.Rows), j})
			}
			size += len(s)
			row[j] = &s
		}
		res.Rows = append(res.Rows, row)
	}
	return res, rows.Err()
}

// Text is how a cell reads: blobs as 0x hex, text cut at max bytes (0: not
// cut) without splitting a character. ok is false for NULL.
func Text(v any, max int) (s string, ok, cut bool) {
	switch v := v.(type) {
	case nil:
		return "", false, false
	case []byte:
		if max > 0 && 2+2*len(v) > max {
			return "0x" + hex.EncodeToString(v[:(max-2)/2]), true, true
		}
		return "0x" + hex.EncodeToString(v), true, false
	case string:
		s = v
	case int64:
		s = strconv.FormatInt(v, 10)
	case float64:
		s = strconv.FormatFloat(v, 'g', -1, 64)
	case bool:
		s = strconv.FormatBool(v)
	case time.Time:
		// shortcut: the driver parses DATE/DATETIME/TIMESTAMP columns read
		// directly (the console's), so this is a reformat, not the stored
		// text; the browser's own queries read text and never get here.
		s = v.Format("2006-01-02 15:04:05.999999999-07:00")
	default:
		s = fmt.Sprint(v)
	}
	if max > 0 && len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut], true, true
	}
	return s, true, false
}

// apply runs statements in one transaction; failed is the 1-based one that
// didn't find its row (the transaction is rolled back).
func apply(ctx context.Context, db *sql.DB, stmts []Statement) (failed int, err error) {
	conn, err := connect(ctx, db)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return 0, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK") //nolint:errcheck // no-op after COMMIT
	for i, s := range stmts {
		r, err := conn.ExecContext(ctx, s.SQL)
		if err != nil {
			return 0, err
		}
		if s.One {
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				return i + 1, errors.New("row changed")
			}
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return 0, err
}

// export streams a query's rows as CSV with a header; NULL is empty.
func export(ctx context.Context, db *sql.DB, query string, w io.Writer) error {
	conn, err := connect(ctx, db)
	if err != nil {
		return err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(cols); err != nil {
		return err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	record := make([]string, len(cols))
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		for i, v := range vals {
			record[i], _, _ = Text(v, 0)
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}
