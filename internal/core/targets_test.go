package core

import (
	"context"
	"os"
	"testing"

	"github.com/MatHoyer/kipitiny/internal/config"
	"github.com/MatHoyer/kipitiny/internal/store"
)

func TestDriveConfig(t *testing.T) {
	bin := os.Getenv("KIPITINY_TEST_RCLONE")
	if bin == "" {
		t.Skip("KIPITINY_TEST_RCLONE not set (rclone obscures the password)")
	}
	ctx := context.Background()
	c := &Core{cfg: config.Config{Rclone: bin}}

	tg := store.BackupTarget{Kind: store.BackupTargetProtonDrive}
	if err := c.applyDriveConfig(ctx, &tg, map[string]string{"username": "me@proton.me"}); err == nil {
		t.Fatal("missing password accepted")
	}
	in := map[string]string{"username": "me@proton.me", "password": "pw", "2fa": "123456"}
	if err := c.applyDriveConfig(ctx, &tg, in); err != nil {
		t.Fatal(err)
	}
	if tg.Config["password"] == "" || tg.Config["password"] == "pw" {
		t.Fatalf("password not obscured: %q", tg.Config["password"])
	}

	// After signing in, rclone saved a session and the one-time code is gone
	// (checkTarget); an unchanged edit (masked secret) keeps the session.
	tg.Config["client_uid"] = "session"
	delete(tg.Config, "2fa")
	obscured := tg.Config["password"]
	shown := maskedTarget(tg).Settings
	if shown["password"] != SecretMask || shown["client_uid"] != "" || shown["username"] != "me@proton.me" {
		t.Fatalf("settings = %v", shown)
	}
	if err := c.applyDriveConfig(ctx, &tg, shown); err != nil {
		t.Fatal(err)
	}
	if tg.Config["password"] != obscured || tg.Config["client_uid"] != "session" {
		t.Fatalf("unchanged edit lost state: %v", tg.Config)
	}

	// A new account signs in again.
	shown["username"] = "other@proton.me"
	if err := c.applyDriveConfig(ctx, &tg, shown); err != nil {
		t.Fatal(err)
	}
	if _, ok := tg.Config["client_uid"]; ok {
		t.Fatalf("stale session kept: %v", tg.Config)
	}

	gd := store.BackupTarget{Kind: store.BackupTargetGoogleDrive}
	if err := c.applyDriveConfig(ctx, &gd, map[string]string{"token": `{"access_token":"x"}`}); err != nil {
		t.Fatal(err)
	}
	if gd.Config["scope"] != "drive" || maskedTarget(gd).Settings["token"] != SecretMask {
		t.Fatalf("google drive config = %v", gd.Config)
	}
}
