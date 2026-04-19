package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"

	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/discover"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/ignore"
	"github.com/colinc86/ccsync/internal/jsonfilter"
	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/merge"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/snapshot"
)

// Inputs bundles everything Run needs.
type Inputs struct {
	Config      *config.Config
	Profile     string
	ClaudeDir   string // absolute path to ~/.claude
	ClaudeJSON  string // absolute path to ~/.claude.json
	RepoPath    string // local clone of sync repo
	StateDir    string // ~/.ccsync
	HostUUID    string
	HostName    string
	AuthorEmail string
	DryRun      bool
	Auth        transport.AuthMethod

	// OnlyPaths, when non-nil, restricts this Run to a subset of repo paths.
	// Paths not in the set are shown in the plan (action=NoOp) but are NOT
	// applied. When set, state.LastSyncedSHA is NOT advanced so the skipped
	// paths remain pending for the next sync. Selective sync is one-shot.
	OnlyPaths map[string]bool
}

// Selective reports whether this run is a filtered/selective sync.
func (in Inputs) Selective() bool { return in.OnlyPaths != nil }

// Run performs a full sync. Events are emitted on the provided channel; nil
// channel disables events. The channel is NOT closed by Run.
func Run(ctx context.Context, in Inputs, events chan<- Event) (Result, error) {
	emit := func(stage, msg, path string) {
		if events == nil {
			return
		}
		select {
		case events <- Event{Stage: stage, Message: msg, Path: path}:
		case <-ctx.Done():
		}
	}

	repo, err := gitx.Open(in.RepoPath)
	if err != nil {
		return Result{}, fmt.Errorf("open repo: %w", err)
	}

	empty, err := repo.IsEmpty()
	if err != nil {
		return Result{}, err
	}
	if !empty {
		emit("fetch", "fetching remote", "")
		if err := repo.Fetch(ctx, in.Auth); err != nil {
			return Result{}, fmt.Errorf("fetch: %w", err)
		}
	}

	manifestPath := filepath.Join(in.RepoPath, "manifest.json")
	mf, err := manifest.Load(manifestPath, in.HostUUID)
	if err != nil {
		return Result{}, err
	}

	state, err := loadHostState(in.StateDir)
	if err != nil {
		return Result{}, fmt.Errorf("load state: %w", err)
	}
	baseCommit := state.LastSyncedSHA[in.Profile]

	matcher := ignore.New(in.Config.DefaultSyncignore)
	if data, err := os.ReadFile(filepath.Join(in.RepoPath, ".syncignore")); err == nil {
		matcher = ignore.New(string(data))
	}

	emit("discover", "walking local Claude config", "")
	disc, err := discover.Walk(discover.Inputs{
		ClaudeDir:  in.ClaudeDir,
		ClaudeJSON: in.ClaudeJSON,
	}, matcher)
	if err != nil {
		return Result{}, err
	}

	jsonRules := resolveJSONRules(in.Config, in.ClaudeDir, in.ClaudeJSON)
	profilePrefix := "profiles/" + in.Profile + "/"

	localEntries := map[string]*localFile{}
	for _, e := range disc.Tracked {
		data, err := os.ReadFile(e.AbsPath)
		if err != nil {
			return Result{}, fmt.Errorf("read %s: %w", e.AbsPath, err)
		}
		repoPath := profilePrefix + e.RelPath
		lf := &localFile{abs: e.AbsPath, data: data}
		if rule, ok := jsonRules[e.AbsPath]; ok {
			res, err := jsonfilter.Apply(data, rule, in.Profile)
			if err != nil {
				return Result{}, fmt.Errorf("filter %s: %w", e.AbsPath, err)
			}
			lf.data = res.Data
			lf.redactions = res.Redactions
			lf.isJSON = true
		}
		lf.sha = manifest.SHA256Bytes(lf.data)
		localEntries[repoPath] = lf
	}

	remoteEntries := map[string][]byte{}
	if !empty {
		entries, err := readProfileTreeFromWorktree(in.RepoPath, profilePrefix)
		if err != nil {
			return Result{}, err
		}
		remoteEntries = entries
	}

	allPaths := map[string]struct{}{}
	for p := range localEntries {
		allPaths[p] = struct{}{}
	}
	for p := range remoteEntries {
		allPaths[p] = struct{}{}
	}
	// Also consider files that were in our base commit but may now be deleted
	// on one or both sides.
	if baseCommit != "" {
		baseFiles, err := repo.FilesAtCommit(baseCommit)
		if err == nil {
			for _, p := range baseFiles {
				if strings.HasPrefix(p, profilePrefix) {
					allPaths[p] = struct{}{}
				}
			}
		}
	}

	plan := Plan{}
	pendingRepoWrites := map[string][]byte{}
	pendingLocalWrites := map[string][]byte{}

	for path := range allPaths {
		var localSHA, baseSHA, remoteSHA string
		var localData, remoteData []byte
		var localAbs string

		if le, ok := localEntries[path]; ok {
			localSHA = le.sha
			localData = le.data
			localAbs = le.abs
		}
		if baseCommit != "" {
			if data, ok, _ := repo.BlobAtCommit(baseCommit, path); ok {
				baseSHA = manifest.SHA256Bytes(data)
			}
		}
		if data, ok := remoteEntries[path]; ok {
			remoteSHA = manifest.SHA256Bytes(data)
			remoteData = data
		}

		action := manifest.Decide(localSHA, baseSHA, remoteSHA)
		if localAbs == "" {
			localAbs = repoPathToLocal(path, in.Profile, in.ClaudeDir, in.ClaudeJSON)
		}
		plan.Actions = append(plan.Actions, FileAction{
			Path: path, LocalAbs: localAbs, Action: action,
		})

		// Selective sync: only apply actions for paths in the filter.
		if in.Selective() && !in.OnlyPaths[path] {
			continue
		}

		if in.DryRun {
			continue
		}

		switch action {
		case manifest.ActionNoOp:
			// nothing
		case manifest.ActionAddRemote, manifest.ActionPush:
			pendingRepoWrites[path] = localData
		case manifest.ActionAddLocal, manifest.ActionPull:
			pendingLocalWrites[localAbs] = remoteData
		case manifest.ActionDeleteLocal:
			pendingLocalWrites[localAbs] = nil
		case manifest.ActionDeleteRemote:
			pendingRepoWrites[path] = nil
		case manifest.ActionMerge:
			merged, clean := mergeFile(path, nil, localData, remoteData)
			if !clean.Clean() {
				plan.Conflicts = append(plan.Conflicts, FileConflict{
					Path:       path,
					Conflicts:  clean.Conflicts,
					LocalData:  localData,
					RemoteData: remoteData,
				})
				continue
			}
			pendingRepoWrites[path] = merged.Merged
			if localAbs != "" {
				pendingLocalWrites[localAbs] = merged.Merged
			}
		case manifest.ActionConflict:
			plan.Conflicts = append(plan.Conflicts, FileConflict{
				Path: path,
				Conflicts: []merge.Conflict{{
					Path: path, Kind: merge.ConflictJSONDeleteMod,
					Local: jsonString(localData), Remote: jsonString(remoteData),
				}},
				LocalData:  localData,
				RemoteData: remoteData,
			})
		}
	}

	if in.DryRun {
		return Result{Plan: plan}, nil
	}

	var snapID string
	if len(pendingLocalWrites) > 0 {
		emit("snapshot", "taking pre-sync snapshot", "")
		absPaths := make([]string, 0, len(pendingLocalWrites))
		for abs := range pendingLocalWrites {
			absPaths = append(absPaths, abs)
		}
		snapRoot := filepath.Join(in.StateDir, "snapshots")
		meta, err := snapshot.Take(snapRoot, "sync", in.Profile, absPaths)
		if err != nil {
			return Result{}, fmt.Errorf("snapshot: %w", err)
		}
		snapID = meta.ID
	}

	var missingSecrets []string
	for abs, data := range pendingLocalWrites {
		if data == nil {
			_ = os.Remove(abs)
			continue
		}
		isJSON := isConfiguredJSON(jsonRules, abs)
		if isJSON {
			values, err := loadKeyringForJSON(in.Profile, data)
			if err != nil {
				return Result{}, err
			}
			restored, err := jsonfilter.Restore(data, values)
			if err != nil {
				return Result{}, err
			}
			if len(restored.Missing) > 0 {
				missingSecrets = append(missingSecrets, restored.Missing...)
				emit("redaction", "missing secrets prevent writing "+abs, abs)
				continue
			}
			data = restored.Data
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return Result{}, err
		}
	}

	for _, le := range localEntries {
		for path, raw := range le.redactions {
			if err := secrets.Store(secrets.Key(in.Profile, path), string(raw)); err != nil {
				return Result{}, fmt.Errorf("store secret %q: %w", path, err)
			}
		}
	}

	for path, data := range pendingRepoWrites {
		abs := filepath.Join(in.RepoPath, path)
		if data == nil {
			_ = os.Remove(abs)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Result{}, err
		}
		if err := writeFileAtomic(abs, data); err != nil {
			return Result{}, err
		}
	}

	// Refresh README.md so a human browsing the repo sees current state.
	profiles := listProfilesFromRepo(in.RepoPath)
	_ = writeRepoREADME(in.RepoPath, profiles, state, in.HostName)

	now := time.Now().UTC()
	for path, data := range pendingRepoWrites {
		if data == nil {
			mf.Delete(path)
		} else {
			mf.Set(path, manifest.Entry{
				SHA256: manifest.SHA256Bytes(data), Size: int64(len(data)),
				MTime: now, LastModifiedBy: in.HostUUID,
			})
		}
	}
	mf.UpdatedBy = in.HostUUID
	if err := mf.Save(manifestPath); err != nil {
		return Result{}, err
	}

	var commitSHA string
	hasChanges, err := repo.HasChanges()
	if err != nil {
		return Result{}, err
	}
	if hasChanges {
		emit("commit", "committing", "")
		if err := repo.AddAll(); err != nil {
			return Result{}, err
		}
		msg := commitMessage(in.Profile, in.HostName, plan)
		commitSHA, err = repo.Commit(msg, in.HostName, in.AuthorEmail)
		if err != nil {
			return Result{}, err
		}
		emit("push", "pushing to remote", "")
		if err := repo.Push(ctx, in.Auth); err != nil {
			return Result{}, err
		}
	}

	// Advance state.LastSyncedSHA ONLY for full syncs. Selective syncs leave
	// the base commit alone so the skipped files remain pending next time.
	if !in.Selective() {
		if newHead, err := repo.HeadSHA(); err == nil && newHead != "" {
			state.LastSyncedSHA[in.Profile] = newHead
			if err := saveHostState(in.StateDir, state); err != nil {
				return Result{}, fmt.Errorf("save state: %w", err)
			}
		}
	}

	emit("done", "sync complete", "")
	return Result{
		Plan:           plan,
		CommitSHA:      commitSHA,
		SnapshotID:     snapID,
		MissingSecrets: missingSecrets,
	}, nil
}

type localFile struct {
	abs        string
	data       []byte
	sha        string
	isJSON     bool
	redactions map[string]json.RawMessage
}

// mergeFile picks the right merge strategy based on path extension and content.
func mergeFile(path string, base, local, remote []byte) (merge.Result, merge.Result) {
	if isJSONPath(path) {
		res, err := merge.JSON(base, local, remote)
		if err != nil {
			return merge.Result{}, merge.Result{Conflicts: []merge.Conflict{{Path: path, Kind: merge.ConflictJSONValue, Local: string(local), Remote: string(remote)}}}
		}
		return res, res
	}
	if isTextPath(path) {
		res := merge.Text(string(base), string(local), string(remote))
		return res, res
	}
	res := merge.Binary(local, time.Now(), remote, time.Now())
	return res, res
}

func isJSONPath(p string) bool {
	return strings.HasSuffix(p, ".json")
}

func isTextPath(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".md", ".txt", ".yaml", ".yml", ".toml", ".sh", ".py", ".go":
		return true
	}
	return false
}

// resolveJSONRules builds abs-path → rule from the user-friendly keys in config.
func resolveJSONRules(cfg *config.Config, claudeDir, claudeJSON string) map[string]config.JSONFileRule {
	out := map[string]config.JSONFileRule{}
	home, _ := os.UserHomeDir()
	for key, rule := range cfg.JSONFiles {
		abs := key
		switch {
		case key == "~/.claude.json" && claudeJSON != "":
			abs = claudeJSON
		case strings.HasPrefix(key, "~/.claude/") && claudeDir != "":
			abs = filepath.Join(claudeDir, strings.TrimPrefix(key, "~/.claude/"))
		case strings.HasPrefix(key, "~/"):
			abs = filepath.Join(home, strings.TrimPrefix(key, "~/"))
		}
		out[abs] = rule
	}
	return out
}

func isConfiguredJSON(rules map[string]config.JSONFileRule, abs string) bool {
	_, ok := rules[abs]
	return ok
}

// loadKeyringForJSON walks data, finds placeholders, and pulls each from keychain.
func loadKeyringForJSON(profile string, data []byte) (map[string]string, error) {
	values := map[string]string{}
	placeholders := findPlaceholdersInJSON(data)
	for _, p := range placeholders {
		raw, err := secrets.Fetch(secrets.Key(profile, p))
		if err != nil {
			continue
		}
		values[p] = raw
	}
	return values, nil
}

// repoPathToLocal inverts profile-prefixed repo paths back to an abs local path.
func repoPathToLocal(repoPath, profile, claudeDir, claudeJSON string) string {
	prefix := "profiles/" + profile + "/"
	rel := strings.TrimPrefix(repoPath, prefix)
	if rel == "claude.json" {
		return claudeJSON
	}
	if after, ok := strings.CutPrefix(rel, "claude/"); ok {
		return filepath.Join(claudeDir, after)
	}
	return ""
}

func jsonString(b []byte) string {
	if b == nil {
		return ""
	}
	return string(b)
}
