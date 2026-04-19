package sync

import (
	"github.com/tidwall/sjson"

	"github.com/colinc86/ccsync/internal/merge"
)

// KeyChoice is the user's pick for a single conflicted JSON key.
type KeyChoice int

const (
	KeyPending KeyChoice = iota
	KeyLocal
	KeyRemote
)

// BuildPerKeyResolution starts from the merge engine's best-effort merged
// bytes (which default to local at every conflict) and applies the user's
// per-key choices. Returns the final bytes ready to be passed to
// ApplyResolutions.
//
// Absence is respected: when a choice selects a side that didn't have the
// key, the key is deleted from the output.
func BuildPerKeyResolution(merged []byte, conflicts []merge.Conflict, choices []KeyChoice) ([]byte, error) {
	out := make([]byte, len(merged))
	copy(out, merged)

	for i, c := range conflicts {
		if i >= len(choices) {
			break
		}
		switch choices[i] {
		case KeyPending, KeyLocal:
			if !c.LocalPresent {
				if c.Path != "" {
					if next, err := sjson.DeleteBytes(out, c.Path); err == nil {
						out = next
					}
				}
			} else if c.Path != "" && c.Local != "" {
				next, err := sjson.SetRawBytes(out, c.Path, []byte(c.Local))
				if err != nil {
					return nil, err
				}
				out = next
			}
		case KeyRemote:
			if !c.RemotePresent {
				if c.Path != "" {
					if next, err := sjson.DeleteBytes(out, c.Path); err == nil {
						out = next
					}
				}
			} else if c.Path != "" && c.Remote != "" {
				next, err := sjson.SetRawBytes(out, c.Path, []byte(c.Remote))
				if err != nil {
					return nil, err
				}
				out = next
			}
		}
	}
	return out, nil
}
