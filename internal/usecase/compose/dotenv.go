package compose

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// MarshalEnv writes vars as a .env file, sorted, every value quoted so
// docker compose reads it literally.
func MarshalEnv(vars map[string]string) []byte {
	var b bytes.Buffer
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		v := vars[k]
		if !strings.ContainsAny(v, "'\n\r") {
			fmt.Fprintf(&b, "%s='%s'\n", k, v)
			continue
		}
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "$", `\$`)
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, r.Replace(v))
	}
	return b.Bytes()
}

// ParseEnv reads a .env file: KEY=value lines, optionally "export "-
// prefixed, values bare, 'single-quoted' (literal) or "double-quoted"
// (backslash escapes); # starts a comment line.
func ParseEnv(data []byte) (map[string]string, error) {
	vars := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		k = strings.TrimSpace(k)
		if !ok || !soleVarRe.MatchString("$"+k) {
			return nil, fmt.Errorf(".env line %d: expected KEY=value", n)
		}
		v = strings.TrimSpace(v)
		switch {
		case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
			v = v[1 : len(v)-1]
		case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
			v = unescape(v[1 : len(v)-1])
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		vars[k] = v
	}
	return vars, sc.Err()
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
