package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	cryptopkg "github.com/colinc86/ccsync/internal/crypto"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/secrets"
	syncpkg "github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/theme"
)

// encStatus describes the repo's encryption state from this machine's POV.
// "on" means a marker exists AND we have the passphrase cached. "locked"
// means the marker exists but our keychain doesn't — typical of a second
// machine that just cloned but hasn't unlocked yet.
type encStatus int

const (
	encOff encStatus = iota
	encOn
	encLocked
)

// encryptionStep tracks where the user is in the flow.
type encryptionStep int

const (
	encStepChoose   encryptionStep = iota // show status + available actions
	encStepPrompt                         // passphrase input
	encStepConfirm                        // confirm destructive choice (disable)
	encStepRunning                        // migration in flight
	encStepResult                         // terminal — show outcome and pop on key
)

type encryptionAction int

const (
	encActionEnable encryptionAction = iota
	encActionDisable
	encActionUnlock
)

type encryptionScreenModel struct {
	ctx     *AppContext
	status  encStatus
	step    encryptionStep
	action  encryptionAction
	input   textinput.Model
	spin    spinner.Model
	err     error
	message string
	running bool
}

type encMigrationDoneMsg struct {
	commitSHA string
	err       error
	action    encryptionAction
}

func newEncryptionScreen(ctx *AppContext) *encryptionScreenModel {
	ti := textinput.New()
	ti.CharLimit = 128
	ti.Width = 40
	ti.EchoMode = textinput.EchoPassword
	return &encryptionScreenModel{
		ctx:    ctx,
		status: detectEncStatus(ctx),
		step:   encStepChoose,
		input:  ti,
		spin:   newSpinner(),
	}
}

func (m *encryptionScreenModel) Title() string { return "Repo encryption" }

func (m *encryptionScreenModel) Init() tea.Cmd { return nil }

func (m *encryptionScreenModel) CapturesEscape() bool {
	// While running or prompting, esc should step back within this screen
	// rather than popping it entirely. The result step lets esc pop.
	return m.step == encStepPrompt || m.step == encStepConfirm || m.running
}

func detectEncStatus(ctx *AppContext) encStatus {
	if ctx == nil || ctx.RepoPath == "" {
		return encOff
	}
	marker, err := cryptopkg.ReadMarker(ctx.RepoPath)
	if err != nil || marker == nil {
		return encOff
	}
	if _, err := secrets.Fetch(syncpkg.SecretsKeyPassphrase); err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return encLocked
		}
		return encLocked
	}
	return encOn
}

func (m *encryptionScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case encMigrationDoneMsg:
		m.running = false
		m.err = msg.err
		if msg.err == nil {
			switch msg.action {
			case encActionEnable:
				m.message = "repo encrypted ✓"
				if msg.commitSHA != "" {
					m.message += "  migration commit " + shortSHA(msg.commitSHA)
				}
			case encActionDisable:
				m.message = "repo decrypted ✓"
				if msg.commitSHA != "" {
					m.message += "  migration commit " + shortSHA(msg.commitSHA)
				}
			case encActionUnlock:
				m.message = "passphrase stored ✓"
			}
			// Refresh the detected status for anything the user does next.
			m.status = detectEncStatus(m.ctx)
			// Plan cache is stale after a migration commit — nudge it.
			m.ctx.RefreshState()
		}
		m.step = encStepResult
		return m, refreshPlanCmd(m.ctx)

	case tea.KeyMsg:
		if m.running {
			return m, nil
		}
		switch m.step {
		case encStepChoose:
			return m.updateChoose(msg)
		case encStepPrompt:
			return m.updatePrompt(msg)
		case encStepConfirm:
			return m.updateConfirm(msg)
		case encStepResult:
			return m, popScreen()
		}
	}
	return m, nil
}

func (m *encryptionScreenModel) updateChoose(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "e":
		if m.status != encOff {
			return m, nil
		}
		m.action = encActionEnable
		m.step = encStepPrompt
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "u":
		if m.status != encLocked {
			return m, nil
		}
		m.action = encActionUnlock
		m.step = encStepPrompt
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "d":
		if m.status != encOn {
			return m, nil
		}
		m.action = encActionDisable
		m.step = encStepConfirm
		return m, nil
	}
	return m, nil
}

func (m *encryptionScreenModel) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.step = encStepChoose
		m.input.Blur()
		m.err = nil
		return m, nil
	case "enter":
		pp := strings.TrimSpace(m.input.Value())
		if pp == "" {
			m.err = fmt.Errorf("passphrase required")
			return m, nil
		}
		m.err = nil
		m.running = true
		m.step = encStepRunning
		m.input.Blur()
		return m, tea.Batch(m.runMigration(pp), m.spin.Tick)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *encryptionScreenModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.step = encStepChoose
		return m, nil
	case "y":
		m.running = true
		m.step = encStepRunning
		return m, tea.Batch(m.runMigration("" /*not needed — passphrase in keychain*/), m.spin.Tick)
	}
	return m, nil
}

// runMigration dispatches to the appropriate sync package entry point and
// packages the result as an encMigrationDoneMsg.
func (m *encryptionScreenModel) runMigration(passphrase string) tea.Cmd {
	action := m.action
	return func() tea.Msg {
		in := buildMigrationInputsFromCtx(m.ctx)
		switch action {
		case encActionEnable:
			res, err := syncpkg.EnableEncryption(context.Background(), in, passphrase)
			return encMigrationDoneMsg{commitSHA: res.CommitSHA, err: err, action: action}
		case encActionDisable:
			res, err := syncpkg.DisableEncryption(context.Background(), in)
			return encMigrationDoneMsg{commitSHA: res.CommitSHA, err: err, action: action}
		case encActionUnlock:
			err := storeAndVerifyPassphrase(m.ctx, passphrase)
			return encMigrationDoneMsg{err: err, action: action}
		}
		return encMigrationDoneMsg{err: fmt.Errorf("unknown action"), action: action}
	}
}

// storeAndVerifyPassphrase mirrors the CLI `unlock` command: derive a key
// from the passphrase + marker's salt, trial-decrypt any ciphertext in the
// repo to verify, and on success cache the passphrase in the keychain.
func storeAndVerifyPassphrase(ctx *AppContext, passphrase string) error {
	marker, err := cryptopkg.ReadMarker(ctx.RepoPath)
	if err != nil {
		return err
	}
	if marker == nil {
		return fmt.Errorf("repo is not encrypted")
	}
	key, err := marker.DeriveKey(passphrase)
	if err != nil {
		return err
	}
	if err := trialDecrypt(ctx.RepoPath, key); err != nil {
		return err
	}
	return secrets.Store(syncpkg.SecretsKeyPassphrase, passphrase)
}

// buildMigrationInputsFromCtx is the TUI mirror of the CLI helper — just
// enough fields for the sync package's migration routines.
func buildMigrationInputsFromCtx(ctx *AppContext) syncpkg.Inputs {
	auth, _ := gitx.AuthConfig{
		Kind:       gitx.AuthSSH,
		SSHKeyPath: ctx.State.SSHKeyPath,
	}.Resolve()
	return syncpkg.Inputs{
		RepoPath:    ctx.RepoPath,
		StateDir:    ctx.StateDir,
		HostUUID:    ctx.State.HostUUID,
		HostName:    ctx.HostName,
		AuthorEmail: ctx.Email,
		Auth:        auth,
	}
}

func (m *encryptionScreenModel) View() string {
	var sb strings.Builder

	if m.err != nil {
		sb.WriteString(theme.Bad.Render("error: "+m.err.Error()) + "\n\n")
	} else if m.message != "" {
		sb.WriteString(theme.Good.Render(m.message) + "\n\n")
	}

	// Always show status at the top so the user has context.
	fmt.Fprintf(&sb, "  %s  %s\n\n",
		theme.Secondary.Render("status:"), renderEncStatus(m.status))

	switch m.step {
	case encStepChoose:
		sb.WriteString(m.renderChoose())
	case encStepPrompt:
		sb.WriteString(m.renderPrompt())
	case encStepConfirm:
		sb.WriteString(m.renderConfirm())
	case encStepRunning:
		sb.WriteString(m.spin.View() + " " + theme.Hint.Render(m.runningLabel()))
	case encStepResult:
		sb.WriteString(theme.Hint.Render("press any key to return"))
	}
	return sb.String()
}

func renderEncStatus(s encStatus) string {
	switch s {
	case encOff:
		return theme.Hint.Render("off — repo contents are plaintext in git")
	case encOn:
		return theme.Good.Render("on — repo contents encrypted; passphrase cached on this machine")
	case encLocked:
		return theme.Warn.Render("on (locked) — repo is encrypted but this machine doesn't have the passphrase")
	}
	return "?"
}

func (m *encryptionScreenModel) renderChoose() string {
	var sb strings.Builder
	sb.WriteString(theme.Heading.Render("available actions") + "\n\n")
	switch m.status {
	case encOff:
		sb.WriteString("  " + theme.Primary.Render("e") + "  enable encryption")
		sb.WriteString(theme.Hint.Render(" — prompts for a passphrase; re-encrypts every tracked file") + "\n")
	case encOn:
		sb.WriteString("  " + theme.Primary.Render("d") + "  disable encryption")
		sb.WriteString(theme.Hint.Render(" — decrypts and recommits; plaintext after this") + "\n")
	case encLocked:
		sb.WriteString("  " + theme.Primary.Render("u") + "  unlock")
		sb.WriteString(theme.Hint.Render(" — enter the passphrase so this machine can sync") + "\n")
	}
	sb.WriteString("\n" + theme.Hint.Render("esc back"))
	return sb.String()
}

func (m *encryptionScreenModel) renderPrompt() string {
	var sb strings.Builder
	label := "passphrase:"
	hint := ""
	switch m.action {
	case encActionEnable:
		label = "new passphrase:"
		hint = "choose something memorable — you'll need it on every machine you sync from"
	case encActionUnlock:
		label = "passphrase:"
		hint = "the passphrase set when encryption was enabled on another machine"
	}
	fmt.Fprintf(&sb, "  %s  %s\n", theme.Secondary.Render(label), m.input.View())
	if hint != "" {
		sb.WriteString("\n  " + theme.Hint.Render(hint))
	}
	sb.WriteString("\n\n" + theme.Hint.Render("enter confirm • esc back"))
	return sb.String()
}

func (m *encryptionScreenModel) renderConfirm() string {
	var sb strings.Builder
	sb.WriteString(theme.Warn.Render("disable encryption?") + "\n\n")
	sb.WriteString(theme.Hint.Render(
		"every tracked file in the repo will be decrypted and pushed up as a\n" +
			"migration commit. once done, repo contents are visible to anyone with\n" +
			"read access to the remote."))
	sb.WriteString("\n\n" +
		theme.Primary.Render("y") + "  confirm disable • " +
		theme.Hint.Render("esc back"))
	return sb.String()
}

func (m *encryptionScreenModel) runningLabel() string {
	switch m.action {
	case encActionEnable:
		return "encrypting every tracked file… this can take a few seconds"
	case encActionDisable:
		return "decrypting every tracked file…"
	case encActionUnlock:
		return "verifying passphrase…"
	}
	return "working…"
}

// trialDecrypt finds any encrypted file in the repo and tries to decrypt
// it with key. Success proves the passphrase; failure means it's wrong.
func trialDecrypt(repoPath string, key []byte) error {
	sample, err := firstEncryptedFile(repoPath)
	if err != nil {
		return err
	}
	if sample == nil {
		return fmt.Errorf("repo has no encrypted files to verify against yet")
	}
	if _, err := cryptopkg.Decrypt(key, sample); err != nil {
		return fmt.Errorf("wrong passphrase (couldn't decrypt an encrypted file)")
	}
	return nil
}

// firstEncryptedFile returns the bytes of the first file under repoPath
// that carries the crypto magic header. Walks stop at the first match.
// Skips .git entirely — those object packs carry all sorts of magic-ish
// bytes but aren't our concern.
func firstEncryptedFile(repoPath string) ([]byte, error) {
	var result []byte
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if cryptopkg.HasMagic(data) {
			result = data
			return filepath.SkipAll
		}
		return nil
	})
	return result, err
}
