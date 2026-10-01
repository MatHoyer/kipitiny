package protonpass

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestItemFields(t *testing.T) {
	const raw = `{"id":"I1","share_id":"S1","content":{"title":"API","note":"n",
		"content":{"Login":{"email":"","username":"u","password":"p","totp_uri":""}},
		"extra_fields":[{"name":"token","content":{"Hidden":"x"}}]}}`
	var it listedItem
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, f := range itemFields("Work", it) {
		refs = append(refs, f.Ref)
	}
	want := []string{"pass://Work/API/username", "pass://Work/API/password", "pass://Work/API/token", "pass://Work/API/note"}
	if !slices.Equal(refs, want) {
		t.Errorf("refs = %v, want %v", refs, want)
	}

	const custom = `{"id":"I2","share_id":"S1","content":{"title":"a/b","note":"",
		"content":{"Custom":{"sections":[{"section_name":"Prod","section_fields":[{"name":"key"}]}]}}}}`
	it = listedItem{}
	if err := json.Unmarshal([]byte(custom), &it); err != nil {
		t.Fatal(err)
	}
	// A slash in the title can't be referenced by name: IDs instead.
	if f := itemFields("Work", it); len(f) != 1 || f[0].Ref != "pass://S1/I2/Prod.key" || f[0].Name != "Prod.key" {
		t.Errorf("fields = %v", f)
	}
}
