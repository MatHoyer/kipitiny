package data

import (
	"strings"
	"testing"

	"github.com/kipit-labs/kipitiny/internal/domain"
)

func pgService() domain.Service {
	return domain.Service{
		ID: "01DB", ProjectID: "P1", Name: "db", Kind: domain.ServiceKindPostgres,
		Image: DefaultPostgresImage, Replicas: 1, MemoryMB: 512, Env: NewPostgresEnv(),
	}
}

func TestDatabaseURLEscapes(t *testing.T) {
	db := pgService()
	db.Env[PGPassword] = "p@ss/w:rd"
	got := DatabaseURL(db)
	if got != "postgres://app:p%40ss%2Fw%3Ard@db:5432/postgres" {
		t.Errorf("DatabaseURL = %q", got)
	}
}

func TestParseVerifyOutput(t *testing.T) {
	d, err := parseVerifyOutput("3|100000|8421376\n")
	if err != nil || d.Tables != 3 || d.Rows != 100000 || d.DBBytes != 8421376 {
		t.Fatalf("got %+v %v", d, err)
	}
	if _, err := parseVerifyOutput("ERROR"); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestPostgresMajor(t *testing.T) {
	for image, want := range map[string]int{
		"postgres:17-alpine":     17,
		"postgres:16.4":          16,
		"postgres:18":            18,
		"postgis/postgis:16-3.4": 16,
		"postgres":               0, // implicit latest
		"postgres:latest":        0,
		"postgres@sha256:" + strings.Repeat("0", 64): 0,
		"ghcr.io/org/pg:15-bookworm":                 15,
	} {
		if got := postgresMajor(image); got != want {
			t.Errorf("postgresMajor(%q) = %d, want %d", image, got, want)
		}
	}
}
