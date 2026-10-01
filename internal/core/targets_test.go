package core

import (
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/storage"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestDriveConfig(t *testing.T) {
	// Any binary on PATH stands in for rclone: only its presence is checked.
	c := &Core{cfg: config.Config{Rclone: "true"}}

	gd := store.BackupTarget{Kind: store.BackupTargetGoogleDrive}
	if err := c.applyDriveConfig(&gd, map[string]string{"client_id": "id"}); err == nil {
		t.Fatal("missing token accepted")
	}
	if err := c.applyDriveConfig(&gd, map[string]string{"token": `{"access_token":"x"}`}); err != nil {
		t.Fatal(err)
	}
	if gd.Config["scope"] != "drive" {
		t.Fatalf("config = %v", gd.Config)
	}

	// rclone refreshed the token; an unchanged edit (masked secret) keeps it.
	gd.Config["token"] = `{"access_token":"refreshed"}`
	shown := maskedTarget(gd).Settings
	if shown["token"] != SecretMask {
		t.Fatalf("settings = %v", shown)
	}
	if err := c.applyDriveConfig(&gd, shown); err != nil {
		t.Fatal(err)
	}
	if gd.Config["token"] != `{"access_token":"refreshed"}` {
		t.Fatalf("unchanged edit lost the token: %v", gd.Config)
	}
}

func TestProtonLoginRequired(t *testing.T) {
	c := &Core{}
	pd := store.BackupTarget{Kind: store.BackupTargetProtonDrive}
	if err := c.applyProtonLogin(&pd, ""); err == nil {
		t.Fatal("Proton Drive target without a sign-in accepted")
	}
	if err := c.applyProtonLogin(&pd, "unknown"); err == nil {
		t.Fatal("unknown sign-in accepted")
	}
	pd.Config = map[string]string{storage.ProtonSessionFile: "{}", "account": "me@proton.me"}
	if err := c.applyProtonLogin(&pd, ""); err != nil {
		t.Fatalf("edit without signing in again: %v", err)
	}
	if s := maskedTarget(pd).Settings; s["account"] != "me@proton.me" || len(s) != 1 {
		t.Fatalf("settings = %v", s)
	}
}
