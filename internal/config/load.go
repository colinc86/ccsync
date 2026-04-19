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
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse ccsync.yaml: %w", err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
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
		if err := os.WriteFile(path+".bak", prev, 0o644); err != nil {
			return fmt.Errorf("write backup: %w", err)
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
func RestoreBackup(path string) error {
	bak := path + ".bak"
	data, err := os.ReadFile(bak)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	if _, err := Parse(data); err != nil {
		return fmt.Errorf("backup failed validation: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}
