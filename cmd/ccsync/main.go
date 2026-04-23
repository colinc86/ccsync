package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/colinc86/ccsync/internal/bootstrap"
	cryptopkg "github.com/colinc86/ccsync/internal/crypto"
	"github.com/colinc86/ccsync/internal/doctor"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/humanize"
	ignorepkg "github.com/colinc86/ccsync/internal/ignore"
	"github.com/colinc86/ccsync/internal/profile"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/snapshot"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/tui"
	"github.com/colinc86/ccsync/internal/updater"
	watchpkg "github.com/colinc86/ccsync/internal/watch"
	"github.com/colinc86/ccsync/internal/why"
)

// version is settable via ldflags (see .goreleaser.yaml:
// -X main.version={{.Version}}) so release builds pick up the tag
// automatically. Local builds (go build, make build) fall back to
// the hardcoded value committed in this file. Declared var, not
// const — the Go linker can only override variables.
var version = "0.8.3"

func init() {
	updater.SetCurrentVersion(version)
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Println("ccsync " + version)
			return
		case "sync":
			os.Exit(runHeadlessSync(os.Args[2:]))
		case "bootstrap":
			os.Exit(runBootstrap(os.Args[2:]))
		case "doctor":
			os.Exit(runDoctor(os.Args[2:]))
		case "profile":
			os.Exit(runProfile(os.Args[2:]))
		case "snapshot":
			os.Exit(runSnapshot(os.Args[2:]))
		case "rollback":
			os.Exit(runRollback(os.Args[2:]))
		case "update":
			os.Exit(runUpdate(os.Args[2:]))
		case "why":
			os.Exit(runWhy(os.Args[2:]))
		case "blame":
			os.Exit(runBlame(os.Args[2:]))
		case "watch":
			os.Exit(runWatch(os.Args[2:]))
		case "encrypt":
			os.Exit(runEncrypt(os.Args[2:]))
		case "decrypt":
			os.Exit(runDecrypt(os.Args[2:]))
		case "unlock":
			os.Exit(runUnlock(os.Args[2:]))
		case "uninstall":
			os.Exit(runUninstall(os.Args[2:]))
		case "--help", "-h":
			printHelp()
			return
		}
	}

	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		os.Exit(1)
	}
	p := tea.NewProgram(tui.New(ctx), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		os.Exit(1)
	}
	// If the Update screen asked for a hot-swap into the freshly-
	// installed binary, do it now. By the time Run() returns,
	// bubbletea has restored the terminal to cooked mode, so the
	// re-exec's TUI starts clean. syscall.Exec replaces the current
	// process image, so we never return past it; control flow ends
	// here for the update-then-restart flow.
	if ctx.RestartBinaryPath != "" {
		env := os.Environ()
		// argv[0] is conventionally the program path. Preserve the
		// user's original os.Args so any invocation flags they
		// started with survive the restart (ccsync runs without
		// args 99% of the time, so this is belt-and-braces).
		argv := append([]string{ctx.RestartBinaryPath}, os.Args[1:]...)
		if err := syscall.Exec(ctx.RestartBinaryPath, argv, env); err != nil {
			fmt.Fprintln(os.Stderr, "ccsync: auto-restart failed:", err)
			fmt.Fprintln(os.Stderr, "ccsync: relaunch manually to pick up the new version")
			os.Exit(1)
		}
	}
}

func printHelp() {
	fmt.Println(`ccsync — sync Claude Code settings via git

Usage:
  ccsync                              launch TUI
  ccsync sync [--dry-run] [--yes]     headless sync
  ccsync bootstrap --repo URL         initialize sync from an existing git repo
  ccsync bootstrap --gh-create NAME   create a new private repo via gh CLI
  ccsync profile ls|use|create|rm     manage profiles
  ccsync snapshot ls                  list pre-sync snapshots
  ccsync snapshot restore ID          restore local files from a snapshot
  ccsync rollback                     restore local files from latest snapshot
  ccsync rollback --commit SHA        revert repo+local to a specific commit
  ccsync doctor [--fix]               run integrity checks (optionally auto-fix)
  ccsync why <path>                   trace which rules apply to a path
  ccsync blame <path>                 per-line sync attribution for a repo path
  ccsync watch [--debounce 10s]       auto-sync on local file changes
  ccsync encrypt                      enable repo encryption (prompts for passphrase)
  ccsync decrypt                      disable repo encryption
  ccsync unlock                       store the passphrase for an encrypted repo
  ccsync update [--check] [--force]   install the latest release in place
  ccsync uninstall [--yes]            remove state, snapshots, secrets, and self
  ccsync --version                    print version`)
}

func runHeadlessSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "compute change set without writing")
	_ = fs.Bool("yes", false, "skip interactive confirm (unused in headless mode — always applies)")
	fs.Parse(args)

	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if ctx.State.SyncRepoURL == "" {
		fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured. run: ccsync bootstrap --repo <URL>")
		return 1
	}

	profile := ctx.State.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	repoPath := filepath.Join(ctx.StateDir, "repo")
	auth := tui.BuildAuth(ctx)

	res, err := sync.RunWithRetry(context.Background(), sync.Inputs{
		Config:      ctx.Config,
		Profile:     profile,
		ClaudeDir:   ctx.ClaudeDir,
		ClaudeJSON:  ctx.ClaudeJSON,
		RepoPath:    repoPath,
		StateDir:    ctx.StateDir,
		HostUUID:    ctx.State.HostUUID,
		HostName:    ctx.HostName,
		AuthorEmail: ctx.Email,
		DryRun:      *dryRun,
		Auth:        auth,
	}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	added, modified, deleted := res.Plan.Summary()
	fmt.Printf("+%d ~%d -%d", added, modified, deleted)
	if res.CommitSHA != "" {
		short := res.CommitSHA
		if len(short) > 7 {
			short = short[:7]
		}
		fmt.Printf("  commit %s", short)
	}
	fmt.Println()
	if len(res.Plan.Conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "%s — resolve in the TUI\n", humanize.Count(len(res.Plan.Conflicts), "conflict"))
		return 2
	}
	if len(res.MissingSecrets) > 0 {
		fmt.Fprintf(os.Stderr, "%s skipped due to missing secrets\n", humanize.Count(len(res.MissingSecrets), "file"))
		return 3
	}
	return 0
}

func runBootstrap(args []string) int {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	repoURL := fs.String("repo", "", "URL of the sync repo to clone")
	ghCreate := fs.String("gh-create", "", "create a new private repo via gh CLI (repo name)")
	profileName := fs.String("profile", "default", "initial active profile")
	authKind := fs.String("auth", "ssh", "auth method (ssh | https)")
	fs.Parse(args)

	source := bootstrap.SourceExisting
	var url, repoName string
	switch {
	case *repoURL != "":
		source = bootstrap.SourceExisting
		url = *repoURL
		if _, err := os.Stat(url); err == nil {
			source = bootstrap.SourceLocalBare
		}
	case *ghCreate != "":
		source = bootstrap.SourceGHCreate
		repoName = *ghCreate
	default:
		fmt.Fprintln(os.Stderr, "ccsync bootstrap: provide --repo or --gh-create")
		return 1
	}

	stateDir, err := state.DefaultStateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}

	st, err := bootstrap.Run(context.Background(), bootstrap.Inputs{
		Source:   source,
		RepoURL:  url,
		RepoName: repoName,
		Profile:  *profileName,
		StateDir: stateDir,
		Auth:     state.AuthKind(*authKind),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	// Power-user bypass of the TUI wizard — but still flip the flag so
	// the onboarding flow doesn't nag on next TUI launch.
	st.OnboardingComplete = true
	_ = state.Save(stateDir, st)
	fmt.Printf("bootstrapped: profile=%s repo=%s\n", st.ActiveProfile, st.SyncRepoURL)
	fmt.Println("next: `ccsync sync` or launch the TUI")
	return 0
}

func runProfile(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ccsync profile: subcommand required (ls | use | create | rm)")
		return 1
	}
	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}

	switch args[0] {
	case "ls":
		names := make([]string, 0, len(ctx.Config.Profiles))
		for k := range ctx.Config.Profiles {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			marker := ""
			if n == ctx.State.ActiveProfile {
				marker = " (active)"
			}
			fmt.Printf("  %s%s  %s\n", n, marker, ctx.Config.Profiles[n].Description)
		}
		return 0

	case "use":
		if ctx.State.SyncRepoURL == "" {
			fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured. run: ccsync bootstrap --repo <URL>")
			return 1
		}
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "ccsync profile use: target profile required")
			return 1
		}
		target := args[1]
		if _, ok := ctx.Config.Profiles[target]; !ok {
			fmt.Fprintf(os.Stderr, "no such profile: %q\n", target)
			return 1
		}
		meta, err := profile.SwitchAndSwap(ctx.Config, ctx.RepoPath, ctx.State, ctx.StateDir, target, ctx.ClaudeDir, ctx.ClaudeJSON)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccsync:", err)
			return 1
		}
		fmt.Printf("active profile: %s (backup %s)\n", target, meta.ID)
		fmt.Println("next: `ccsync sync` to materialize the profile's content")
		return 0

	case "create":
		if ctx.State.SyncRepoURL == "" {
			fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured. run: ccsync bootstrap --repo <URL>")
			return 1
		}
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "ccsync profile create: name required")
			return 1
		}
		name := args[1]
		desc := ""
		if len(args) >= 3 {
			desc = args[2]
		}
		cfgPath := ctx.ConfigPath()
		if err := profile.Create(ctx.Config, cfgPath, name, desc); err != nil {
			fmt.Fprintln(os.Stderr, "ccsync:", err)
			return 1
		}
		fmt.Printf("created profile: %s\n", name)
		return 0

	case "rm":
		if ctx.State.SyncRepoURL == "" {
			fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured. run: ccsync bootstrap --repo <URL>")
			return 1
		}
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "ccsync profile rm: name required")
			return 1
		}
		name := args[1]
		cfgPath := ctx.ConfigPath()
		if err := profile.Delete(ctx.Config, cfgPath, name, ctx.State.ActiveProfile); err != nil {
			fmt.Fprintln(os.Stderr, "ccsync:", err)
			return 1
		}
		fmt.Printf("deleted profile: %s\n", name)
		return 0
	}

	fmt.Fprintf(os.Stderr, "unknown profile subcommand: %s\n", args[0])
	return 1
}

func runSnapshot(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ccsync snapshot: subcommand required (ls | restore)")
		return 1
	}
	stateDir, err := state.DefaultStateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	root := filepath.Join(stateDir, "snapshots")

	switch args[0] {
	case "ls":
		snaps, err := snapshot.List(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccsync:", err)
			return 1
		}
		if len(snaps) == 0 {
			fmt.Println("(no snapshots)")
			return 0
		}
		for _, s := range snaps {
			pin := ""
			if s.Pinned {
				pin = " [pinned]"
			}
			fmt.Printf("  %s  %s  %s%s\n",
				s.CreatedAt.Local().Format("2006-01-02 15:04:05"),
				s.ID, humanize.Count(len(s.Files), "file"), pin)
		}
		return 0

	case "restore":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "ccsync snapshot restore: snapshot ID required")
			return 1
		}
		if err := snapshot.Restore(root, args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "ccsync:", err)
			return 1
		}
		fmt.Printf("restored: %s\n", args[1])
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown snapshot subcommand: %s\n", args[0])
	return 1
}

func runRollback(args []string) int {
	fs := flag.NewFlagSet("rollback", flag.ExitOnError)
	commitSHA := fs.String("commit", "", "roll back to a specific commit SHA (creates a new forward commit)")
	fs.Parse(args)

	if *commitSHA != "" {
		return runRollbackCommit(*commitSHA)
	}

	stateDir, err := state.DefaultStateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	root := filepath.Join(stateDir, "snapshots")
	snaps, err := snapshot.List(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if len(snaps) == 0 {
		// Snapshots are taken pre-local-write, so push-only machines
		// (the common case for the machine where edits originate)
		// never accumulate any. Users on such machines reaching for
		// rollback usually want to undo a push, which is a different
		// tool — point them there rather than leaving them at a dead
		// end.
		fmt.Fprintln(os.Stderr, "ccsync: no snapshot to roll back to")
		fmt.Fprintln(os.Stderr, "  snapshots are only taken on pulls/merges that write locally;")
		fmt.Fprintln(os.Stderr, "  to undo a push, use `ccsync rollback --commit <sha>` instead")
		fmt.Fprintln(os.Stderr, "  (see recent commits with: git -C ~/.ccsync/repo log --oneline)")
		return 1
	}
	latest := snaps[0]
	if err := snapshot.Restore(root, latest.ID); err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	fmt.Printf("rolled back to %s (%d files restored)\n", latest.ID, len(latest.Files))
	fmt.Println("note: this restored LOCAL files only. if you pushed changes you want to undo, use `ccsync rollback --commit SHA` instead.")
	return 0
}

func runRollbackCommit(commitSHA string) int {
	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if ctx.State.SyncRepoURL == "" {
		fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured")
		return 1
	}
	profileName := ctx.State.ActiveProfile
	if profileName == "" {
		profileName = "default"
	}
	auth := tui.BuildAuth(ctx)
	in := sync.Inputs{
		Config: ctx.Config, Profile: profileName,
		ClaudeDir: ctx.ClaudeDir, ClaudeJSON: ctx.ClaudeJSON,
		RepoPath: ctx.RepoPath, StateDir: ctx.StateDir,
		HostUUID: ctx.State.HostUUID, HostName: ctx.HostName, AuthorEmail: ctx.Email,
		Auth: auth,
	}
	res, err := sync.RollbackTo(context.Background(), in, commitSHA)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	short := res.CommitSHA
	if len(short) > 7 {
		short = short[:7]
	}
	if res.CommitSHA == "" {
		fmt.Println("already matches target")
		return 0
	}
	fmt.Printf("rolled back to %s (new commit %s)\n", commitSHA[:7], short)
	if len(res.MissingSecrets) > 0 {
		fmt.Fprintf(os.Stderr, "%s skipped due to missing secrets; run `ccsync` and use RedactionReview\n",
			humanize.Count(len(res.MissingSecrets), "file"))
		return 3
	}
	return 0
}

func runEncrypt(args []string) int {
	fs := flag.NewFlagSet("encrypt", flag.ExitOnError)
	passphrase := fs.String("passphrase", "", "encryption passphrase (read from stdin if blank)")
	fs.Parse(args)

	pp := strings.TrimSpace(*passphrase)
	if pp == "" {
		fmt.Fprint(os.Stderr, "passphrase: ")
		b, err := readPassphrase()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccsync encrypt:", err)
			return 1
		}
		pp = strings.TrimSpace(string(b))
	}
	if pp == "" {
		fmt.Fprintln(os.Stderr, "ccsync encrypt: passphrase required")
		return 1
	}

	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if ctx.State.SyncRepoURL == "" {
		fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured")
		return 1
	}
	in := buildMigrationInputs(ctx)
	res, err := sync.EnableEncryption(context.Background(), in, pp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync encrypt:", err)
		return 1
	}
	if res.CommitSHA != "" {
		short := res.CommitSHA
		if len(short) > 7 {
			short = short[:7]
		}
		fmt.Printf("repo encrypted (migration commit %s)\n", short)
	} else {
		fmt.Println("repo encrypted")
	}
	return 0
}

// runUnlock accepts the passphrase for an already-encrypted repo, verifies
// it by decrypting the marker-signed round trip, and stores it in the
// keychain so subsequent syncs work. The typical path for a second machine
// after a fresh `ccsync bootstrap` against an encrypted repo.
func runUnlock(args []string) int {
	fs := flag.NewFlagSet("unlock", flag.ExitOnError)
	passphrase := fs.String("passphrase", "", "encryption passphrase (read from stdin if blank)")
	fs.Parse(args)

	pp := strings.TrimSpace(*passphrase)
	if pp == "" {
		fmt.Fprint(os.Stderr, "passphrase: ")
		b, err := readPassphrase()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccsync unlock:", err)
			return 1
		}
		pp = strings.TrimSpace(string(b))
	}
	if pp == "" {
		fmt.Fprintln(os.Stderr, "ccsync unlock: passphrase required")
		return 1
	}

	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	marker, err := cryptopkg.ReadMarker(ctx.RepoPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync unlock:", err)
		return 1
	}
	if marker == nil {
		fmt.Fprintln(os.Stderr, "ccsync unlock: repo is not encrypted")
		return 1
	}
	key, err := marker.DeriveKey(pp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync unlock:", err)
		return 1
	}
	// Sanity-check the passphrase by decrypting any encrypted file we can
	// find. Fail loud if it doesn't match — much better than silently
	// storing a wrong passphrase and hitting errors later.
	if err := verifyPassphrase(ctx.RepoPath, key); err != nil {
		fmt.Fprintln(os.Stderr, "ccsync unlock:", err)
		return 1
	}
	if err := secrets.Store(sync.SecretsKeyPassphrase, pp); err != nil {
		fmt.Fprintln(os.Stderr, "ccsync unlock: store:", err)
		return 1
	}
	fmt.Println("passphrase stored. next: `ccsync sync`")
	return 0
}

// verifyPassphrase walks the repo for any file with the encryption magic
// and tries to decrypt it. A single successful decrypt proves the key.
func verifyPassphrase(repoPath string, key []byte) error {
	var checked int
	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if strings.Contains(path, string(os.PathSeparator)+".git"+string(os.PathSeparator)) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !cryptopkg.HasMagic(data) {
			return nil
		}
		if _, derr := cryptopkg.Decrypt(key, data); derr != nil {
			return fmt.Errorf("wrong passphrase (couldn't decrypt %s)", filepath.Base(path))
		}
		checked++
		return filepath.SkipAll
	})
	if err != nil {
		return err
	}
	if checked == 0 {
		return fmt.Errorf("repo has no encrypted files yet — nothing to verify against")
	}
	return nil
}

func runDecrypt(args []string) int {
	_ = args
	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if ctx.State.SyncRepoURL == "" {
		fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured")
		return 1
	}
	in := buildMigrationInputs(ctx)
	res, err := sync.DisableEncryption(context.Background(), in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync decrypt:", err)
		return 1
	}
	if res.CommitSHA != "" {
		short := res.CommitSHA
		if len(short) > 7 {
			short = short[:7]
		}
		fmt.Printf("repo decrypted (migration commit %s)\n", short)
	} else {
		fmt.Println("repo decrypted")
	}
	return 0
}

// buildMigrationInputs assembles just enough sync.Inputs for the
// enable/disable migration routines. They don't need Config/Profile since
// they operate on every file under profiles/ regardless of which profile
// is active.
func buildMigrationInputs(ctx *tui.AppContext) sync.Inputs {
	auth := tui.BuildAuth(ctx)
	return sync.Inputs{
		RepoPath:    ctx.RepoPath,
		StateDir:    ctx.StateDir,
		HostUUID:    ctx.State.HostUUID,
		HostName:    ctx.HostName,
		AuthorEmail: ctx.Email,
		Auth:        auth,
	}
}

// readPassphrase reads a secret from stdin. Uses the terminal's no-echo
// mode when stdin is a TTY; otherwise reads one line so pipes / CI still
// work (e.g. `echo pw | ccsync encrypt`).
func readPassphrase() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr) // newline after the hidden entry
		return b, err
	}
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			buf = append(buf, b[0])
		}
		if err != nil {
			break
		}
	}
	return buf, nil
}

func runWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	debounce := fs.Duration("debounce", 10*time.Second, "quiet period before firing a sync")
	fs.Parse(args)

	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if ctx.State.SyncRepoURL == "" {
		fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured")
		return 1
	}
	profile := ctx.State.ActiveProfile
	if profile == "" {
		profile = "default"
	}
	auth := tui.BuildAuth(ctx)
	syncIn := sync.Inputs{
		Config:      ctx.Config,
		Profile:     profile,
		ClaudeDir:   ctx.ClaudeDir,
		ClaudeJSON:  ctx.ClaudeJSON,
		RepoPath:    ctx.RepoPath,
		StateDir:    ctx.StateDir,
		HostUUID:    ctx.State.HostUUID,
		HostName:    ctx.HostName,
		AuthorEmail: ctx.Email,
		Auth:        auth,
	}
	// Load .syncignore so the watcher doesn't wake on ignored dirs.
	syncignoreRules := ctx.Config.DefaultSyncignore
	if data, err := os.ReadFile(filepath.Join(ctx.RepoPath, ".syncignore")); err == nil {
		syncignoreRules = string(data)
	}
	matcher := ignorepkg.New(syncignoreRules)

	runCtx, cancel := signalContext()
	defer cancel()

	return runWatchLoop(runCtx, watchpkg.Inputs{
		SyncInputs: syncIn,
		Debounce:   *debounce,
		Out:        os.Stdout,
		Ignore:     matcher,
	})
}

// runWatchLoop hands off to the watch package. Broken out so the tests can
// swap it, and so main.go stays a thin dispatcher.
func runWatchLoop(ctx context.Context, in watchpkg.Inputs) int {
	if err := watchpkg.Run(ctx, in); err != nil {
		fmt.Fprintln(os.Stderr, "ccsync watch:", err)
		return 1
	}
	return 0
}

// signalContext returns a context that cancels on SIGINT/SIGTERM.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func runBlame(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ccsync blame: provide a repo-relative path, e.g.")
		fmt.Fprintln(os.Stderr, "  ccsync blame profiles/default/claude/agents/foo.md")
		fmt.Fprintln(os.Stderr, "  ccsync blame claude/agents/foo.md   # shorthand under active profile")
		return 1
	}
	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if ctx.State.SyncRepoURL == "" {
		fmt.Fprintln(os.Stderr, "ccsync: no sync repo configured")
		return 1
	}
	target := args[0]
	// Accept a shorthand: if the user gave a path like claude/agents/foo.md
	// (which is how they see it everywhere else in the app), prepend the
	// active profile prefix so the blame lookup hits the right blob.
	profilePrefix := "profiles/" + ctx.State.ActiveProfile + "/"
	if !strings.HasPrefix(target, "profiles/") {
		target = profilePrefix + target
	}

	repo, err := gitx.Open(ctx.RepoPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	lines, err := repo.Blame(target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	if len(lines) == 0 {
		fmt.Fprintf(os.Stderr, "ccsync blame: no history for %q\n", target)
		return 1
	}

	// Figure out max host-name width for alignment.
	hostWidth := 1
	for _, l := range lines {
		if w := len(l.AuthorName); w > hostWidth {
			hostWidth = w
		}
	}
	if hostWidth > 16 {
		hostWidth = 16
	}

	for _, l := range lines {
		short := l.SHA
		if len(short) > 7 {
			short = short[:7]
		}
		host := l.AuthorName
		if len(host) > hostWidth {
			host = host[:hostWidth]
		}
		date := l.When.Local().Format("2006-01-02")
		text := strings.TrimRight(l.Text, "\n")
		fmt.Printf("%s  %-*s  %s  %4d│ %s\n", short, hostWidth, host, date, l.LineNo, text)
	}
	return 0
}

func runWhy(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ccsync why: provide a path, e.g.")
		fmt.Fprintln(os.Stderr, "  ccsync why claude/agents/foo.md")
		fmt.Fprintln(os.Stderr, "  ccsync why ~/.claude.json:'$.mcpServers.gemini'")
		return 1
	}
	target := args[0]

	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	profileName := ctx.State.ActiveProfile
	if profileName == "" {
		profileName = "default"
	}

	// Prefer the repo's .syncignore when present; otherwise defaults.
	syncignore := ctx.Config.DefaultSyncignore
	if ctx.State.SyncRepoURL != "" {
		if data, err := os.ReadFile(filepath.Join(ctx.RepoPath, ".syncignore")); err == nil {
			syncignore = string(data)
		}
	}

	tr, err := why.Explain(why.Inputs{
		Config:     ctx.Config,
		Profile:    profileName,
		Syncignore: syncignore,
		ClaudeDir:  ctx.ClaudeDir,
		ClaudeJSON: ctx.ClaudeJSON,
	}, target)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync why:", err)
		return 1
	}
	fmt.Print(why.Format(tr))
	return 0
}

func runUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	check := fs.Bool("check", false, "print whether an update is available and exit")
	force := fs.Bool("force", false, "reinstall even if the current version matches latest")
	fs.Parse(args)

	tag, err := updater.LatestTag()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync update:", err)
		return 1
	}
	current := "v" + version
	if tag == current && !*force {
		fmt.Printf("ccsync is up to date (%s)\n", current)
		return 0
	}
	if *check {
		fmt.Printf("update available: %s → %s\n", current, tag)
		return 0
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync update:", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	if updater.IsHomebrew(exe) {
		fmt.Println("ccsync was installed via Homebrew. run: brew upgrade ccsync")
		return 0
	}

	fmt.Printf("installing %s → %s ...\n", current, tag)
	if err := updater.InstallRelease(tag, exe); err != nil {
		fmt.Fprintln(os.Stderr, "ccsync update:", err)
		fmt.Fprintln(os.Stderr, "hint: if ccsync lives in a system dir you can't write, reinstall with:")
		fmt.Fprintln(os.Stderr, "  curl -fsSL https://raw.githubusercontent.com/colinc86/ccsync/main/scripts/install.sh | bash")
		return 1
	}
	fmt.Printf("updated: %s → %s\n", current, tag)
	return 0
}

func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	fix := fs.Bool("fix", false, "auto-apply safe fixes for detected issues")
	fs.Parse(args)

	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	repoPath := ""
	if ctx.State.SyncRepoURL != "" {
		repoPath = filepath.Join(ctx.StateDir, "repo")
	}
	in := doctor.Inputs{
		ClaudeDir:  ctx.ClaudeDir,
		ClaudeJSON: ctx.ClaudeJSON,
		RepoPath:   repoPath,
		StateDir:   ctx.StateDir,
	}
	r := doctor.Check(in)
	for _, f := range r.Findings {
		fmt.Printf("[%s] %s: %s\n", f.Severity, f.Check, f.Message)
		if f.Suggest != "" {
			fmt.Printf("       → %s\n", f.Suggest)
		}
	}
	if *fix {
		applied, errs := r.ApplyFixes()
		if applied > 0 {
			fmt.Printf("\napplied %d fix(es); re-checking…\n\n", applied)
			r = doctor.Check(in)
			for _, f := range r.Findings {
				fmt.Printf("[%s] %s: %s\n", f.Severity, f.Check, f.Message)
			}
		} else {
			fmt.Println("\nno fixable issues found")
		}
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "fix error:", e)
		}
	}
	if r.Worst() >= doctor.SeverityFail {
		return 1
	}
	return 0
}

// runUninstall removes everything ccsync has written on this machine
// (state dir, snapshots, keychain secrets) and finally the binary
// itself. Leaves ~/.claude and ~/.claude.json untouched — those
// belong to Claude Code, not us. The remote sync repo is also left
// alone; deleting it is a separate choice on the user's GitHub/host.
//
// The --yes flag skips the interactive prompt; otherwise we print the
// plan and ask for confirmation.
func runUninstall(args []string) int {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	yes := fs.Bool("yes", false, "skip the interactive confirmation")
	fs.BoolVar(yes, "y", false, "alias for --yes")
	_ = fs.Parse(args)

	stateDir, err := state.DefaultStateDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uninstall:", err)
		return 1
	}
	exePath, _ := os.Executable()

	fmt.Println("ccsync uninstall — the following will be removed from this machine:")
	fmt.Println()
	fmt.Printf("  state directory  %s\n", stateDir)
	fmt.Printf("    - state.json, repo clone, snapshots, file-backend secrets\n")
	fmt.Printf("  keychain secrets  service \"%s\"\n", secrets.ServiceName)
	fmt.Printf("  binary            %s\n", binaryLabel(exePath))
	fmt.Println()
	fmt.Println("NOT touched:")
	fmt.Println("  ~/.claude/, ~/.claude.json  (Claude Code's own files)")
	fmt.Println("  the remote sync repo        (delete on GitHub/host if you want it gone)")
	fmt.Println()

	if !*yes {
		fmt.Print("Proceed? (y/N) ")
		var ans string
		_, _ = fmt.Scanln(&ans)
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Println("aborted.")
			return 0
		}
	}

	var errs []error

	// 1. Keychain / file-backend secrets. Do this FIRST because the
	// backend choice lives in state.json which we're about to delete.
	if st, err := state.Load(stateDir); err == nil {
		secrets.SetBackend(string(st.SecretsBackend))
	}
	if err := secrets.DeleteAll(); err != nil {
		errs = append(errs, fmt.Errorf("keychain: %w", err))
	} else {
		fmt.Println("removed keychain secrets")
	}

	// 2. State directory (includes repo clone + snapshots + file-backend).
	if err := os.RemoveAll(stateDir); err != nil {
		errs = append(errs, fmt.Errorf("state dir: %w", err))
	} else {
		fmt.Printf("removed %s\n", stateDir)
	}

	// 3. The binary itself. On darwin/linux removing a running binary
	// is fine — the kernel keeps the inode alive for this process and
	// the path just goes away. Homebrew-managed binaries should go
	// through `brew uninstall`, so we bail out with instructions.
	switch {
	case exePath == "":
		fmt.Println("skipped binary — couldn't resolve own path; remove manually")
	case updater.IsHomebrew(exePath):
		fmt.Println("skipped binary — appears Homebrew-managed; run: brew uninstall ccsync")
	default:
		if err := os.Remove(exePath); err != nil {
			errs = append(errs, fmt.Errorf("binary: %w", err))
		} else {
			fmt.Printf("removed %s\n", exePath)
		}
	}

	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr)
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
		return 1
	}
	fmt.Println()
	fmt.Println("ccsync is gone from this machine.")
	return 0
}

// binaryLabel renders a readable install path for the uninstall banner,
// distinguishing Homebrew from a normal install.
func binaryLabel(exe string) string {
	if exe == "" {
		return "(unknown — install path not resolvable)"
	}
	if updater.IsHomebrew(exe) {
		return exe + "  (Homebrew-managed — will skip)"
	}
	return exe
}
