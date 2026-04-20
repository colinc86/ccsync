package config

// Config is the ccsync.yaml schema.
type Config struct {
	Profiles          map[string]ProfileSpec  `yaml:"profiles"`
	JSONFiles         map[string]JSONFileRule `yaml:"jsonFiles"`
	DefaultSyncignore string                  `yaml:"defaultSyncignore"`
}

// ProfileSpec declares one named profile. Profiles may extend another
// profile and carry their own exclude rules; the effective ruleset is
// resolved by config.EffectiveProfile.
type ProfileSpec struct {
	Description string          `yaml:"description,omitempty"`
	Extends     string          `yaml:"extends,omitempty"`
	HostClasses []string        `yaml:"hostClasses,omitempty"`
	Exclude     *ProfileExclude `yaml:"exclude,omitempty"`
}

// ProfileExclude declares what a profile refuses to sync on this machine.
// Paths use .syncignore/gitignore syntax (relative to the sync-repo tree:
// "claude/agents/foo.md", "claude.json"). Excludes are deny-lists layered on
// top of whatever the profile would otherwise sync.
type ProfileExclude struct {
	Paths []string `yaml:"paths,omitempty"`
}

// JSONFileRule holds filter/redact rules for a single JSON file.
// Path expressions use JSONPath syntax ($.foo.bar, $..key, etc).
type JSONFileRule struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
	Redact  []string `yaml:"redact"`
}
