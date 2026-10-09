package core

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

const (
	dataConsoleTimeout = 30 * time.Second
	// dataQueryMax caps a console query (it is passed as one argument).
	dataQueryMax = 64 << 10
	// dataOutputMax caps the console output read.
	dataOutputMax = 1 << 20
	// pgNull marks NULL in psql's CSV output, where it is otherwise empty
	// like an empty string.
	pgNull = "␀"
)

// ConsoleResult is a console run's answer: rows for a postgres query that
// returns some, otherwise Output (a command tag such as "UPDATE 3", or
// redis-cli's reply).
type ConsoleResult struct {
	Columns []string    `json:"columns,omitempty"`
	Rows    [][]*string `json:"rows,omitempty"`
	// Truncated lists the [row, column] cells cut at DataCellMax.
	Truncated [][2]int `json:"truncated,omitempty"`
	// More is set when rows or output past the caps were dropped.
	More   bool   `json:"more,omitempty"`
	Output string `json:"output,omitempty"`
}

// pgTagRe matches a command tag psql prints for a statement without rows.
var pgTagRe = regexp.MustCompile(`^[A-Z]+( [A-Z]+)*( [0-9]+){0,2}$`)

// DataConsole runs an admin's query: SQL on postgres, a command on redis.
// Unless write is set, the session is read-only (postgres) or commands that
// write are refused (redis). Every run is audited, without its text, which
// may hold secrets.
func (c *Core) DataConsole(ctx context.Context, id, query string, write bool) (res ConsoleResult, err error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return ConsoleResult{}, err
	}
	action := "data console"
	if write {
		action += " (write)"
	}
	defer func() {
		status := http.StatusOK
		if err != nil {
			status = http.StatusBadRequest
		}
		c.Audit(ctx, action, id, status, err)
	}()
	if strings.TrimSpace(query) == "" {
		return ConsoleResult{}, fmt.Errorf("%w: empty query", ErrInvalid)
	}
	if len(query) > dataQueryMax {
		return ConsoleResult{}, fmt.Errorf("%w: query longer than %d bytes", ErrInvalid, dataQueryMax)
	}
	svc, err := c.store.GetService(ctx, id)
	if err != nil {
		return ConsoleResult{}, err
	}
	t, err := c.dataTarget(ctx, id, svc.Kind)
	if err != nil {
		return ConsoleResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout+5*time.Second)
	defer cancel()
	switch svc.Kind {
	case store.ServiceKindPostgres:
		return t.pgConsole(ctx, query, write)
	case store.ServiceKindRedis:
		return t.redisConsole(ctx, query, write)
	}
	return ConsoleResult{}, fmt.Errorf("%w: %s is not a database", ErrInvalid, svc.Name)
}

// pgConsole runs sql as one psql command, so only the last statement's
// result shows and write mode runs the whole of it in one transaction.
// Read-only mode is a guard against mistakes, not a security boundary: the
// admin can switch to write mode anyway.
func (t dataTarget) pgConsole(ctx context.Context, sql string, write bool) (ConsoleResult, error) {
	cmd := []string{
		"psql", "-X", "--csv", "-v", "ON_ERROR_STOP=1", "-v", "SHOW_ALL_RESULTS=off", "-P", "null=" + pgNull,
		"-U", t.svc.Env[pgUser], "-d", t.svc.Env[pgDatabase],
	}
	opts := fmt.Sprintf("-c statement_timeout=%ds", int(dataConsoleTimeout.Seconds()))
	if write {
		cmd = append(cmd, "--single-transaction")
	} else {
		opts = "-c default_transaction_read_only=on " + opts
	}
	var res ConsoleResult
	var records [][]string
	err := t.execStream(ctx, docker.ExecOptions{Cmd: append(cmd, "-c", sql), Env: []string{"PGOPTIONS=" + opts}}, func(r io.Reader) error {
		lr := &io.LimitedReader{R: r, N: dataOutputMax}
		cr := csv.NewReader(lr)
		cr.FieldsPerRecord, cr.LazyQuotes = -1, true
		for len(records) <= DataPageMax { // header + DataPageMax rows
			rec, err := cr.Read()
			switch {
			case lr.N <= 0: // cut, maybe mid-record
				res.More = true
				return dropRest(r, write)
			case errors.Is(err, io.EOF):
				return nil
			case err != nil:
				return fmt.Errorf("psql output: %w", err)
			}
			records = append(records, rec)
		}
		if _, err := cr.Read(); errors.Is(err, io.EOF) {
			return nil
		}
		res.More = true
		return dropRest(r, write)
	})
	if err != nil {
		return ConsoleResult{}, err
	}
	switch {
	case len(records) == 0:
		res.Output = "OK"
	case len(records) == 1 && len(records[0]) == 1 && pgTagRe.MatchString(records[0][0]):
		res.Output = records[0][0]
	default:
		res.Columns, res.Rows, res.Truncated = records[0], [][]*string{}, [][2]int{}
		for i, rec := range records[1:] {
			row := make([]*string, len(rec))
			for j, cell := range rec {
				if cell == pgNull {
					continue
				}
				s, cut := truncate(cell)
				if cut {
					res.Truncated = append(res.Truncated, [2]int{i, j})
				}
				row[j] = &s
			}
			res.Rows = append(res.Rows, row)
		}
	}
	return res, nil
}

// dropRest ends a read past the caps. A write must run to its end: stopping
// psql mid-output would roll the transaction back, so the rest is drained.
func dropRest(r io.Reader, write bool) error {
	if !write {
		return errStopLines
	}
	_, err := io.Copy(io.Discard, r)
	return err
}

// redisDenied are refused even in write mode: they block, stream forever,
// or take the server down.
var redisDenied = map[string]bool{
	"MONITOR": true, "SUBSCRIBE": true, "PSUBSCRIBE": true, "SSUBSCRIBE": true, "SYNC": true, "PSYNC": true,
	"SHUTDOWN": true, "DEBUG": true, "REPLICAOF": true, "SLAVEOF": true, "FAILOVER": true, "WAIT": true, "WAITAOF": true,
	"BLPOP": true, "BRPOP": true, "BRPOPLPUSH": true, "BLMOVE": true, "BLMPOP": true,
	"BZPOPMIN": true, "BZPOPMAX": true, "BZMPOP": true,
}

// KEYS: none. ARGV: command names to look up ("config|get" for a
// subcommand). Returns the flags of the first one Redis knows.
const redisFlagsScript = `
for _, name in ipairs(ARGV) do
  local info = redis.call('COMMAND', 'INFO', name)[1]
  if info then
    local flags = {}
    for i, f in ipairs(info[3]) do flags[i] = f['ok'] end
    return flags
  end
end
return {}`

func (t dataTarget) redisConsole(ctx context.Context, line string, write bool) (ConsoleResult, error) {
	args, err := splitRedisArgs(line)
	if err != nil {
		return ConsoleResult{}, err
	}
	if len(args) == 0 {
		return ConsoleResult{}, fmt.Errorf("%w: empty command", ErrInvalid)
	}
	name := strings.ToUpper(args[0])
	if redisDenied[name] || (strings.HasPrefix(name, "XREAD") && slices.ContainsFunc(args, func(a string) bool { return strings.EqualFold(a, "BLOCK") })) {
		return ConsoleResult{}, fmt.Errorf("%w: %s blocks or never returns; use redis-cli in the terminal", ErrInvalid, name)
	}
	if !write {
		names := []string{strings.ToLower(name)}
		if len(args) > 1 {
			names = append([]string{strings.ToLower(name + "|" + args[1])}, names...)
		}
		var flags []string
		if err := t.redisEval(ctx, redisFlagsScript, nil, names, &flags); err != nil {
			return ConsoleResult{}, err
		}
		if slices.Contains(flags, "write") || slices.Contains(flags, "admin") {
			return ConsoleResult{}, fmt.Errorf("%w: %s changes data or the server; allow writes to run it", ErrInvalid, name)
		}
	}
	out := &capWriter{max: dataOutputMax}
	err = t.dk.Exec(ctx, t.container, docker.ExecOptions{Cmd: append([]string{"redis-cli", "--no-raw"}, args...), Stdout: out})
	if err != nil {
		return ConsoleResult{}, clientError(err)
	}
	text := strings.TrimRight(out.b.String(), "\n")
	if msg, ok := strings.CutPrefix(text, "(error) "); ok {
		return ConsoleResult{}, fmt.Errorf("%w: %s", ErrInvalid, msg)
	}
	return ConsoleResult{Output: text, More: out.dropped}, nil
}

// splitRedisArgs splits a command line like redis-cli does: spaces separate
// arguments, "double quotes" take \n \t \" \\ and \xHH escapes, 'single
// quotes' only \'.
func splitRedisArgs(line string) ([]string, error) {
	var args []string
	i := 0
	for {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t' || line[i] == '\n' || line[i] == '\r') {
			i++
		}
		if i == len(line) {
			return args, nil
		}
		var b strings.Builder
		var quote byte
	arg:
		for ; i < len(line); i++ {
			ch := line[i]
			switch {
			case quote == 0 && (ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'):
				break arg
			case quote == 0 && (ch == '"' || ch == '\''):
				quote = ch
			case ch == quote:
				quote = 0
				if i+1 < len(line) && line[i+1] != ' ' && line[i+1] != '\t' {
					return nil, fmt.Errorf("%w: closing quote must be followed by a space", ErrInvalid)
				}
			case quote == '"' && ch == '\\' && i+1 < len(line):
				i++
				switch e := line[i]; e {
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				case 't':
					b.WriteByte('\t')
				case 'x':
					if i+2 >= len(line) {
						return nil, fmt.Errorf("%w: incomplete \\x escape", ErrInvalid)
					}
					v, err := strconv.ParseUint(line[i+1:i+3], 16, 8)
					if err != nil {
						return nil, fmt.Errorf("%w: invalid \\x escape", ErrInvalid)
					}
					b.WriteByte(byte(v))
					i += 2
				default:
					b.WriteByte(e)
				}
			case quote == '\'' && ch == '\\' && i+1 < len(line) && line[i+1] == '\'':
				i++
				b.WriteByte('\'')
			default:
				b.WriteByte(ch)
			}
		}
		if quote != 0 {
			return nil, fmt.Errorf("%w: unbalanced quotes", ErrInvalid)
		}
		args = append(args, b.String())
	}
}

// capWriter keeps the first max bytes and drops the rest.
type capWriter struct {
	b       strings.Builder
	max     int
	dropped bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.b.Len(); len(p) > room {
		w.b.Write(p[:max(room, 0)])
		w.dropped = true
		return len(p), nil
	}
	return w.b.Write(p)
}
