---
name: statuscolor
description: Set statusline accent color (red, blue, green, yellow, purple, orange, pink, cyan, default)
user-invocable: true
---

Set the statusline accent color for this session. The color affects the divider lines and sparkline chart.

Supported colors: `red`, `blue`, `green`, `yellow`, `purple`, `orange`, `pink`, `cyan`, `default` (gray).

Extract the color from the user's argument (e.g., `/statuscolor blue` → `blue`). If no argument or invalid color, tell the user the available options.

Run the following command with the chosen color:

```bash
sid=$(cat ~/.claude/session-by-ccpid/$PPID 2>/dev/null)
[ -z "$sid" ] && sid=$(cat ~/.claude/.current-session-id 2>/dev/null)
if [ -n "$sid" ]; then
  echo COLOR > ~/.claude/statusline-color-${sid}
else
  echo COLOR > ~/.claude/statusline-color
fi
```

Replace `COLOR` with the user's chosen color (lowercase).

Tell the user: "Statusline accent color set to COLOR."
