// Package anonymize turns rules on a database's tables into the script that
// anonymizes a copy of it: SQL for PostgreSQL and MySQL/MariaDB, a mongosh
// script for MongoDB. It is pure: core runs the scripts in a throwaway
// server.
//
// Every generated value is derived from a salted MD5 of the original, so
// the same value always gives the same result (unique keys and joins keep
// working) and nobody without the salt can match it against a list of
// known values. The SQL and JavaScript versions compute the same values as
// Value.
package anonymize

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// Rules say how to anonymize a database, by table: a table name, or
// schema.table outside PostgreSQL's public schema; a collection of the
// service's own database for MongoDB. Tables without a rule are kept as is.
// An empty, non-nil Rules says the database holds nothing personal.
type Rules map[string]Table

// Table is a table's rule: Action (truncate or exclude), else rules by
// column (a field path for MongoDB).
type Table struct {
	Action  string
	Columns map[string]string
}

// Table actions.
const (
	// Truncate keeps the table, empty.
	Truncate = "truncate"
	// Exclude drops the table (CASCADE for PostgreSQL: views on it too).
	Exclude = "exclude"
)

// Column rules: Keep, Null, Hash, FixedPrefix+value, FakePrefix+FakeKinds.
const (
	Keep        = "keep"
	Null        = "null"
	Hash        = "hash"
	FixedPrefix = "fixed:"
	FakePrefix  = "fake:"
)

// FakeKinds are the fake values a column can get.
var FakeKinds = []string{"email", "name", "phone", "text"}

func (t Table) MarshalYAML() (any, error) {
	if t.Action != "" {
		return t.Action, nil
	}
	return t.Columns, nil
}

func (t *Table) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		t.Action = n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: a table's rule is truncate, exclude or rules by column", n.Line)
	}
	t.Columns = map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if v.Kind != yaml.ScalarNode {
			return fmt.Errorf("line %d: %s: a column's rule is a string", v.Line, k.Value)
		}
		t.Columns[k.Value] = v.Value
		if v.Tag == "!!null" { // email: null, unquoted
			t.Columns[k.Value] = Null
		}
	}
	return nil
}

func (t Table) MarshalJSON() ([]byte, error) {
	if t.Action != "" {
		return json.Marshal(t.Action)
	}
	if t.Columns == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(t.Columns)
}

func (t *Table) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte(`"`)) {
		return json.Unmarshal(b, &t.Action)
	}
	return json.Unmarshal(b, &t.Columns)
}

// Validate checks every rule.
func (r Rules) Validate() error {
	for _, name := range slices.Sorted(maps.Keys(r)) {
		t := r[name]
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("a table name is empty")
		}
		if t.Action != "" {
			if t.Action != Truncate && t.Action != Exclude {
				return fmt.Errorf("%s: %q is not truncate, exclude or rules by column", name, t.Action)
			}
			continue
		}
		for _, col := range slices.Sorted(maps.Keys(t.Columns)) {
			if strings.TrimSpace(col) == "" {
				return fmt.Errorf("%s: a column name is empty", name)
			}
			if err := checkRule(t.Columns[col]); err != nil {
				return fmt.Errorf("%s.%s: %w", name, col, err)
			}
		}
	}
	return nil
}

func checkRule(rule string) error {
	switch {
	case rule == Keep, rule == Null, rule == Hash, strings.HasPrefix(rule, FixedPrefix):
		return nil
	case strings.HasPrefix(rule, FakePrefix) && slices.Contains(FakeKinds, strings.TrimPrefix(rule, FakePrefix)):
		return nil
	}
	return fmt.Errorf("%q is not keep, null, hash, fixed:<value> or fake:%s", rule, strings.Join(FakeKinds, "|"))
}

// Hash identifies rules applied with salt: an anonymized copy made with
// other rules (or another salt) is out of date.
func (r Rules) Hash(salt string) string {
	b, _ := json.Marshal(r) // map keys sorted
	h := sha256.Sum256(append(append(b, 0), salt...))
	return hex.EncodeToString(h[:8])
}

// Personal reports whether a column's name looks like it holds personal
// data, for warnings about columns without a rule.
func Personal(column string) bool {
	for _, w := range words(column) {
		if personalWords[w] {
			return true
		}
	}
	return false
}

var personalWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`email mail phone mobile tel name firstname lastname surname fullname username
		address street city zip postal postcode birth birthday birthdate dob ssn iban card token secret password passwd
		ip lat lng latitude longitude`) {
		personalWords[w] = true
	}
}

// words splits snake_case, kebab-case and camelCase names into lowercase
// words.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	for i, c := range s {
		switch {
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			flush()
			continue
		case unicode.IsUpper(c) && i > 0 && len(cur) > 0 && !unicode.IsUpper(cur[len(cur)-1]):
			flush()
		}
		cur = append(cur, c)
	}
	flush()
	return out
}

// Value is what a column rule makes of a non-null value: the reference
// the SQL and JavaScript versions match. ok is false for keep.
func Value(rule, salt, v string) (_ string, ok bool) {
	h := md5Hex(salt + v)
	switch {
	case rule == Keep:
		return "", false
	case rule == Hash:
		return h, true
	case strings.HasPrefix(rule, FixedPrefix):
		return strings.TrimPrefix(rule, FixedPrefix), true
	}
	switch strings.TrimPrefix(rule, FakePrefix) {
	case "email":
		return "u" + h[:12] + "@example.invalid", true
	case "name":
		return firstNames[hexInt(h[0:7])%len(firstNames)] + " " + lastNames[hexInt(h[7:14])%len(lastNames)], true
	case "phone":
		var b strings.Builder
		n, j := 0, 0
		for _, c := range v {
			if c >= '0' && c <= '9' {
				// The first two digits stay: a country or area code keeps
				// the number's shape.
				if n++; n > 2 {
					c = rune('0' + hexInt(h[j%32:j%32+1])%10)
					j++
				}
			}
			b.WriteRune(c)
		}
		return b.String(), true
	case "text":
		n := len([]rune(v))
		return string([]rune(strings.Repeat(lorem, n/len(lorem)+1))[:n]), true
	}
	return "", false // null: no value
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func hexInt(s string) int {
	n, _ := strconv.ParseInt(s, 16, 64)
	return int(n)
}

const lorem = "Lorem ipsum dolor sit amet. "

var (
	firstNames = []string{
		"Alex", "Camille", "Charlie", "Dominique", "Eden", "Elliot", "Gabriel", "Jordan", "Kim", "Lou",
		"Morgan", "Noa", "Robin", "Sacha", "Sam", "Taylor",
	}
	lastNames = []string{
		"Bernard", "Dubois", "Garcia", "Kowalski", "Martin", "Meyer", "Nguyen", "Novak", "Petit", "Rossi",
		"Silva", "Smith", "Jansen", "Weber", "Moreau", "Laurent",
	}
)

// parsed splits a table's column rules into the function each runs
// (null, fixed, hash or a fake kind) and fixed's value, keep left out,
// columns sorted.
func (t Table) parsed() (cols, fns, vals []string) {
	for _, col := range slices.Sorted(maps.Keys(t.Columns)) {
		rule := t.Columns[col]
		fn, val := strings.TrimPrefix(rule, FakePrefix), ""
		switch {
		case rule == Keep:
			continue
		case strings.HasPrefix(rule, FixedPrefix):
			fn, val = "fixed", strings.TrimPrefix(rule, FixedPrefix)
		}
		cols, fns, vals = append(cols, col), append(fns, fn), append(vals, val)
	}
	return cols, fns, vals
}

// actions lists the tables of each action and those with column rules.
func (r Rules) actions() (exclude, truncate, update []string) {
	for _, name := range slices.Sorted(maps.Keys(r)) {
		switch t := r[name]; {
		case t.Action == Exclude:
			exclude = append(exclude, name)
		case t.Action == Truncate:
			truncate = append(truncate, name)
		default:
			if cols, _, _ := t.parsed(); len(cols) > 0 {
				update = append(update, name)
			}
		}
	}
	return exclude, truncate, update
}

// NewSalt makes a project's salt.
func NewSalt() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails
	return hex.EncodeToString(b)
}

var saltRe = regexp.MustCompile(`^[0-9a-f]{16,64}$`)

func checkSalt(salt string) error {
	if !saltRe.MatchString(salt) {
		// It's written into function bodies: only ever a generated hex token.
		return fmt.Errorf("invalid salt")
	}
	return nil
}
