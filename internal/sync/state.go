package sync

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// hostState is the per-machine sync pointer kept at ~/.ccsync/state.json.
// It records the commit SHA this host last synced to, per profile — the
// authoritative "base" for three-way decisions.
type hostState struct {
	LastSyncedSHA map[string]string `json:"lastSyncedSHA"`
}

func stateFile(stateDir string) string {
	return filepath.Join(stateDir, "state.json")
}

func loadHostState(stateDir string) (*hostState, error) {
	data, err := os.ReadFile(stateFile(stateDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &hostState{LastSyncedSHA: map[string]string{}}, nil
		}
		return nil, err
	}
	var s hostState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.LastSyncedSHA == nil {
		s.LastSyncedSHA = map[string]string{}
	}
	return &s, nil
}

func saveHostState(stateDir string, s *hostState) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := stateFile(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, stateFile(stateDir))
}
