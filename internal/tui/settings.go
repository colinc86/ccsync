package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/theme"
	"github.com/colinc86/ccsync/internal/updater"
)

// settingsModel is a cursor-driven list where each row is either a read-only
// display or an editable field. Editing uses a single shared textinput overlay;
// radio-style rows cycle values inline on enter.
type settingsModel struct {
	ctx     *AppContext
	cursor  int
	rows    []settingRow
	editing bool
	input   textinput.Model
	err     error
	message string
}

type settingKind int

const (
	kindDisplay  settingKind = iota // read-only
	kindText                        // free-form string via modal
	kindInt                         // integer via modal
	kindRadio                       // cycle through fixed options on enter
	kindBool                        // toggle on enter
	kindAction                      // enter shells out or launches a subflow
	kindSecret                      // like Text but masked input
)

type settingRow struct {
	label   string
	kind    settingKind
	value   func() string // current rendered value
	apply   func(s string) error // commit a text/int edit; ignored for other kinds
	cycle   func() error         // cycle kind=radio
	toggle  func() error         // flip kind=bool
	run     func(m *settingsModel) tea.Cmd // kind=action
	options []string             // radio options (for hint text only)
}

func newSettings(ctx *AppContext) *settingsModel {
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 48
	m := &settingsModel{ctx: ctx, input: ti}
	m.buildRows()
	return m
}

func (m *settingsModel) Title() string { return "Settings" }
func (m *settingsModel) Init() tea.Cmd { return nil }

// CapturesEscape tells the app router that esc should cancel the active
// edit rather than popping back to Home.
func (m *settingsModel) CapturesEscape() bool { return m.editing }

func (m *settingsModel) buildRows() {
	ctx := m.ctx
	m.rows = []settingRow{
		// --- identity ---
		heading("identity"),
		display("host-uuid", ctx.State.HostUUID),
		{
			label: "author name", kind: kindText,
			value: func() string { return valueOrHost(ctx.State.AuthorName) },
			apply: func(s string) error {
				ctx.State.AuthorName = strings.TrimSpace(s)
				if err := state.Save(ctx.StateDir, ctx.State); err != nil {
					return err
				}
				ctx.HostName = valueOrHost(ctx.State.AuthorName)
				return nil
			},
		},
		{
			label: "author email", kind: kindText,
			value: func() string { return valueOrDefault(ctx.State.AuthorEmail, ctx.HostName+"@ccsync.local") },
			apply: func(s string) error {
				ctx.State.AuthorEmail = strings.TrimSpace(s)
				if err := state.Save(ctx.StateDir, ctx.State); err != nil {
					return err
				}
				if ctx.State.AuthorEmail != "" {
					ctx.Email = ctx.State.AuthorEmail
				} else {
					ctx.Email = ctx.HostName + "@ccsync.local"
				}
				return nil
			},
		},
		{
			label: "host class", kind: kindText,
			value: func() string { return valueOr(ctx.State.HostClass, "(none)") },
			apply: func(s string) error {
				ctx.State.HostClass = strings.TrimSpace(s)
				return state.Save(ctx.StateDir, ctx.State)
			},
		},

		// --- sync repo ---
		heading("sync repo"),
		display("url", valueOr(ctx.State.SyncRepoURL, "(not bootstrapped)")),
		{
			label: "auth method", kind: kindRadio,
			options: []string{"ssh", "https"},
			value:   func() string { return currentAuthLabel(ctx.State.Auth) },
			cycle: func() error {
				switch ctx.State.Auth {
				case state.AuthSSH, state.AuthNone:
					ctx.State.Auth = state.AuthHTTPS
				default:
					ctx.State.Auth = state.AuthSSH
				}
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "ssh key path", kind: kindText,
			value: func() string {
				if ctx.State.SSHKeyPath != "" {
					return ctx.State.SSHKeyPath
				}
				if p, err := gitx.DiscoverSSHKey(); err == nil {
					return theme.Hint.Render(p + " (auto)")
				}
				return theme.Warn.Render("(no key found in ~/.ssh)")
			},
			apply: func(s string) error {
				ctx.State.SSHKeyPath = strings.TrimSpace(s)
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "https user", kind: kindText,
			value: func() string { return valueOr(ctx.State.HTTPSUser, theme.Hint.Render("x-access-token (default)")) },
			apply: func(s string) error {
				ctx.State.HTTPSUser = strings.TrimSpace(s)
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "https token", kind: kindSecret,
			value: func() string {
				if _, err := secrets.Fetch("https-token"); err == nil {
					return theme.Good.Render("set (hidden)")
				}
				return theme.Hint.Render("(not set)")
			},
			apply: func(s string) error {
				s = strings.TrimSpace(s)
				if s == "" {
					return secrets.Delete("https-token")
				}
				return secrets.Store("https-token", s)
			},
		},

		// --- behavior ---
		heading("behavior"),
		{
			label: "secrets backend", kind: kindRadio,
			options: []string{"keychain", "file"},
			value:   func() string { return currentSecretsLabel(ctx.State.SecretsBackend) },
			cycle: func() error {
				switch ctx.State.SecretsBackend {
				case state.SecretsBackendDefault, state.SecretsBackendKeychain:
					ctx.State.SecretsBackend = state.SecretsBackendFile
				default:
					ctx.State.SecretsBackend = state.SecretsBackendKeychain
				}
				secrets.SetBackend(string(ctx.State.SecretsBackend))
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "auto-apply clean syncs", kind: kindBool,
			value: func() string { return boolLabel(ctx.State.AutoApplyClean) },
			toggle: func() error {
				ctx.State.AutoApplyClean = !ctx.State.AutoApplyClean
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "background fetch", kind: kindRadio,
			options: []string{"none", "1h", "24h"},
			value:   func() string { return fetchIntervalLabel(ctx.State.FetchInterval) },
			cycle: func() error {
				switch ctx.State.FetchInterval {
				case "":
					ctx.State.FetchInterval = "1h"
				case "1h":
					ctx.State.FetchInterval = "24h"
				default:
					ctx.State.FetchInterval = ""
				}
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "snapshot: keep count", kind: kindInt,
			value: func() string {
				c, _ := ctx.State.SnapshotRetention()
				return fmt.Sprintf("%d", c)
			},
			apply: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil || n <= 0 {
					return fmt.Errorf("expected a positive integer")
				}
				ctx.State.SnapshotMaxCount = n
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "snapshot: keep days", kind: kindInt,
			value: func() string {
				_, d := ctx.State.SnapshotRetention()
				return fmt.Sprintf("%d", d)
			},
			apply: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil || n <= 0 {
					return fmt.Errorf("expected a positive integer")
				}
				ctx.State.SnapshotMaxAgeDays = n
				return state.Save(ctx.StateDir, ctx.State)
			},
		},

		{
			label: "repo encryption", kind: kindAction,
			value: func() string {
				switch detectEncStatus(ctx) {
				case encOn:
					return theme.Good.Render("on")
				case encLocked:
					return theme.Warn.Render("on (locked — needs unlock)")
				default:
					return theme.Hint.Render("off")
				}
			},
			run: func(m *settingsModel) tea.Cmd {
				return switchTo(newEncryptionScreen(m.ctx))
			},
		},

		// --- config files ---
		heading("config files"),
		{
			label: "edit ccsync.yaml", kind: kindAction,
			value: func() string { return theme.Hint.Render("opens $EDITOR") },
			run: func(m *settingsModel) tea.Cmd {
				return editConfigFileCmd(m.ctx.ConfigPath(), true)
			},
		},
		{
			label: "edit .syncignore", kind: kindAction,
			value: func() string { return theme.Hint.Render("opens $EDITOR") },
			run: func(m *settingsModel) tea.Cmd {
				return editConfigFileCmd(filepath.Join(m.ctx.RepoPath, ".syncignore"), false)
			},
		},

		// --- about / maintenance ---
		heading("about"),
		{
			label: "ccsync version", kind: kindDisplay,
			value: func() string {
				v := "v" + updater.CurrentVersion()
				if ctx.UpdateAvailable {
					v += "  " + theme.Warn.Render("· update available: "+ctx.LatestVersion)
				}
				return v
			},
		},
		{
			label: "update mode", kind: kindRadio,
			options: []string{"manual", "auto"},
			value:   func() string { return updateModeLabel(ctx.State.UpdateMode) },
			cycle: func() error {
				switch ctx.State.UpdateMode {
				case "", "manual":
					ctx.State.UpdateMode = "auto"
				default:
					ctx.State.UpdateMode = "manual"
				}
				return state.Save(ctx.StateDir, ctx.State)
			},
		},
		{
			label: "check for updates", kind: kindAction,
			value: func() string { return theme.Hint.Render("opens update checker") },
			run: func(m *settingsModel) tea.Cmd {
				return switchTo(newUpdateScreen(m.ctx))
			},
		},

		// --- paths (display only) ---
		heading("paths"),
		display("~/.claude", ctx.ClaudeDir),
		display("~/.claude.json", ctx.ClaudeJSON),
		display("state dir", ctx.StateDir),

		// --- profiles (display; use Profiles screen to switch) ---
		heading("profiles"),
	}
	var names []string
	for k := range ctx.Config.Profiles {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		p := ctx.Config.Profiles[name]
		active := ""
		if name == ctx.State.ActiveProfile {
			active = theme.Good.Render(" (active)")
		}
		m.rows = append(m.rows, display(name+active, valueOr(p.Description, "(no description)")))
	}

	// Clamp cursor after rebuild and skip past leading headings.
	if m.cursor >= len(m.rows) {
		m.cursor = 0
	}
	for m.cursor < len(m.rows) && m.rows[m.cursor].kind == kindDisplay && isHeading(m.rows[m.cursor]) {
		m.cursor++
	}
}

// --- row helpers ---

func heading(label string) settingRow {
	return settingRow{label: "§ " + label, kind: kindDisplay, value: func() string { return "" }}
}

func display(label, val string) settingRow {
	return settingRow{label: label, kind: kindDisplay, value: func() string { return val }}
}

func isHeading(r settingRow) bool { return strings.HasPrefix(r.label, "§ ") }

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func valueOrDefault(s, fallback string) string {
	if s == "" {
		return theme.Hint.Render(fallback + " (default)")
	}
	return s
}

func valueOrHost(s string) string {
	if s == "" {
		host, _ := os.Hostname()
		return theme.Hint.Render(host + " (default)")
	}
	return s
}

func currentAuthLabel(k state.AuthKind) string {
	if k == "" {
		return theme.Hint.Render("ssh (default)")
	}
	return string(k)
}

func currentSecretsLabel(b state.SecretsBackend) string {
	if b == "" {
		if os.Getenv("CCSYNC_SECRETS_BACKEND") == "file" {
			return theme.Hint.Render("file (from env)")
		}
		return theme.Hint.Render("keychain (default)")
	}
	return string(b)
}

func boolLabel(b bool) string {
	if b {
		return theme.Good.Render("on")
	}
	return "off"
}

func updateModeLabel(s string) string {
	switch s {
	case "auto":
		return theme.Good.Render("auto") + theme.Hint.Render(" — installs new versions silently in the background")
	case "", "manual":
		return "manual" + theme.Hint.Render(" — you trigger each update")
	}
	return s
}

func fetchIntervalLabel(s string) string {
	switch s {
	case "":
		return "none"
	case "1h":
		return "every 1h"
	case "24h":
		return "every 24h"
	}
	return s
}

// rowKindGlyph returns a small leading icon so users can tell what a row
// does without trying enter on it. Display rows get a faded dot; every
// interactive row gets an affordance matching its input style.
func rowKindGlyph(k settingKind) string {
	switch k {
	case kindText:
		return theme.Hint.Render("✎")
	case kindInt:
		return theme.Hint.Render("#")
	case kindSecret:
		return theme.Hint.Render("⎇")
	case kindRadio:
		return theme.Hint.Render("⇄")
	case kindBool:
		return theme.Hint.Render("☐")
	case kindAction:
		return theme.Hint.Render("↗")
	}
	return theme.Subtle.Render("·")
}

// --- editor shell-out ---

type editDoneMsg struct {
	err  error
	path string
}

// editConfigFileCmd suspends the TUI and opens the file in $EDITOR. On
// return, if validate is true, we re-parse ccsync.yaml; a parse error rolls
// the file back to the snapshot we took before editing.
func editConfigFileCmd(path string, validate bool) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() tea.Msg { return editDoneMsg{err: err, path: path} }
	}

	// Snapshot current content to .bak so a bad edit can always be reverted.
	// If the file doesn't exist yet, create an empty one (editor needs a file
	// to open) and skip the backup — an empty reversion isn't useful.
	if prev, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", prev, 0o644)
	} else if os.IsNotExist(err) {
		_ = os.WriteFile(path, []byte{}, 0o644)
	}

	return tea.ExecProcess(exec.Command(editor, path), func(err error) tea.Msg {
		if err != nil {
			return editDoneMsg{err: err, path: path}
		}
		if validate {
			if _, perr := config.Load(path); perr != nil {
				if rerr := config.RestoreBackup(path); rerr == nil {
					return editDoneMsg{
						err:  fmt.Errorf("validation failed: %w (rolled back to previous version)", perr),
						path: path,
					}
				}
				return editDoneMsg{err: fmt.Errorf("validation failed: %w", perr), path: path}
			}
		}
		return editDoneMsg{path: path}
	})
}

// --- Update / View ---

func (m *settingsModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case editDoneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Re-read config so profile edits etc. take effect without restart.
		if cfg, err := loadOrDefaultConfig(m.ctx.RepoPath); err == nil {
			m.ctx.Config = cfg
		}
		m.message = "saved: " + filepath.Base(msg.path)
		m.buildRows()
		return m, nil

	case tea.KeyMsg:
		if m.editing {
			switch msg.String() {
			case "enter":
				row := m.rows[m.cursor]
				if row.apply != nil {
					if err := row.apply(m.input.Value()); err != nil {
						m.err = err
						// keep editing so user can correct
						return m, nil
					}
					m.message = "saved: " + row.label
				}
				m.editing = false
				m.err = nil
				m.input.Blur()
				m.buildRows()
				return m, nil
			case "esc":
				m.editing = false
				m.err = nil
				m.input.Blur()
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "up", "k":
			m.moveCursor(-1)
			m.message = ""
		case "down", "j":
			m.moveCursor(+1)
			m.message = ""
		case "enter":
			row := m.rows[m.cursor]
			m.err = nil
			switch row.kind {
			case kindText, kindInt, kindSecret:
				m.editing = true
				m.input.SetValue("")
				m.input.EchoMode = textinput.EchoNormal
				if row.kind == kindSecret {
					m.input.EchoMode = textinput.EchoPassword
				}
				m.input.Focus()
				return m, textinput.Blink
			case kindRadio:
				if err := row.cycle(); err != nil {
					m.err = err
				} else {
					m.message = "updated: " + row.label
					// Changing the background-fetch interval should feel
					// immediate — bump the generation so any in-flight tick
					// from the previous cadence is dropped, then fire a
					// refresh and schedule a fresh tick.
					if row.label == "background fetch" {
						m.ctx.TickGen++
						return m, tea.Batch(refreshPlanCmd(m.ctx), schedulePeriodicRefresh(m.ctx))
					}
					// Flipping to auto when an update is already pending
					// should kick the install immediately rather than wait
					// for the next 24h tick.
					if row.label == "update mode" {
						return m, autoInstallIfNeeded(m.ctx)
					}
				}
			case kindBool:
				if err := row.toggle(); err != nil {
					m.err = err
				} else {
					m.message = "updated: " + row.label
				}
			case kindAction:
				if row.run != nil {
					return m, row.run(m)
				}
			}
		}
	}
	return m, nil
}

// moveCursor moves the cursor by delta, skipping past heading rows.
func (m *settingsModel) moveCursor(delta int) {
	i := m.cursor
	for {
		i += delta
		if i < 0 || i >= len(m.rows) {
			return
		}
		if !isHeading(m.rows[i]) {
			m.cursor = i
			return
		}
	}
}

func (m *settingsModel) View() string {
	var sb strings.Builder
	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}

	for i, r := range m.rows {
		if isHeading(r) {
			sb.WriteString("\n" + theme.Heading.Render(strings.TrimPrefix(r.label, "§ ")) + "\n")
			continue
		}
		cursor := "  "
		if m.cursor == i {
			cursor = theme.Primary.Render("▸ ")
		}
		label := fmt.Sprintf("%-26s", r.label)
		labelStyled := theme.Secondary.Render(label)
		if r.kind == kindDisplay {
			labelStyled = theme.Hint.Render(label)
		}
		fmt.Fprintf(&sb, "%s%s %s  %s\n", cursor, rowKindGlyph(r.kind), labelStyled, r.value())
	}

	if m.editing {
		cur := m.rows[m.cursor]
		sb.WriteString("\n" + theme.Primary.Render(cur.label+": ") + m.input.View())
		sb.WriteString("\n" + theme.Hint.Render("enter save • esc cancel"))
	} else {
		sb.WriteString("\n" + theme.Hint.Render("↑↓ move • enter edit/toggle • esc back"))
	}
	return sb.String()
}
