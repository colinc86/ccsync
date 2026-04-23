package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Load reads and parses a ccsync.yaml from disk.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// LoadDefault parses the embedded default config.
func LoadDefault() (*Config, error) {
	return Parse(defaultYAML)
}

// Parse decodes YAML bytes into a Config and validates it.
func Parse(data []byte) (*Config, error) {
	if len(data) == 0 {
		return nil, errors.New("ccsync.yaml is empty")
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse ccsync.yaml: %w", err)
	}
	c.normalize()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// normalize cleans up zero-value shapes that yaml would otherwise round-
// trip as clutter — most importantly empty ProfileExclude pointers that
// serialize as `exclude: {}` even with omitempty (yaml's omitempty
// doesn't recurse into non-nil pointers).
func (c *Config) normalize() {
	for name, spec := range c.Profiles {
		if spec.Exclude != nil && len(spec.Exclude.Paths) == 0 {
			spec.Exclude = nil
			c.Profiles[name] = spec
		}
	}
}

func (c *Config) validate() error {
	if len(c.Profiles) == 0 {
		return errors.New("ccsync.yaml must declare at least one profile")
	}
	return nil
}

// SaveWithBackup serializes c to path atomically. If path already exists,
// its prior content is copied to path+".bak" first. Validates the serialized
// form by re-parsing before committing the write.
func (c *Config) SaveWithBackup(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("serialized config failed validation: %w", err)
	}

	if prev, err := os.ReadFile(path); err == nil {
		// Write the backup atomically (tmp → rename), matching every
		// other atomic-write path in the codebase. Pre-v0.6.11 this
		// was a direct WriteFile: a crash or disk-full mid-write
		// left .bak truncated, and RestoreBackup's validation step
		// (which is the last line of defense) would reject the
		// corrupt file — the user would lose their rollback option
		// at the exact moment they needed it.
		bak := path + ".bak"
		bakTmp := bak + ".tmp"
		if err := os.WriteFile(bakTmp, prev, 0o644); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
		if err := os.Rename(bakTmp, bak); err != nil {
			// Renames on Unix are atomic within the same directory,
			// so this almost never fires, but don't leave a
			// half-staged .bak.tmp lying around if it does.
			_ = os.Remove(bakTmp)
			return fmt.Errorf("commit backup: %w", err)
		}
	}

	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RestoreBackup rolls path back to path+".bak" if the backup exists.
// Uses the same tmp+rename pattern as SaveWithBackup — without this,
// a crash or disk-full mid-write leaves the live ccsync.yaml truncated
// (same class of bug as the pre-v0.6.11 .bak-write race, just
// mirrored). Validates the backup content before any disk write so a
// corrupt .bak can't clobber a valid live file.
func RestoreBackup(path string) error {
	bak := path + ".bak"
	data, err := os.ReadFile(bak)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("backup failed validation: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit restore: %w", err)
	}
	return nil
}
