// Package profile implements profile CRUD on top of ccsync.yaml + state.json.
// Switching an active profile triggers a pre-switch backup (Layer 1 safety).
package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/snapshot"
	"github.com/colinc86/ccsync/internal/state"
)

// Create adds a new profile to cfg (persisting ccsync.yaml) and returns the
// updated config. If name already exists, returns an error.
func Create(cfg *config.Config, cfgPath, name, description string) error {
	if name == "" {
		return errors.New("profile name required")
	}
	if _, exists := cfg.Profiles[name]; exists {
		return fmt.Errorf("profile %q already exists", name)
	}
	cfg.Profiles[name] = config.ProfileSpec{Description: description}
	return cfg.SaveWithBackup(cfgPath)
}

// Delete removes a profile from cfg. Rejects deleting the active
// profile, the last profile, or a profile that has descendants
// (anyone else's `extends` points at it). Pre-v0.6.9 the descendants
// check was missing — deleting "default" when "work" extended it
// silently orphaned work, and the next sync failed with "extends
// unknown profile 'default'" without any hint about which profile
// configuration needed fixing.
func Delete(cfg *config.Config, cfgPath, name string, activeProfile string) error {
	if name == activeProfile {
		return errors.New("cannot delete the active profile; switch first")
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return fmt.Errorf("no such profile: %q", name)
	}
	if len(cfg.Profiles) <= 1 {
		return errors.New("cannot delete the last profile")
	}
	var descendants []string
	for other, spec := range cfg.Profiles {
		if other != name && spec.Extends == name {
			descendants = append(descendants, other)
		}
	}
	if len(descendants) > 0 {
		sort.Strings(descendants)
		return fmt.Errorf("cannot delete %q: profile %s extends it; delete or reparent %s first",
			name, quoteJoin(descendants), pluralizeProfile(len(descendants)))
	}
	delete(cfg.Profiles, name)
	return cfg.SaveWithBackup(cfgPath)
}

// quoteJoin formats a list of profile names as a comma-separated
// quoted string for error messages: ["work"] → `"work"`, ["work",
// "personal"] → `"work", "personal"`.
func quoteJoin(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(parts, ", ")
}

func pluralizeProfile(n int) string {
	if n == 1 {
		return "that profile"
	}
	return "those profiles"
}

// Switch changes state.ActiveProfile to target. Before switching, it takes a
// backup of tracked files (~/.claude + ~/.claude.json) so the user can revert.
// absPaths are the files to snapshot (provided by the caller; usually the
// full discovered file set for the current profile).
func Switch(st *state.State, stateDir, target string, absPaths []string) (snapshot.Meta, error) {
	if target == "" {
		return snapshot.Meta{}, errors.New("target profile required")
	}
	if target == st.ActiveProfile {
		return snapshot.Meta{}, nil
	}
	backupRoot := filepath.Join(stateDir, "backups")
	op := "profile-" + sanitize(st.ActiveProfile) + "-to-" + sanitize(target)
	m, err := snapshot.Take(backupRoot, op, st.ActiveProfile, absPaths)
	if err != nil {
		return snapshot.Meta{}, err
	}
	st.ActiveProfile = target
	if err := state.Save(stateDir, st); err != nil {
		return snapshot.Meta{}, err
	}
	_ = time.Now() // reserved for future audit logging
	return m, nil
}

// RestoreBackup copies the backup at id (under stateDir/backups/) back to
// original locations. Useful as a one-step "undo my last profile switch".
func RestoreBackup(stateDir, id string) error {
	if _, err := os.Stat(filepath.Join(stateDir, "backups", id)); err != nil {
		return fmt.Errorf("backup not found: %w", err)
	}
	return snapshot.Restore(filepath.Join(stateDir, "backups"), id)
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}
