package config

import (
	"fmt"
	"strings"
)

// ResolvedProfile is the effective ruleset for a profile after walking its
// extends chain. Every field is a union across the chain; excludes are
// additive (more-specific profiles cannot re-include something a parent
// excluded — use a separate profile if you need that).
type ResolvedProfile struct {
	Name        string
	Description string
	HostClasses []string
	Chain       []string // extends chain, closest-to-leaf first: ["work", "default"]
	PathExcludes []string
}

// HasExcludes reports whether the resolved profile filters anything out.
func (r *ResolvedProfile) HasExcludes() bool {
	return r != nil && len(r.PathExcludes) > 0
}

// ExcludeRules returns the path-exclude patterns joined as gitignore-style
// lines, ready to feed into ignore.New.
func (r *ResolvedProfile) ExcludeRules() string {
	if r == nil {
		return ""
	}
	return strings.Join(r.PathExcludes, "\n")
}

// EffectiveProfile resolves `name` by walking its `extends` chain. It
// detects cycles and missing parents. The result is ordered so leaf-specific
// metadata (description, hostClasses) wins over parent metadata.
func EffectiveProfile(cfg *Config, name string) (*ResolvedProfile, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	spec, ok := cfg.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q", name)
	}

	out := &ResolvedProfile{Name: name, Description: spec.Description}
	visited := map[string]bool{}
	cur := name
	curSpec := spec
	for {
		if visited[cur] {
			return nil, fmt.Errorf("profile extends cycle detected at %q", cur)
		}
		visited[cur] = true
		out.Chain = append(out.Chain, cur)

		if len(curSpec.HostClasses) > 0 && len(out.HostClasses) == 0 {
			out.HostClasses = append(out.HostClasses, curSpec.HostClasses...)
		}
		if curSpec.Exclude != nil {
			out.PathExcludes = append(out.PathExcludes, curSpec.Exclude.Paths...)
		}

		parent := curSpec.Extends
		if parent == "" {
			break
		}
		parentSpec, ok := cfg.Profiles[parent]
		if !ok {
			return nil, fmt.Errorf("profile %q extends unknown profile %q", cur, parent)
		}
		cur = parent
		curSpec = parentSpec
	}
	return out, nil
}
