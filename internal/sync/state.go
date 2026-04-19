package sync

import "github.com/colinc86/ccsync/internal/state"

// loadHostState and saveHostState are local aliases over internal/state so the
// orchestrator doesn't import the shared package in two places.
func loadHostState(stateDir string) (*state.State, error) { return state.Load(stateDir) }
func saveHostState(stateDir string, s *state.State) error  { return state.Save(stateDir, s) }
