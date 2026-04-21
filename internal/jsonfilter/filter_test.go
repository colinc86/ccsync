package jsonfilter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/colinc86/ccsync/internal/config"
)

const fixtureClaude = `{
  "autoUpdates": true,
  "theme": "dark",
  "userID": "user-123",
  "numStartups": 42,
  "mcpServers": {
    "gemini": {
      "command": "gemini-mcp",
      "env": {
        "GEMINI_API_KEY": "secret-key-value"
      }
    }
  },
  "projects": {
    "/local/path": {"sessionID": "s1"}
  },
  "cachedGrowthBookFeatures": {
    "foo": {"val": true}
  }
}`

func TestApplyExcludesRedactsIncludes(t *testing.T) {
	rule := config.JSONFileRule{
		Include: []string{"$.autoUpdates", "$.theme", "$.mcpServers"},
		Exclude: []string{"$.projects", "$.cachedGrowthBookFeatures", "$.numStartups", "$.userID"},
		Redact:  []string{"$.mcpServers.*.env.*"},
	}
	res, err := Apply([]byte(fixtureClaude), rule, "default")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(res.Data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"projects", "cachedGrowthBookFeatures", "numStartups", "userID"} {
		if _, ok := parsed[key]; ok {
			t.Errorf("%q should have been excluded", key)
		}
	}
	for _, key := range []string{"autoUpdates", "theme", "mcpServers"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("%q should have been included", key)
		}
	}

	placeholder := "<<REDACTED:ccsync:default:mcpServers.gemini.env.GEMINI_API_KEY>>"
	if !strings.Contains(string(res.Data), placeholder) {
		t.Errorf("output missing redaction placeholder\ngot: %s", res.Data)
	}
	if strings.Contains(string(res.Data), "secret-key-value") {
		t.Error("redacted secret still present in output")
	}

	if len(res.Redactions) != 1 {
		t.Fatalf("expected 1 redaction, got %d: %v", len(res.Redactions), res.Redactions)
	}
	raw, ok := res.Redactions["mcpServers.gemini.env.GEMINI_API_KEY"]
	if !ok {
		t.Fatal("redaction for expected path missing")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("unmarshal redaction: %v", err)
	}
	if value != "secret-key-value" {
		t.Errorf("redaction value = %q", value)
	}
}

func TestRoundTripRestore(t *testing.T) {
	rule := config.JSONFileRule{
		Include: []string{"$"},
		Redact:  []string{"$.mcpServers.*.env.*"},
	}
	applied, err := Apply([]byte(fixtureClaude), rule, "default")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	values := map[string]string{}
	for path, raw := range applied.Redactions {
		values[path] = string(raw)
	}
	restored, err := Restore(applied.Data, values)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(restored.Missing) != 0 {
		t.Errorf("unexpected missing: %v", restored.Missing)
	}
	if !strings.Contains(string(restored.Data), "secret-key-value") {
		t.Errorf("restore didn't re-insert secret\ngot: %s", restored.Data)
	}
	if strings.Contains(string(restored.Data), "<<REDACTED") {
		t.Errorf("restore left a placeholder\ngot: %s", restored.Data)
	}
}

func TestRestoreMissingKey(t *testing.T) {
	rule := config.JSONFileRule{
		Include: []string{"$"},
		Redact:  []string{"$.mcpServers.*.env.*"},
	}
	applied, err := Apply([]byte(fixtureClaude), rule, "default")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	restored, err := Restore(applied.Data, map[string]string{})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(restored.Missing) != 1 {
		t.Errorf("expected 1 missing, got %v", restored.Missing)
	}
}

func TestPreserveLocalExcludes(t *testing.T) {
	t.Run("top-level key preserved", func(t *testing.T) {
		local := []byte(`{"theme":"dark","oauthAccount":{"userId":"home-user"}}`)
		incoming := []byte(`{"theme":"light"}`) // syncable fields; oauthAccount was excluded at push
		out, err := PreserveLocalExcludes(incoming, local, []string{"$.oauthAccount"})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if got["theme"] != "light" {
			t.Errorf("sync'd field not applied: %v", got["theme"])
		}
		oauth, ok := got["oauthAccount"].(map[string]any)
		if !ok {
			t.Fatalf("oauthAccount wiped; got %v", got)
		}
		if oauth["userId"] != "home-user" {
			t.Errorf("oauthAccount.userId lost: %v", oauth)
		}
	})

	t.Run("nested key preserved", func(t *testing.T) {
		local := []byte(`{"theme":"dark","permissions":{"allow":["Bash(x)","Bash(y)"],"deny":["WebFetch(z)"]}}`)
		incoming := []byte(`{"theme":"light","permissions":{}}`) // user syncs theme only; permissions emptied at push
		out, err := PreserveLocalExcludes(incoming, local, []string{"$.permissions.allow", "$.permissions.deny"})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		perms := got["permissions"].(map[string]any)
		allow, _ := perms["allow"].([]any)
		if len(allow) != 2 {
			t.Errorf("allow list lost: %v", allow)
		}
	})

	t.Run("missing local preserves nothing", func(t *testing.T) {
		incoming := []byte(`{"theme":"light"}`)
		out, err := PreserveLocalExcludes(incoming, nil, []string{"$.oauthAccount"})
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(incoming) {
			t.Errorf("empty-local should pass-through; got %s", out)
		}
	})

	t.Run("key in incoming AND local → local wins", func(t *testing.T) {
		local := []byte(`{"userID":"local-id","theme":"dark"}`)
		// Incoming shouldn't have userID (it's excluded) but if it somehow
		// did, the preserve step should still overwrite with local's value.
		incoming := []byte(`{"userID":"remote-id","theme":"light"}`)
		out, err := PreserveLocalExcludes(incoming, local, []string{"$.userID"})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if got["userID"] != "local-id" {
			t.Errorf("local userID should win; got %v", got["userID"])
		}
	})

	t.Run("unparseable local → incoming passes through", func(t *testing.T) {
		incoming := []byte(`{"theme":"light"}`)
		out, err := PreserveLocalExcludes(incoming, []byte(`not json`), []string{"$.oauthAccount"})
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(incoming) {
			t.Errorf("unparseable local should pass-through; got %s", out)
		}
	})

	t.Run("empty excludes list is no-op", func(t *testing.T) {
		local := []byte(`{"oauthAccount":"x"}`)
		incoming := []byte(`{"theme":"light"}`)
		out, err := PreserveLocalExcludes(incoming, local, nil)
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != string(incoming) {
			t.Errorf("no excludes should pass-through")
		}
	})
}

func TestIncludeRoot(t *testing.T) {
	rule := config.JSONFileRule{Include: []string{"$"}}
	res, err := Apply([]byte(`{"a":1,"b":2}`), rule, "default")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := json.Unmarshal(res.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got["a"] != 1 || got["b"] != 2 {
		t.Errorf("root include lost fields: %v", got)
	}
}
