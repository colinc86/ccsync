package config

import _ "embed"

//go:embed defaults.yaml
var defaultYAML []byte

// DefaultYAML returns the embedded default ccsync.yaml bytes.
func DefaultYAML() []byte { return defaultYAML }
