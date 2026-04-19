// Package profile implements profile CRUD on top of ccsync.yaml + state.json.
// Switching an active profile triggers a pre-switch backup (Layer 1 safety).
package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// Delete removes a profile from cfg. Rejects deleting the active or last profile.
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
	delete(cfg.Profiles, name)
	return cfg.SaveWithBackup(cfgPath)
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
