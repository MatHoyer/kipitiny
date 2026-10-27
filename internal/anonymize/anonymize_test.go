package anonymize

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const salt = "00112233445566778899aabbccddeeff"

func TestRulesYAMLAndJSON(t *testing.T) {
	src := `users:
  email: fake:email
  name: fake:name
  password_hash: fixed:$2b$10$abc
  note: null
sessions: truncate
audit: exclude
`
	var r Rules
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		t.Fatal(err)
	}
	if r["sessions"].Action != Truncate || r["audit"].Action != Exclude || r["users"].Columns["password_hash"] != "fixed:$2b$10$abc" ||
		r["users"].Columns["note"] != Null {
		t.Fatalf("parsed %+v", r)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"audit":"exclude","sessions":"truncate","users":{"email":"fake:email","name":"fake:name","note":"null","password_hash":"fixed:$2b$10$abc"}}`
	if string(b) != want {
		t.Fatalf("json %s, want %s", b, want)
	}
	var back Rules
	if err := json.Unmarshal(b, &back); err != nil || back.Hash(salt) != r.Hash(salt) {
		t.Fatalf("json round trip: %v %+v", err, back)
	}
	out, err := yaml.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	var again Rules
	if err := yaml.Unmarshal(out, &again); err != nil || again.Hash(salt) != r.Hash(salt) {
		t.Fatalf("yaml round trip: %v\n%s", err, out)
	}

	// An empty table rule stays an object, an empty Rules too.
	b, _ = json.Marshal(Rules{"t": {}})
	if string(b) != `{"t":{}}` {
		t.Fatalf("empty table: %s", b)
	}
}

func TestValidate(t *testing.T) {
	for name, r := range map[string]Rules{
		"bad action": {"t": {Action: "drop"}},
		"bad rule":   {"t": {Columns: map[string]string{"c": "fake:iban"}}},
		"no column":  {"t": {Columns: map[string]string{"": "null"}}},
		"no table":   {" ": {Action: Truncate}},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := Rules{"t": {Columns: map[string]string{"a": "keep", "b": "null", "c": "hash", "d": "fixed:", "e": "fake:phone"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestHashChangesWithRulesAndSalt(t *testing.T) {
	a := Rules{"t": {Columns: map[string]string{"c": "hash"}}}
	b := Rules{"t": {Columns: map[string]string{"c": "null"}}}
	if a.Hash(salt) == b.Hash(salt) || a.Hash(salt) == a.Hash(salt+"00") {
		t.Fatal("hash ignores rules or salt")
	}
}

func TestValue(t *testing.T) {
	email, _ := Value("fake:email", salt, "michelle@mail.com")
	if !strings.HasPrefix(email, "u") || !strings.HasSuffix(email, "@example.invalid") || len(email) != 29 {
		t.Errorf("email %q", email)
	}
	if again, _ := Value("fake:email", salt, "michelle@mail.com"); again != email {
		t.Error("not stable")
	}
	if other, _ := Value("fake:email", salt, "bob@mail.com"); other == email {
		t.Error("two values collide")
	}
	if other, _ := Value("fake:email", "ff"+salt, "michelle@mail.com"); other == email {
		t.Error("salt ignored")
	}
	phone, _ := Value("fake:phone", salt, "+33 6 01 01 01 01")
	if !strings.HasPrefix(phone, "+33 ") || len(phone) != len("+33 6 01 01 01 01") || phone == "+33 6 01 01 01 01" {
		t.Errorf("phone %q", phone)
	}
	for i, c := range phone {
		if orig := "+33 6 01 01 01 01"[i]; (orig >= '0' && orig <= '9') != (c >= '0' && c <= '9') {
			t.Errorf("phone %q lost its shape", phone)
		}
	}
	if name, _ := Value("fake:name", salt, "michelle"); len(strings.Fields(name)) != 2 {
		t.Errorf("name %q", name)
	}
	if text, _ := Value("fake:text", salt, "héllo wörld"); text != "Lorem ipsum" {
		t.Errorf("text %q", text)
	}
	if h, _ := Value("hash", salt, "x"); len(h) != 32 {
		t.Errorf("hash %q", h)
	}
	if v, _ := Value("fixed:$2b$x", salt, "secret"); v != "$2b$x" {
		t.Errorf("fixed %q", v)
	}
	if _, ok := Value("keep", salt, "x"); ok {
		t.Error("keep changed the value")
	}
	if len(lorem) != 28 {
		t.Error("the scripts divide by lorem's length, 28")
	}
}

func TestPersonal(t *testing.T) {
	for col, want := range map[string]bool{
		"email": true, "contact_email": true, "firstName": true, "phone_number": true, "ip_address": true,
		"id": false, "created_at": false, "status": false, "description": false, "shipped": false,
	} {
		if Personal(col) != want {
			t.Errorf("Personal(%q) = %v", col, !want)
		}
	}
}

func TestScriptsQuoteValues(t *testing.T) {
	r := Rules{
		"users":    {Columns: map[string]string{"it's": "fixed:o'neil\\", "email": "fake:email", "id": "keep"}},
		"sessions": {Action: Truncate},
		"audit":    {Action: Exclude},
	}
	pg, err := Postgres(r, salt)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"SELECT pg_temp.kp_drop(ARRAY['audit']::text[]);",
		"SELECT pg_temp.kp_truncate(ARRAY['sessions']::text[]);",
		"SELECT pg_temp.kp_update('users', ARRAY['email', 'it''s']::text[], ARRAY['email', 'fixed']::text[], ARRAY['', 'o''neil\\']::text[]);",
	} {
		if !strings.Contains(pg, want) {
			t.Errorf("postgres script lacks %s\n%s", want, pg)
		}
	}
	my, err := MySQL(r, salt)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DROP TABLE `audit`;", "TRUNCATE TABLE `sessions`;", "UPDATE `users` SET `email` = kp_anon_email(`email`), `it's` = 'o''neil\\\\';"} {
		if !strings.Contains(my, want) {
			t.Errorf("mysql script lacks %s\n%s", want, my)
		}
	}
	if _, err := Postgres(r, "'; DROP"); err == nil {
		t.Error("a salt that isn't hex reached the script")
	}
	_, env, err := Mongo(r, salt, "app")
	if err != nil || !strings.Contains(strings.Join(env, "\n"), `KP_RULES={"audit":"exclude"`) {
		t.Errorf("mongo env %v %v", env, err)
	}
}
