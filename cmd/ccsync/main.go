package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/colinc86/ccsync/internal/bootstrap"
	"github.com/colinc86/ccsync/internal/doctor"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/profile"
	"github.com/colinc86/ccsync/internal/snapshot"
	"github.com/colinc86/ccsync/internal/state"
	"github.com/colinc86/ccsync/internal/sync"
	"github.com/colinc86/ccsync/internal/tui"
)

const version = "0.1.0"

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
			os.Exit(runDoctor())
		case "profile":
			os.Exit(runProfile(os.Args[2:]))
		case "snapshot":
			os.Exit(runSnapshot(os.Args[2:]))
		case "rollback":
			os.Exit(runRollback(os.Args[2:]))
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
  ccsync doctor                       run integrity checks
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
	auth, _ := gitx.AuthConfig{Kind: gitx.AuthSSH, SSHKeyPath: ctx.State.SSHKeyPath}.Resolve()

	res, err := sync.Run(context.Background(), sync.Inputs{
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
		fmt.Fprintf(os.Stderr, "%d conflict(s) — resolve in the TUI\n", len(res.Plan.Conflicts))
		return 2
	}
	if len(res.MissingSecrets) > 0 {
		fmt.Fprintf(os.Stderr, "%d file(s) skipped due to missing secrets\n", len(res.MissingSecrets))
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
		meta, err := profile.Switch(ctx.State, ctx.StateDir, target, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccsync:", err)
			return 1
		}
		fmt.Printf("active profile: %s (backup %s)\n", target, meta.ID)
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
			fmt.Printf("  %s  %s  %d file(s)%s\n",
				s.CreatedAt.Local().Format("2006-01-02 15:04:05"),
				s.ID, len(s.Files), pin)
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
		fmt.Fprintln(os.Stderr, "ccsync: no snapshot to roll back to")
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
	auth, _ := gitx.AuthConfig{Kind: gitx.AuthSSH, SSHKeyPath: ctx.State.SSHKeyPath}.Resolve()
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
		fmt.Fprintf(os.Stderr, "%d file(s) skipped due to missing secrets; run `ccsync` and use RedactionReview\n",
			len(res.MissingSecrets))
		return 3
	}
	return 0
}

func runDoctor() int {
	ctx, err := tui.NewContext()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccsync:", err)
		return 1
	}
	repoPath := ""
	if ctx.State.SyncRepoURL != "" {
		repoPath = filepath.Join(ctx.StateDir, "repo")
	}
	r := doctor.Check(doctor.Inputs{
		ClaudeDir:  ctx.ClaudeDir,
		ClaudeJSON: ctx.ClaudeJSON,
		RepoPath:   repoPath,
		StateDir:   ctx.StateDir,
	})
	for _, f := range r.Findings {
		fmt.Printf("[%s] %s: %s\n", f.Severity, f.Check, f.Message)
		if f.Suggest != "" {
			fmt.Printf("       → %s\n", f.Suggest)
		}
	}
	if r.Worst() >= doctor.SeverityFail {
		return 1
	}
	return 0
}
