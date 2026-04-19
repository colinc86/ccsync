package config

// Config is the ccsync.yaml schema.
type Config struct {
	Profiles          map[string]ProfileSpec  `yaml:"profiles"`
	JSONFiles         map[string]JSONFileRule `yaml:"jsonFiles"`
	DefaultSyncignore string                  `yaml:"defaultSyncignore"`
}

type ProfileSpec struct {
	Description  string `yaml:"description,omitempty"`
	InheritsFrom string `yaml:"inheritsFrom,omitempty"`
}

// JSONFileRule holds filter/redact rules for a single JSON file.
// Path expressions use JSONPath syntax ($.foo.bar, $..key, etc).
type JSONFileRule struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
	Redact  []string `yaml:"redact"`
}
