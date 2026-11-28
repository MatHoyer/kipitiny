package compose

import (
	"slices"
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func TestDependsOnRestart(t *testing.T) {
	f, warns, err := Parse([]byte(`
services:
  web:
    image: a
    depends_on:
      migrate: {condition: service_completed_successfully}
      api: {condition: service_healthy, required: true}
  api:
    image: a
    depends_on: [db]
    restart: on-failure:3
  migrate:
    image: a
    restart: "no"
  db:
    image: postgres:16
    restart: always
`), Vars{})
	if err != nil {
		t.Fatal(err)
	}
	web := f.Services["web"]
	if !slices.Equal(web.DependsOn, []domain.Dependency{{Service: "api", Condition: "healthy"}, {Service: "migrate", Condition: "completed"}}) {
		t.Errorf("web depends_on = %v", web.DependsOn)
	}
	if f.Services["migrate"].Runtime.Restart != "no" || f.Services["api"].Runtime.Restart != "on-failure:3" || f.Services["db"].Runtime != nil {
		t.Errorf("restart: %+v %+v", f.Services["migrate"].Runtime, f.Services["api"].Runtime)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "required is ignored") {
		t.Errorf("warns = %v", warns)
	}
	data, err := Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"restart: \"no\"", "depends_on:\n      - db", "condition: service_completed_successfully", "restart: on-failure:3"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("marshal lacks %q:\n%s", want, data)
		}
	}
	back, _, err := Parse(data, Vars{})
	if err != nil || !slices.Equal(back.Services["web"].DependsOn, web.DependsOn) {
		t.Errorf("round trip: %v %v", err, back.Services["web"].DependsOn)
	}

	for in, want := range map[string]string{
		"services: {a: {image: a, depends_on: [b]}}":                                   "depends_on b: no such service",
		"services: {a: {image: a, depends_on: [b]}, b: {image: a, depends_on: [a]}}":   "depends_on cycle: a → b → a",
		"services: {a: {image: a, depends_on: {b: {condition: nope}}}, b: {image: a}}": "condition nope is not supported",
		"services: {a: {image: a, restart: sometimes}}":                                "restart sometimes is not supported",
	} {
		if _, _, err := Parse([]byte(in), Vars{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", in, err, want)
		}
	}
}
