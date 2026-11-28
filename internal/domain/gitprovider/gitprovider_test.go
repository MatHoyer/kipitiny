package gitprovider

import (
	"encoding/json"
	"testing"
)

func TestManifest(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(Manifest(ManifestInput{Name: "k", WebhookURL: "https://k.example.com/h"})), &m); err != nil {
		t.Fatal(err)
	}
	perms, _ := m["default_permissions"].(map[string]any)
	if perms["contents"] != "read" || m["public"] != false || m["hook_attributes"] == nil {
		t.Errorf("manifest = %v", m)
	}
	var noHook map[string]any
	if err := json.Unmarshal([]byte(Manifest(ManifestInput{Name: "k"})), &noHook); err != nil {
		t.Fatal(err)
	}
	if noHook["hook_attributes"] != nil || noHook["default_events"] != nil {
		t.Errorf("manifest without webhook = %v", noHook)
	}
}
