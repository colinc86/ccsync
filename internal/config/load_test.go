package config

import "testing"

func TestLoadDefault(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if _, ok := c.Profiles["default"]; !ok {
		t.Fatal("default profile missing from embedded defaults")
	}
	if c.DefaultSyncignore == "" {
		t.Fatal("defaultSyncignore is empty")
	}
	// Post-v0.9.0: jsonFiles is intentionally empty by default. ccsync
	// no longer tracks whole settings files; mcpextract handles the
	// few JSON subtrees that are synced.
	if len(c.JSONFiles) != 0 {
		t.Errorf("default jsonFiles should be empty in v0.9.0; got %d entries", len(c.JSONFiles))
	}
}

func TestParseRejectsEmpty(t *testing.T) {
	if _, err := Parse([]byte("")); err == nil {
		t.Fatal("expected error for empty config")
	}
}

func TestParseMinimal(t *testing.T) {
	y := []byte(`
profiles:
  default:
    description: minimal
`)
	c, err := Parse(y)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.Profiles["default"].Description != "minimal" {
		t.Fatal("description didn't parse")
	}
}
