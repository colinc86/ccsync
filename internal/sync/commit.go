package sync

import (
	"fmt"
	"sort"
	"strings"
)

// commitMessage renders the "sync(<profile>): <host> +N ~M -K" shape with
// per-file bullets (truncated).
func commitMessage(profile, host string, plan Plan) string {
	added, modified, deleted := plan.Summary()
	var buf strings.Builder
	fmt.Fprintf(&buf, "sync(%s): %s +%d ~%d -%d\n", profile, host, added, modified, deleted)

	paths := make([]string, 0, len(plan.Actions))
	byPath := map[string]FileAction{}
	for _, a := range plan.Actions {
		if a.Action == 0 {
			continue
		}
		paths = append(paths, a.Path)
		byPath[a.Path] = a
	}
	sort.Strings(paths)

	limit := 20
	if len(paths) > limit {
		paths = paths[:limit]
	}
	if len(paths) > 0 {
		buf.WriteString("\n")
	}
	for _, p := range paths {
		fmt.Fprintf(&buf, "  %s %s\n", actionSymbol(byPath[p]), p)
	}
	return buf.String()
}

func actionSymbol(a FileAction) string {
	switch a.Action.String() {
	case "AddLocal", "AddRemote":
		return "+"
	case "DeleteLocal", "DeleteRemote":
		return "-"
	case "Pull", "Push", "Merge":
		return "~"
	}
	return "·"
}
