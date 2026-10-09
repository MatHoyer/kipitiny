package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/MatHoyer/kipitiny/internal/docker"
	"github.com/MatHoyer/kipitiny/internal/store"
)

type RedisKey struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	// TTL is in milliseconds; -1 when the key never expires.
	TTL   int64 `json:"ttl"`
	Bytes int64 `json:"bytes"`
}

type RedisKeys struct {
	Keys []RedisKey `json:"keys"`
	// Cursor continues the scan; "0" when it is complete.
	Cursor string `json:"cursor"`
}

type RedisValue struct {
	Type string `json:"type"`
	// Length is the string's bytes or the collection's members.
	Length int64 `json:"length"`
	TTL    int64 `json:"ttl"`
	// Items depend on the type: [value] for a string, [field, value] for a
	// hash, [index, value] for a list, [member] for a set, [member, score]
	// for a sorted set, [id, field, value, ...] for a stream.
	Items [][]string `json:"items"`
	// Truncated lists the [item, position] strings cut at DataCellMax.
	Truncated [][2]int `json:"truncated"`
	// Cursor fetches the next page; empty after the last one.
	Cursor string `json:"cursor"`
}

// The scripts run with redis.pcall-style protection: they always return
// {"ok": result} or {"err": message} as JSON, since redis-cli exits 0 on a
// server error. They only read.
const redisScriptWrap = `local ok, res = pcall(function() %s end)
if not ok then
  if type(res) == 'table' and res.err then res = res.err end
  return cjson.encode({err = tostring(res)})
end
return cjson.encode({ok = res})`

// ARGV: cursor, pattern, count.
const redisScanScript = `
local r = redis.call('SCAN', ARGV[1], 'MATCH', ARGV[2], 'COUNT', ARGV[3])
local keys = {}
for i, k in ipairs(r[2]) do
  keys[i] = {k, redis.call('TYPE', k)['ok'], redis.call('PTTL', k), redis.call('MEMORY', 'USAGE', k) or -1}
end
return {r[1], keys}`

// KEYS: key. ARGV: cursor (offset, scan cursor or stream id), count, max
// string bytes. Returns {type, length, ttl, items, next cursor}.
const redisValueScript = `
local k, cur, n = KEYS[1], ARGV[1], tonumber(ARGV[2])
local t = redis.call('TYPE', k)['ok']
local ttl = redis.call('PTTL', k)
if t == 'none' then return {t} end
if t == 'string' then
  return {t, redis.call('STRLEN', k), ttl, {{redis.call('GETRANGE', k, 0, tonumber(ARGV[3]))}}, ''}
end
local items, next = {}, ''
if t == 'hash' or t == 'set' then
  local cmd = t == 'hash' and 'HSCAN' or 'SSCAN'
  local r = redis.call(cmd, k, cur, 'COUNT', n)
  local step = t == 'hash' and 2 or 1
  for i = 1, #r[2], step do
    items[#items + 1] = step == 2 and {r[2][i], r[2][i + 1]} or {r[2][i]}
  end
  if r[1] ~= '0' then next = r[1] end
  return {t, redis.call(t == 'hash' and 'HLEN' or 'SCARD', k), ttl, items, next}
end
if t == 'list' or t == 'zset' then
  local off = tonumber(cur)
  local len = redis.call(t == 'list' and 'LLEN' or 'ZCARD', k)
  if t == 'list' then
    for i, v in ipairs(redis.call('LRANGE', k, off, off + n - 1)) do items[i] = {tostring(off + i - 1), v} end
  else
    local r = redis.call('ZRANGE', k, off, off + n - 1, 'WITHSCORES')
    for i = 1, #r, 2 do items[#items + 1] = {r[i], r[i + 1]} end
  end
  if off + n < len then next = tostring(off + n) end
  return {t, len, ttl, items, next}
end
if t == 'stream' then
  local start = cur == '0' and '-' or '(' .. cur
  local r = redis.call('XRANGE', k, start, '+', 'COUNT', n)
  for i, e in ipairs(r) do
    local item = {e[1]}
    for _, v in ipairs(e[2]) do item[#item + 1] = v end
    items[i] = item
  end
  if #r == n then next = r[#r][1] end
  return {t, redis.call('XLEN', k), ttl, items, next}
end
return {t, 0, ttl, items, ''}`

// redisCursorRe matches a scan cursor, a list offset or a stream entry ID.
var redisCursorRe = regexp.MustCompile(`^[0-9]+(-[0-9]+)?$`)

// RedisScan returns one SCAN step over the keys matching pattern (glob).
// A step may return fewer keys than count, or none, before the cursor ends.
func (c *Core) RedisScan(ctx context.Context, id, cursor, pattern string, count int) (RedisKeys, error) {
	if err := Require(ctx, store.ScopeRead); err != nil {
		return RedisKeys{}, err
	}
	t, err := c.dataTarget(ctx, id, store.ServiceKindRedis)
	if err != nil {
		return RedisKeys{}, err
	}
	if cursor == "" {
		cursor = "0"
	}
	if pattern == "" {
		pattern = "*"
	}
	if count <= 0 || count > DataPageMax {
		count = DataPageMax
	}
	var out []json.RawMessage
	if err := t.redisEval(ctx, redisScanScript, nil, []string{cursor, pattern, strconv.Itoa(count)}, &out); err != nil {
		return RedisKeys{}, err
	}
	res := RedisKeys{Keys: []RedisKey{}}
	var keys [][]json.RawMessage
	if len(out) != 2 || json.Unmarshal(out[0], &res.Cursor) != nil || unmarshalList(out[1], &keys) != nil {
		return RedisKeys{}, fmt.Errorf("redis-cli output: unexpected scan reply")
	}
	for _, row := range keys {
		var k RedisKey
		if err := unmarshalRow(row, &k.Key, &k.Type, &k.TTL, &k.Bytes); err != nil {
			return RedisKeys{}, err
		}
		res.Keys = append(res.Keys, k)
	}
	return res, nil
}

// RedisGet returns one page of a key's value.
func (c *Core) RedisGet(ctx context.Context, id, key, cursor string, count int) (RedisValue, error) {
	if err := Require(ctx, store.ScopeRead); err != nil {
		return RedisValue{}, err
	}
	t, err := c.dataTarget(ctx, id, store.ServiceKindRedis)
	if err != nil {
		return RedisValue{}, err
	}
	if cursor == "" {
		cursor = "0"
	}
	if !redisCursorRe.MatchString(cursor) {
		return RedisValue{}, fmt.Errorf("%w: invalid cursor %q", ErrInvalid, cursor)
	}
	if count <= 0 || count > DataPageMax {
		count = DataPageMax
	}
	var out []json.RawMessage
	args := []string{cursor, strconv.Itoa(count), strconv.Itoa(DataCellMax)}
	if err := t.redisEval(ctx, redisValueScript, []string{key}, args, &out); err != nil {
		return RedisValue{}, err
	}
	var v RedisValue
	if len(out) == 0 || json.Unmarshal(out[0], &v.Type) != nil {
		return RedisValue{}, fmt.Errorf("redis-cli output: unexpected value reply")
	}
	if v.Type == "none" {
		return RedisValue{}, fmt.Errorf("key %q: %w", key, store.ErrNotFound)
	}
	var items json.RawMessage
	if err := unmarshalRow(out, &v.Type, &v.Length, &v.TTL, &items, &v.Cursor); err != nil {
		return RedisValue{}, err
	}
	if err := unmarshalList(items, &v.Items); err != nil {
		return RedisValue{}, fmt.Errorf("redis-cli output: %w", err)
	}
	v.Truncated = [][2]int{}
	for i, item := range v.Items {
		for j, s := range item {
			if cut, ok := truncate(s); ok {
				item[j] = cut
				v.Truncated = append(v.Truncated, [2]int{i, j})
			}
		}
	}
	return v, nil
}

// redisEval runs a script (wrapped by redisScriptWrap) with redis-cli and
// decodes its result into dst.
func (t dataTarget) redisEval(ctx context.Context, script string, keys, args []string, dst any) error {
	ctx, cancel := context.WithTimeout(ctx, dataBrowseTimeout)
	defer cancel()
	cmd := append([]string{"redis-cli", "--raw", "EVAL", fmt.Sprintf(redisScriptWrap, script), strconv.Itoa(len(keys))}, keys...)
	var out bytes.Buffer
	err := t.dk.Exec(ctx, t.container, docker.ExecOptions{Cmd: append(cmd, args...), Stdout: &limitedBuffer{buf: &out, max: dataLineMax}})
	if err != nil {
		return clientError(err)
	}
	var reply struct {
		OK  json.RawMessage `json:"ok"`
		Err string          `json:"err"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &reply); err != nil {
		// Not the script's JSON: redis-cli's own error (auth, loading...).
		return fmt.Errorf("%w: %s", ErrInvalid, strings.TrimSpace(out.String()))
	}
	if reply.Err != "" {
		return fmt.Errorf("%w: %s", ErrInvalid, reply.Err)
	}
	return unmarshalList(reply.OK, dst)
}

// unmarshalList decodes a Lua array; cjson encodes an empty one as {}.
func unmarshalList(raw json.RawMessage, dst any) error {
	if string(bytes.TrimSpace(raw)) == "{}" {
		raw = json.RawMessage("[]")
	}
	return json.Unmarshal(raw, dst)
}

// limitedBuffer fails writes past max bytes.
type limitedBuffer struct {
	buf *bytes.Buffer
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.buf.Len()+len(p) > l.max {
		return 0, fmt.Errorf("output exceeds %d bytes", l.max)
	}
	return l.buf.Write(p)
}
