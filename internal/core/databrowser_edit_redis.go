package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// KeyChanges are edits to one Redis key. Items are as RedisValue gives
// them: [value] for a string, [field, value] for a hash, [index, value] for
// a list, [member] for a set, [member, score] for a sorted set, [id, ...]
// for a stream entry.
type KeyChanges struct {
	// Type is the key's type as loaded: the save stops if it changed.
	Type string `json:"type"`
	// Create makes a new key of Type from Inserts: the save stops if the
	// key exists.
	Create bool `json:"create,omitempty"`
	// Delete removes the whole key, and nothing else may change.
	Delete bool `json:"delete,omitempty"`
	// Edits change an item from what was loaded to Set.
	Edits []ItemEdit `json:"edits,omitempty"`
	// Deletes remove items, as loaded.
	Deletes [][]string `json:"deletes,omitempty"`
	// Inserts add items; a list appends them (their index is ignored).
	Inserts [][]string `json:"inserts,omitempty"`
}

type ItemEdit struct {
	Item []string `json:"item"`
	Set  []string `json:"set"`
}

// redisApplyScript checks every value a save touches is still as loaded,
// then writes: both in one script, so no other client runs in between.
// KEYS: the key. ARGV: {checks, writes} as JSON.
const redisApplyScript = `
local p = cjson.decode(ARGV[1])
for _, c in ipairs(p.checks) do
  local got = redis.call(unpack(c.cmd))
  if type(got) == 'table' then got = got['ok'] or #got end
  local ok
  if c.absent then ok = got == false else ok = got ~= false and tostring(got) == c.want end
  if not ok then error(c.msg, 0) end
end
for _, w in ipairs(p.writes) do redis.call(unpack(w)) end
return #p.writes`

// redisCheck expects cmd to reply want, or nil when absent; Msg says why
// the save stopped otherwise. A status reply ("hash" from TYPE) compares by
// its text, an array by its length.
type redisCheck struct {
	Cmd    []string `json:"cmd"`
	Want   string   `json:"want,omitempty"`
	Absent bool     `json:"absent,omitempty"`
	Msg    string   `json:"msg"`
}

// ApplyKeyChanges saves edits to a Redis key, or with apply unset only
// returns the commands it would run. Admin only.
func (c *Core) ApplyKeyChanges(ctx context.Context, id, key string, ch KeyChanges, apply bool) (res ChangeResult, err error) {
	if err := Require(ctx, store.ScopeAdmin); err != nil {
		return ChangeResult{}, err
	}
	if apply {
		defer func() {
			status := http.StatusOK
			if err != nil {
				status = http.StatusBadRequest
			}
			c.Audit(ctx, "data edit", id, status, err)
		}()
	}
	t, err := c.dataTarget(ctx, id, store.ServiceKindRedis)
	if err != nil {
		return ChangeResult{}, err
	}
	checks, writes, err := redisPlan(key, ch)
	if err != nil {
		return ChangeResult{}, err
	}
	res.Statements = make([]string, len(writes))
	for i, w := range writes {
		res.Statements[i] = redisCommandLine(w)
	}
	if !apply {
		return res, nil
	}
	payload, err := json.Marshal(map[string]any{"checks": checks, "writes": writes})
	if err != nil {
		return ChangeResult{}, err
	}
	if len(payload) > mongoChangesMax {
		return ChangeResult{}, fmt.Errorf("%w: these changes are too big to save at once; save them in smaller batches", ErrInvalid)
	}
	var n int
	if err := t.redisEval(ctx, redisApplyScript, []string{key}, []string{string(payload)}, &n); err != nil {
		return ChangeResult{}, err
	}
	res.Applied = true
	return res, nil
}

// redisPlan turns changes into the checks and writes of redisApplyScript.
func redisPlan(key string, ch KeyChanges) ([]redisCheck, [][]string, error) {
	n := len(ch.Edits) + len(ch.Deletes) + len(ch.Inserts)
	switch {
	case ch.Create && (ch.Delete || len(ch.Edits)+len(ch.Deletes) > 0 || len(ch.Inserts) == 0):
		return nil, nil, fmt.Errorf("%w: a new key starts with items only", ErrInvalid)
	case ch.Delete && n > 0:
		return nil, nil, fmt.Errorf("%w: a key being deleted can't change too", ErrInvalid)
	case !ch.Delete && n == 0:
		return nil, nil, fmt.Errorf("%w: no changes to save", ErrInvalid)
	case n > dataChangesMax:
		return nil, nil, fmt.Errorf("%w: %d changes at once is too many; save at most %d", ErrInvalid, n, dataChangesMax)
	}
	if key == "" {
		return nil, nil, fmt.Errorf("%w: the key has no name", ErrInvalid)
	}
	checks := []redisCheck{{Cmd: []string{"TYPE", key}, Want: ch.Type, Msg: rowChanged("the key")}}
	if ch.Create {
		checks[0] = redisCheck{Cmd: []string{"TYPE", key}, Want: "none", Msg: "key " + strconv.Quote(key) + " already exists"}
	}
	if ch.Delete {
		return checks, [][]string{{"DEL", key}}, nil
	}
	width := map[string]int{"string": 1, "hash": 2, "list": 2, "set": 1, "zset": 2}[ch.Type]
	if ch.Type == "stream" {
		if len(ch.Edits)+len(ch.Inserts) > 0 {
			return nil, nil, fmt.Errorf("%w: a stream's entries can only be deleted here: add them from the console", ErrInvalid)
		}
		width = 1
	} else if width == 0 {
		return nil, nil, fmt.Errorf("%w: a %s key can't be edited here", ErrInvalid, ch.Type)
	}
	whole := len(ch.Edits) == 1 && len(ch.Deletes)+len(ch.Inserts) == 0
	if ch.Create {
		whole = len(ch.Inserts) == 1
	}
	if ch.Type == "string" && !whole {
		return nil, nil, fmt.Errorf("%w: a string changes as a whole", ErrInvalid)
	}
	item := func(it []string) error {
		if len(it) < width || (ch.Type != "stream" && len(it) != width) {
			return fmt.Errorf("%w: a %s item has %d parts", ErrInvalid, ch.Type, width)
		}
		for _, s := range it {
			if !utf8.ValidString(s) {
				return fmt.Errorf("%w: a value isn't valid UTF-8", ErrInvalid)
			}
		}
		if ch.Type == "zset" {
			if _, err := strconv.ParseFloat(it[1], 64); err != nil {
				return fmt.Errorf("%w: score %q is not a number", ErrInvalid, it[1])
			}
		}
		return nil
	}
	for _, e := range ch.Edits {
		if err := item(e.Item); err != nil {
			return nil, nil, err
		}
		if err := item(e.Set); err != nil {
			return nil, nil, err
		}
	}
	for _, it := range slices.Concat(ch.Deletes, ch.Inserts) {
		if err := item(it); err != nil {
			return nil, nil, err
		}
	}

	var writes [][]string
	var tail [][]string // a list's removals and appends, after its in-place edits
	// is checks a loaded value is still there; isNew, that a name being
	// added isn't taken.
	is := func(cmd []string, want, label string) {
		checks = append(checks, redisCheck{Cmd: cmd, Want: want, Msg: rowChanged(label)})
	}
	isNew := func(cmd []string, want, label string) {
		checks = append(checks, redisCheck{Cmd: cmd, Want: want, Absent: want == "", Msg: label + " already exists"})
	}
	q := strconv.Quote
	switch ch.Type {
	case "string":
		if ch.Create {
			writes = append(writes, []string{"SET", key, ch.Inserts[0][0]})
			break
		}
		e := ch.Edits[0]
		is([]string{"GET", key}, e.Item[0], "the value")
		writes = append(writes, []string{"SET", key, e.Set[0], "KEEPTTL"})
	case "hash":
		for _, e := range ch.Edits {
			if e.Set[0] != e.Item[0] {
				return nil, nil, fmt.Errorf("%w: a field can't be renamed: add the new one and delete the old one", ErrInvalid)
			}
			is([]string{"HGET", key, e.Item[0]}, e.Item[1], "field "+q(e.Item[0]))
			writes = append(writes, []string{"HSET", key, e.Item[0], e.Set[1]})
		}
		for _, it := range ch.Deletes {
			is([]string{"HGET", key, it[0]}, it[1], "field "+q(it[0]))
			writes = append(writes, []string{"HDEL", key, it[0]})
		}
		for _, it := range ch.Inserts {
			isNew([]string{"HEXISTS", key, it[0]}, "0", "field "+q(it[0]))
			writes = append(writes, []string{"HSET", key, it[0], it[1]})
		}
	case "list":
		// Removing by index: mark the items, then remove the marks, so the
		// other indexes don't shift in between.
		mark := "__kipitiny_deleted_" + randomHex(8)
		for _, e := range ch.Edits {
			if e.Set[0] != e.Item[0] {
				return nil, nil, fmt.Errorf("%w: a list item can't move: delete it and append it", ErrInvalid)
			}
			is([]string{"LINDEX", key, e.Item[0]}, e.Item[1], "item "+e.Item[0])
			writes = append(writes, []string{"LSET", key, e.Item[0], e.Set[1]})
		}
		for _, it := range ch.Deletes {
			is([]string{"LINDEX", key, it[0]}, it[1], "item "+it[0])
			writes = append(writes, []string{"LSET", key, it[0], mark})
		}
		if len(ch.Deletes) > 0 {
			tail = append(tail, []string{"LREM", key, "0", mark})
		}
		for _, it := range ch.Inserts {
			tail = append(tail, []string{"RPUSH", key, it[1]})
		}
	case "set":
		for _, e := range ch.Edits {
			is([]string{"SISMEMBER", key, e.Item[0]}, "1", "member "+q(e.Item[0]))
			isNew([]string{"SISMEMBER", key, e.Set[0]}, "0", "member "+q(e.Set[0]))
			writes = append(writes, []string{"SREM", key, e.Item[0]}, []string{"SADD", key, e.Set[0]})
		}
		for _, it := range ch.Deletes {
			is([]string{"SISMEMBER", key, it[0]}, "1", "member "+q(it[0]))
			writes = append(writes, []string{"SREM", key, it[0]})
		}
		for _, it := range ch.Inserts {
			isNew([]string{"SISMEMBER", key, it[0]}, "0", "member "+q(it[0]))
			writes = append(writes, []string{"SADD", key, it[0]})
		}
	case "zset":
		for _, e := range ch.Edits {
			is([]string{"ZSCORE", key, e.Item[0]}, e.Item[1], "member "+q(e.Item[0]))
			if e.Set[0] == e.Item[0] {
				writes = append(writes, []string{"ZADD", key, "XX", e.Set[1], e.Set[0]})
				continue
			}
			isNew([]string{"ZSCORE", key, e.Set[0]}, "", "member "+q(e.Set[0]))
			writes = append(writes, []string{"ZREM", key, e.Item[0]}, []string{"ZADD", key, e.Set[1], e.Set[0]})
		}
		for _, it := range ch.Deletes {
			is([]string{"ZSCORE", key, it[0]}, it[1], "member "+q(it[0]))
			writes = append(writes, []string{"ZREM", key, it[0]})
		}
		for _, it := range ch.Inserts {
			isNew([]string{"ZSCORE", key, it[0]}, "", "member "+q(it[0]))
			writes = append(writes, []string{"ZADD", key, it[1], it[0]})
		}
	case "stream":
		for _, it := range ch.Deletes {
			if !redisCursorRe.MatchString(it[0]) {
				return nil, nil, fmt.Errorf("%w: invalid stream entry id %q", ErrInvalid, it[0])
			}
			is([]string{"XRANGE", key, it[0], it[0]}, "1", "entry "+it[0])
			writes = append(writes, []string{"XDEL", key, it[0]})
		}
	}
	return checks, append(writes, tail...), nil
}

// redisCommandLine writes a command as typed in redis-cli: arguments with
// spaces, quotes or odd characters in double quotes, escaped.
func redisCommandLine(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		plain := a != ""
		for _, r := range a {
			if r <= ' ' || r == '"' || r == '\'' || r == '\\' || r == 0x7f {
				plain = false
				break
			}
		}
		if plain {
			out[i] = a
			continue
		}
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range a {
			switch {
			case r == '"' || r == '\\':
				b.WriteString(`\` + string(r))
			case r == '\n':
				b.WriteString(`\n`)
			case r == '\r':
				b.WriteString(`\r`)
			case r == '\t':
				b.WriteString(`\t`)
			case r < ' ' || r == 0x7f:
				fmt.Fprintf(&b, `\x%02x`, r)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		out[i] = b.String()
	}
	return strings.Join(out, " ")
}
