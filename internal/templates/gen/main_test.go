package main

import (
	"bytes"
	"os"
	"testing"
)

// The demo's catalog matches the templates.
func TestDemoCatalogUpToDate(t *testing.T) {
	want, err := build()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../../web/src/demo/catalog.json")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("web/src/demo/catalog.json is stale: run go generate ./internal/templates (%v)", err)
	}
}
