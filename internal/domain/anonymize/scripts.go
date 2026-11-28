package anonymize

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Postgres is the psql script anonymizing a database, run in one
// transaction (psql -1) by a superuser. Helpers live in pg_temp, so the
// dump made afterwards has none of them. Values are cast back to each
// column's type: a rule that makes no sense for it (hash on an integer)
// fails the whole script.
func Postgres(r Rules, salt string) (string, error) {
	if err := checkSalt(salt); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("SET session_replication_role = replica;\n") // no triggers, no FK checks on UPDATE
	fmt.Fprintf(&b, "CREATE FUNCTION pg_temp.kp_hash(v text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $kp$ SELECT md5('%s' || v) $kp$;\n", salt)
	b.WriteString(pgHelpers)
	fmt.Fprintf(&b, `CREATE FUNCTION pg_temp.kp_name(v text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $kp$
SELECT (%s)[1 + ('x' || substr(h, 1, 7))::bit(28)::int %% %d] || ' ' || (%s)[1 + ('x' || substr(h, 8, 7))::bit(28)::int %% %d]
FROM (SELECT pg_temp.kp_hash(v) AS h) s $kp$;
`, pgArray(firstNames), len(firstNames), pgArray(lastNames), len(lastNames))

	exclude, truncate, update := r.actions()
	if len(exclude) > 0 {
		fmt.Fprintf(&b, "SELECT pg_temp.kp_drop(%s);\n", pgArray(exclude))
	}
	if len(truncate) > 0 {
		fmt.Fprintf(&b, "SELECT pg_temp.kp_truncate(%s);\n", pgArray(truncate))
	}
	for _, name := range update {
		cols, fns, vals := r[name].parsed()
		fmt.Fprintf(&b, "SELECT pg_temp.kp_update(%s, %s, %s, %s);\n",
			pgLiteral(name), pgArray(cols), pgArray(fns), pgArray(vals))
	}
	return b.String(), nil
}

// pgHelpers find tables by name (schema.table, or a table of public) and
// apply rules; the arguments are values, never SQL.
const pgHelpers = `CREATE FUNCTION pg_temp.kp_email(v text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $kp$
SELECT 'u' || left(pg_temp.kp_hash(v), 12) || '@example.invalid' $kp$;
CREATE FUNCTION pg_temp.kp_text(v text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $kp$
SELECT left(repeat('` + lorem + `', length(v) / 28 + 1), length(v)) $kp$;
CREATE FUNCTION pg_temp.kp_phone(v text) RETURNS text LANGUAGE plpgsql IMMUTABLE STRICT AS $kp$
DECLARE h text := pg_temp.kp_hash(v); r text := ''; c text; n int := 0; j int := 0;
BEGIN
  FOREACH c IN ARRAY regexp_split_to_array(v, '') LOOP
    IF c ~ '^[0-9]$' THEN
      n := n + 1;
      IF n > 2 THEN
        c := ((position(substr(h, j % 32 + 1, 1) IN '0123456789abcdef') - 1) % 10)::text;
        j := j + 1;
      END IF;
    END IF;
    r := r || c;
  END LOOP;
  RETURN r;
END $kp$;
CREATE FUNCTION pg_temp.kp_table(name text) RETURNS regclass LANGUAGE plpgsql STABLE AS $kp$
DECLARE t regclass;
BEGIN
  SELECT c.oid INTO t FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE c.relkind IN ('r', 'p') AND (n.nspname || '.' || c.relname = name OR (n.nspname = 'public' AND c.relname = name))
  ORDER BY n.nspname = 'public' DESC LIMIT 1;
  IF t IS NULL THEN RAISE EXCEPTION 'anonymize: no table %', name; END IF;
  RETURN t;
END $kp$;
CREATE FUNCTION pg_temp.kp_drop(tables text[]) RETURNS void LANGUAGE plpgsql AS $kp$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY tables LOOP
    EXECUTE format('DROP TABLE %s CASCADE', pg_temp.kp_table(name));
  END LOOP;
END $kp$;
CREATE FUNCTION pg_temp.kp_truncate(tables text[]) RETURNS void LANGUAGE plpgsql AS $kp$
DECLARE names text[] := '{}'; name text;
BEGIN
  FOREACH name IN ARRAY tables LOOP
    names := names || pg_temp.kp_table(name)::text;
  END LOOP;
  -- One statement: tables referencing each other empty together.
  EXECUTE 'TRUNCATE ' || array_to_string(names, ', ');
END $kp$;
CREATE FUNCTION pg_temp.kp_update(tbl text, cols text[], fns text[], vals text[]) RETURNS void LANGUAGE plpgsql AS $kp$
DECLARE t regclass := pg_temp.kp_table(tbl); sets text[] := '{}'; typ text;
BEGIN
  FOR i IN 1 .. cardinality(cols) LOOP
    typ := NULL;
    SELECT format_type(a.atttypid, a.atttypmod) INTO typ FROM pg_attribute a
    WHERE a.attrelid = t AND a.attname = cols[i] AND a.attnum > 0 AND NOT a.attisdropped;
    IF typ IS NULL THEN RAISE EXCEPTION 'anonymize: no column %.%', tbl, cols[i]; END IF;
    sets := sets || format('%I = (%s)::%s', cols[i], CASE fns[i]
      WHEN 'null' THEN 'NULL'
      WHEN 'fixed' THEN quote_literal(vals[i])
      ELSE format('pg_temp.kp_%s(%I::text)', fns[i], cols[i]) END, typ);
  END LOOP;
  EXECUTE format('UPDATE %s SET %s', t, array_to_string(sets, ', '));
END $kp$;
`

func pgLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func pgArray(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = pgLiteral(s)
	}
	return "ARRAY[" + strings.Join(q, ", ") + "]::text[]"
}

// MySQL is the mysql client script anonymizing the current database, run
// as root. Its helper functions are dropped at the end, so a dump made
// afterwards (with --routines) has none of them. MySQL converts values to
// each column's type itself, failing in strict mode when it can't.
func MySQL(r Rules, salt string) (string, error) {
	if err := checkSalt(salt); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("SET FOREIGN_KEY_CHECKS = 0;\nDELIMITER //\n")
	fmt.Fprintf(&b, "CREATE FUNCTION kp_anon_hash(v LONGTEXT CHARSET utf8mb4) RETURNS CHAR(32) CHARSET utf8mb4 DETERMINISTIC NO SQL RETURN MD5(CONCAT('%s', v))//\n", salt)
	b.WriteString(mysqlHelpers)
	fmt.Fprintf(&b, `CREATE FUNCTION kp_anon_name(v LONGTEXT CHARSET utf8mb4) RETURNS LONGTEXT CHARSET utf8mb4 DETERMINISTIC NO SQL
RETURN CONCAT(ELT(1 + CONV(SUBSTRING(kp_anon_hash(v), 1, 7), 16, 10) MOD %d, %s), ' ', ELT(1 + CONV(SUBSTRING(kp_anon_hash(v), 8, 7), 16, 10) MOD %d, %s))//
DELIMITER ;
`, len(firstNames), mysqlList(firstNames), len(lastNames), mysqlList(lastNames))

	exclude, truncate, update := r.actions()
	for _, name := range exclude {
		fmt.Fprintf(&b, "DROP TABLE %s;\n", mysqlIdent(name))
	}
	for _, name := range truncate {
		fmt.Fprintf(&b, "TRUNCATE TABLE %s;\n", mysqlIdent(name))
	}
	for _, name := range update {
		cols, fns, vals := r[name].parsed()
		sets := make([]string, len(cols))
		for i, col := range cols {
			v := fmt.Sprintf("kp_anon_%s(%s)", fns[i], mysqlIdent(col))
			switch fns[i] {
			case "null":
				v = "NULL"
			case "fixed":
				v = mysqlLiteral(vals[i])
			}
			sets[i] = mysqlIdent(col) + " = " + v
		}
		fmt.Fprintf(&b, "UPDATE %s SET %s;\n", mysqlIdent(name), strings.Join(sets, ", "))
	}
	b.WriteString("DROP FUNCTION kp_anon_name; DROP FUNCTION kp_anon_phone; DROP FUNCTION kp_anon_text; DROP FUNCTION kp_anon_email; DROP FUNCTION kp_anon_hash;\n")
	return b.String(), nil
}

const mysqlHelpers = `CREATE FUNCTION kp_anon_email(v LONGTEXT CHARSET utf8mb4) RETURNS LONGTEXT CHARSET utf8mb4 DETERMINISTIC NO SQL
RETURN CONCAT('u', LEFT(kp_anon_hash(v), 12), '@example.invalid')//
CREATE FUNCTION kp_anon_text(v LONGTEXT CHARSET utf8mb4) RETURNS LONGTEXT CHARSET utf8mb4 DETERMINISTIC NO SQL
RETURN LEFT(REPEAT('` + lorem + `', CHAR_LENGTH(v) DIV 28 + 1), CHAR_LENGTH(v))//
CREATE FUNCTION kp_anon_phone(v LONGTEXT CHARSET utf8mb4) RETURNS LONGTEXT CHARSET utf8mb4 DETERMINISTIC NO SQL
BEGIN
  DECLARE h CHAR(32) CHARSET utf8mb4 DEFAULT kp_anon_hash(v);
  DECLARE r LONGTEXT CHARSET utf8mb4 DEFAULT '';
  DECLARE c VARCHAR(1) CHARSET utf8mb4;
  DECLARE i, n, j INT DEFAULT 0;
  IF v IS NULL THEN RETURN NULL; END IF;
  WHILE i < CHAR_LENGTH(v) DO
    SET i = i + 1, c = SUBSTRING(v, i, 1);
    IF c REGEXP '^[0-9]$' THEN
      SET n = n + 1;
      IF n > 2 THEN
        SET c = CONV(SUBSTRING(h, j MOD 32 + 1, 1), 16, 10) MOD 10, j = j + 1;
      END IF;
    END IF;
    SET r = CONCAT(r, c);
  END WHILE;
  RETURN r;
END//
`

func mysqlIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func mysqlLiteral(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, "'", "''", "\x00", `\0`).Replace(s) + "'"
}

func mysqlList(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = mysqlLiteral(s)
	}
	return strings.Join(q, ", ")
}

// MongoScript anonymizes collections of the database KP_DB with the rules
// in KP_RULES (Rules as JSON) and the salt in KP_SALT: values reach it
// through the environment, never its text. Field paths go into embedded
// documents, not arrays; a document without the field is left alone.
const MongoScript = `const crypto = require("crypto");
const e = process.env, salt = e.KP_SALT, rules = JSON.parse(e.KP_RULES), d = db.getSiblingDB(e.KP_DB);
const first = ` + "%FIRST%" + `, last = ` + "%LAST%" + `;
const hash = (v) => crypto.createHash("md5").update(salt + v).digest("hex");
const fakes = {
  hash,
  email: (v) => "u" + hash(v).slice(0, 12) + "@example.invalid",
  name: (v) => { const h = hash(v); return first[parseInt(h.slice(0, 7), 16) % first.length] + " " + last[parseInt(h.slice(7, 14), 16) % last.length]; },
  phone: (v) => { const h = hash(v); let n = 0, j = 0, r = "";
    for (const c of v) { if (c >= "0" && c <= "9" && ++n > 2) { r += String(parseInt(h[j % 32], 16) % 10); j++; } else r += c; }
    return r; },
  text: (v) => { const n = [...v].length; return [..."` + lorem + `".repeat(Math.floor(n / 28) + 1)].slice(0, n).join(""); },
};
const get = (doc, path) => path.split(".").reduce((o, k) => (o != null && typeof o === "object" && !Array.isArray(o) ? o[k] : undefined), doc);
const str = (v) => (typeof v === "string" ? v : EJSON.stringify(v, { relaxed: true }));
const names = d.getCollectionNames();
for (const [coll, r] of Object.entries(rules)) {
  if (!names.includes(coll)) throw new Error("anonymize: no collection " + coll);
  const c = d.getCollection(coll);
  if (r === "exclude") { c.drop(); continue; }
  if (r === "truncate") { c.deleteMany({}); continue; }
  const cols = Object.entries(r).filter(([, rule]) => rule !== "keep");
  if (!cols.length) continue;
  let ops = [];
  const flush = () => { if (ops.length) c.bulkWrite(ops, { ordered: false }); ops = []; };
  c.find({}, Object.fromEntries(cols.map(([p]) => [p, 1]))).forEach((doc) => {
    const set = {};
    for (const [path, rule] of cols) {
      const v = get(doc, path);
      if (v === undefined) continue;
      if (rule === "null") set[path] = null;
      else if (rule.startsWith("fixed:")) set[path] = rule.slice(6);
      else if (v !== null) set[path] = fakes[rule.replace(/^fake:/, "")](str(v));
    }
    if (Object.keys(set).length) ops.push({ updateOne: { filter: { _id: doc._id }, update: { $set: set } } });
    if (ops.length >= 1000) flush();
  });
  flush();
}`

// Mongo is MongoScript with the fake names in, and its environment.
func Mongo(r Rules, salt, database string) (script string, env []string, err error) {
	if err := checkSalt(salt); err != nil {
		return "", nil, err
	}
	rules, err := json.Marshal(r)
	if err != nil {
		return "", nil, err
	}
	f, _ := json.Marshal(firstNames)
	l, _ := json.Marshal(lastNames)
	script = strings.NewReplacer("%FIRST%", string(f), "%LAST%", string(l)).Replace(MongoScript)
	return script, []string{"KP_SALT=" + salt, "KP_RULES=" + string(rules), "KP_DB=" + database}, nil
}
