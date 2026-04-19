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
	if _, ok := c.JSONFiles["~/.claude.json"]; !ok {
		t.Fatal("~/.claude.json rule missing from embedded defaults")
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
