package compose

import (
	"slices"
	"strings"
	"testing"
)

func TestNetworks(t *testing.T) {
	f, warns, err := Parse([]byte(`
services:
  proxy:
    image: a
    networks: [front, shared]
  api:
    image: a
    networks:
      front:
      back: {aliases: [api.internal]}
  db:
    image: postgres:16
    networks: [back]
  plain:
    image: a
networks:
  front:
  back: {internal: true}
  shared: {external: true, name: kipitiny-shared}
`), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if s := f.Services["proxy"]; !slices.Equal(s.Networks, []string{"front"}) || !slices.Equal(s.ExternalNetworks, []string{"kipitiny-shared"}) {
		t.Errorf("proxy = %v %v", s.Networks, s.ExternalNetworks)
	}
	if s := f.Services["api"]; !slices.Equal(s.Networks, []string{"back", "front"}) || s.ExternalNetworks != nil {
		t.Errorf("api = %v %v", s.Networks, s.ExternalNetworks)
	}
	if s := f.Services["db"]; !slices.Equal(s.Networks, []string{"back"}) {
		t.Errorf("db = %v", s.Networks)
	}
	if s := f.Services["plain"]; s.Networks != nil {
		t.Errorf("plain = %v", s.Networks)
	}
	if w := strings.Join(warns, "\n"); !strings.Contains(w, "internal is ignored") || !strings.Contains(w, "aliases") {
		t.Errorf("warns = %v", warns)
	}
	data, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := Parse(data, Vars{})
	if err != nil {
		t.Fatal(err)
	}
	if s := back.Services["proxy"]; !slices.Equal(s.Networks, []string{"front"}) || !slices.Equal(s.ExternalNetworks, []string{"kipitiny-shared"}) {
		t.Errorf("round trip proxy = %v %v\n%s", s.Networks, s.ExternalNetworks, data)
	}
	if back.Services["plain"].Networks != nil {
		t.Errorf("round trip plain = %v", back.Services["plain"].Networks)
	}
	for in, want := range map[string]string{
		"services: {a: {image: a, networks: [nope]}}":                               "network nope is not defined",
		"services: {a: {image: a, network_mode: host, networks: [default]}}":        "host network can't join",
		"services: {a: {image: a}}\nnetworks: {default: {external: true, name: x}}": "default network can't be external",
	} {
		if _, _, err := Parse([]byte(in), Vars{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
}
