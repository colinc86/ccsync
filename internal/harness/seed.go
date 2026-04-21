package harness

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/colinc86/ccsync/internal/secrets"
)

// WriteClaudeJSON writes the full body to this machine's ~/.claude.json,
// replacing any existing content. Fluent — returns the machine. Body
// may be a formatted or raw JSON string; it's written as-is.
func (m *Machine) WriteClaudeJSON(body string) *Machine {
	m.scenario.t.Helper()
	if err := mkdirAll(filepath.Dir(m.ClaudeJSON)); err != nil {
		m.scenario.t.Fatalf("mkdir for claude.json: %v", err)
	}
	if err := os.WriteFile(m.ClaudeJSON, []byte(body), 0o644); err != nil {
		m.scenario.t.Fatalf("write claude.json: %v", err)
	}
	return m
}

// WriteClaudeJSONKey reads the existing claude.json, sets one key to
// the given value (arbitrary JSON-serializable), and writes it back.
// Useful for mutating a single field without respelling the whole
// document. If the file doesn't exist, it's created with just this
// key. Fluent.
func (m *Machine) WriteClaudeJSONKey(key string, value any) *Machine {
	m.scenario.t.Helper()
	doc := m.ClaudeJSONMap()
	if doc == nil {
		doc = map[string]any{}
	}
	doc[key] = value
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		m.scenario.t.Fatalf("marshal claude.json: %v", err)
	}
	return m.WriteClaudeJSON(string(buf))
}

// WriteClaudeFile writes `content` to $HOME/.claude/<relPath>. Creates
// intermediate directories. Fluent.
func (m *Machine) WriteClaudeFile(relPath, content string) *Machine {
	m.scenario.t.Helper()
	abs := filepath.Join(m.ClaudeDir, relPath)
	if err := mkdirAll(filepath.Dir(abs)); err != nil {
		m.scenario.t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		m.scenario.t.Fatalf("write %s: %v", relPath, err)
	}
	return m
}

// DeleteClaudeFile removes $HOME/.claude/<relPath>. Missing files are
// not an error. Fluent.
func (m *Machine) DeleteClaudeFile(relPath string) *Machine {
	m.scenario.t.Helper()
	_ = os.Remove(filepath.Join(m.ClaudeDir, relPath))
	return m
}

// SetSecret stores a value in the scenario's shared mock keychain under
// the composed key (profile + ":" + path). Used to seed a secret that a
// test expects the restore path to pick up, e.g. when simulating a
// cross-profile restore. Fluent.
func (m *Machine) SetSecret(keyPath, value string) *Machine {
	m.scenario.t.Helper()
	if err := secrets.Store(secrets.Key(m.Profile, keyPath), value); err != nil {
		m.scenario.t.Fatalf("store secret: %v", err)
	}
	return m
}

// ClaudeJSONMap reads and parses this machine's ~/.claude.json. Returns
// nil when the file is absent or unparseable. Tests that want a strict
// read should use ClaudeJSONRaw + their own unmarshal.
func (m *Machine) ClaudeJSONMap() map[string]any {
	m.scenario.t.Helper()
	b, err := os.ReadFile(m.ClaudeJSON)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// ClaudeJSONRaw returns the raw bytes of ~/.claude.json, or empty on
// missing. Tests use this to assert exact content or parse on their
// own.
func (m *Machine) ClaudeJSONRaw() []byte {
	m.scenario.t.Helper()
	b, _ := os.ReadFile(m.ClaudeJSON)
	return b
}

// ReadClaudeFile returns (content, true) when $HOME/.claude/<relPath>
// exists, or ("", false) when it doesn't.
func (m *Machine) ReadClaudeFile(relPath string) (string, bool) {
	m.scenario.t.Helper()
	b, err := os.ReadFile(filepath.Join(m.ClaudeDir, relPath))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// mkdirAll is a tiny wrapper so we don't repeat the 0o755 everywhere.
func mkdirAll(p string) error { return os.MkdirAll(p, 0o755) }
