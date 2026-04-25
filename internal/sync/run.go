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

	"github.com/colinc86/ccsync/internal/category"
	"github.com/colinc86/ccsync/internal/config"
	"github.com/colinc86/ccsync/internal/discover"
	"github.com/colinc86/ccsync/internal/gitx"
	"github.com/colinc86/ccsync/internal/ignore"
	"github.com/colinc86/ccsync/internal/jsonfilter"
	"github.com/colinc86/ccsync/internal/manifest"
	"github.com/colinc86/ccsync/internal/mcpextract"
	"github.com/colinc86/ccsync/internal/merge"
	"github.com/colinc86/ccsync/internal/secrets"
	"github.com/colinc86/ccsync/internal/snapshot"
	ccstate "github.com/colinc86/ccsync/internal/state"
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

	// Refuse to operate on a v0.8.x-shaped repo. The two formats are
	// incompatible — v0.9.0 never writes whole settings/config files,
	// and silently merging into an old layout would scatter ghost
	// paths through the repo with no clean recovery.
	if err := detectOldFormat(in.RepoPath); err != nil {
		return Result{}, err
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
		// Advance local HEAD + worktree to whatever the remote says. Our
		// reconciliation is a file-level three-way merge, not a git-level
		// merge — leaving local HEAD at a stale commit makes the push
		// non-fast-forward once the remote has moved, even though we've
		// already reconciled the file content. Reset is safe because any
		// unpushed local commit from a previous failed sync is orphaned,
		// not something we want to preserve (the files it references live
		// in ~/.claude and will be re-detected in the next merge pass).
		if err := repo.SyncToRemote(); err != nil {
			return Result{}, fmt.Errorf("align with remote: %w", err)
		}
	}

	// Self-heal .gitignore on repos that predate the iteration-33 fix.
	// Without it, AddAll stages ccsync.yaml.bak (the atomic-save rollback
	// sibling) and any *.tmp interrupted mid-rename, polluting every
	// clone across the fleet. Writing it only when missing keeps user
	// customisations intact on repos that already have one.
	if err := ensureRepoGitignore(in.RepoPath); err != nil {
		return Result{}, fmt.Errorf("ensure gitignore: %w", err)
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

	resolvedProfile, err := config.EffectiveProfile(in.Config, in.Profile)
	if err != nil {
		return Result{}, fmt.Errorf("resolve profile %q: %w", in.Profile, err)
	}
	var profileMatcher *ignore.Matcher
	if resolvedProfile.HasExcludes() {
		profileMatcher = ignore.New(resolvedProfile.ExcludeRules())
	}

	emit("discover", "walking local Claude config", "")
	disc, err := discover.Walk(discover.Inputs{
		ClaudeDir: in.ClaudeDir,
	}, matcher)
	if err != nil {
		return Result{}, err
	}

	jsonRules := resolveJSONRules(in.Config, in.ClaudeDir, in.ClaudeJSON)
	profilePrefix := "profiles/" + in.Profile + "/"

	localEntries := map[string]*localFile{}
	for _, e := range disc.Tracked {
		repoPath := profilePrefix + e.RelPath
		// Honour the user's content-chunk toggles. A turned-off chunk
		// (e.g. memory) means we neither push nor pull it; the discover
		// walk already returned the files, but staging stops here.
		if chunk := contentChunkForRelPath(e.RelPath); chunk != "" && !state.IsContentEnabled(chunk) {
			continue
		}
		// Profile excludes — don't read the file, don't filter it, don't
		// keyring-store any redactions it would've contained. The main loop
		// will still surface it in the plan if it exists on the remote, via
		// the ExcludedByProfile flag.
		if profileExcluded(profileMatcher, repoPath, profilePrefix) {
			continue
		}
		data, err := os.ReadFile(e.AbsPath)
		if err != nil {
			return Result{}, fmt.Errorf("read %s: %w", e.AbsPath, err)
		}
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

	// JSON-slice managed files. Each enabled slice extracts its
	// subtree from the live source file and stages a managed file
	// at profiles/<name>/<managed-path>. The diff loop treats these
	// like any other JSON file (per-key three-way merge); the apply
	// loop redirects pull writes through mcpextract.Inject so the
	// source file's machine-local keys (sessionId, theme, …) stay
	// intact.
	for _, slice := range mcpextract.Slices() {
		if !state.IsContentEnabled(slice.ContentChunk) {
			continue
		}
		repoPath := profilePrefix + slice.ManagedPath
		if profileExcluded(profileMatcher, repoPath, profilePrefix) {
			continue
		}
		sourceAbs := sliceSourceAbs(in, slice)
		if sourceAbs == "" {
			continue
		}
		sourceBytes, readErr := os.ReadFile(sourceAbs)
		if readErr != nil && !os.IsNotExist(readErr) {
			return Result{}, fmt.Errorf("read %s: %w", sourceAbs, readErr)
		}
		managed, err := mcpextract.Extract(sourceBytes, slice.JSONPath)
		if err != nil {
			return Result{}, fmt.Errorf("extract %s from %s: %w", slice.JSONPath, sourceAbs, err)
		}
		s := slice
		lf := &localFile{
			abs:          sourceAbs,
			data:         managed,
			isJSON:       true,
			managedSlice: &s,
			sha:          manifest.SHA256Bytes(managed),
		}
		localEntries[repoPath] = lf
	}

	// Load the encryption marker once per run — drives maybeEncrypt /
	// maybeDecrypt below. Missing marker == plaintext repo (default).
	encKey, err := loadRepoEncryptionKey(in.RepoPath)
	if err != nil {
		return Result{}, err
	}

	remoteEntries := map[string][]byte{}
	if !empty {
		// Walk the extends chain parent-first so leaf profile entries
		// overwrite ancestor entries for the same path. Ancestor files
		// get projected into the leaf's namespace (profiles/<leaf>/...)
		// so the rest of the sync engine stays inheritance-unaware.
		// Example: with resolvedProfile.Chain = ["work", "default"],
		// "profiles/default/claude/agents/foo.md" shows up downstream as
		// "profiles/work/claude/agents/foo.md".
		for i := len(resolvedProfile.Chain) - 1; i >= 0; i-- {
			ancestorPrefix := "profiles/" + resolvedProfile.Chain[i] + "/"
			entries, err := readProfileTreeFromWorktree(in.RepoPath, ancestorPrefix)
			if err != nil {
				return Result{}, err
			}
			for p, data := range entries {
				plain, err := maybeDecrypt(encKey, data)
				if err != nil {
					return Result{}, fmt.Errorf("decrypt %s: %w", p, err)
				}
				rel := strings.TrimPrefix(p, ancestorPrefix)
				childPath := profilePrefix + rel
				remoteEntries[childPath] = plain
			}
		}
	}

	allPaths := map[string]struct{}{}
	for p := range localEntries {
		allPaths[p] = struct{}{}
	}
	for p := range remoteEntries {
		allPaths[p] = struct{}{}
	}
	// Also consider files that were in our base commit but may now be
	// deleted on one or both sides. Scan under every profile in the
	// resolved chain (not just the leaf) and project ancestor paths into
	// the child namespace so delete-detection works for inherited files.
	if baseCommit != "" {
		baseFiles, err := repo.FilesAtCommit(baseCommit)
		if err == nil {
			for _, p := range baseFiles {
				for _, name := range resolvedProfile.Chain {
					ancestorPrefix := "profiles/" + name + "/"
					if strings.HasPrefix(p, ancestorPrefix) {
						rel := strings.TrimPrefix(p, ancestorPrefix)
						allPaths[profilePrefix+rel] = struct{}{}
						break
					}
				}
			}
		}
	}

	plan := Plan{}
	pendingRepoWrites := map[string][]byte{}
	pendingLocalWrites := map[string][]byte{}

	// managedInjects collects pending JSON-slice injections by source
	// file. Each managedInjectOp says "splice these bytes into this
	// JSONPath in <source>" — multiple ops for the same source (e.g.
	// updating both $.mcpServers and $.hooks in settings.json) compose
	// in order. The pull loop routes managed paths here instead of into
	// pendingLocalWrites so we can read the source once, splice all
	// pending changes, then write the source once with everything else
	// preserved.
	managedInjects := map[string][]managedInjectOp{}
	queueInject := func(sourceAbs string, slice *mcpextract.Slice, data []byte) {
		if sourceAbs == "" || slice == nil {
			return
		}
		// nil data → caller wants the slice cleared (delete-local).
		if data == nil {
			data = []byte("{}")
		}
		managedInjects[sourceAbs] = append(managedInjects[sourceAbs], managedInjectOp{
			JSONPath: slice.JSONPath,
			Data:     data,
		})
	}

	for path := range allPaths {
		var localSHA, baseSHA, remoteSHA string
		var localData, remoteData []byte
		var localAbs string

		if le, ok := localEntries[path]; ok {
			localSHA = le.sha
			localData = le.data
			localAbs = le.abs
		}
		var baseFromAncestor bool
		if baseCommit != "" {
			// For inherited paths (file actually lives under an ancestor
			// profile's prefix in the commit tree), the child-path lookup
			// misses. Walk the chain — child first, then ancestors — so
			// baseSHA reflects the same inheritance projection we just
			// applied to remoteEntries. Without this, a file that's been
			// inherited from default looks "brand new" to the child and
			// every sync wants to re-add it.
			for i, name := range resolvedProfile.Chain {
				probe := path
				if i > 0 {
					probe = strings.Replace(path, profilePrefix, "profiles/"+name+"/", 1)
				}
				if data, ok, _ := repo.BlobAtCommit(baseCommit, probe); ok {
					baseSHA = manifest.SHA256Bytes(data)
					baseFromAncestor = i > 0
					break
				}
			}
		}
		if data, ok := remoteEntries[path]; ok {
			remoteSHA = manifest.SHA256Bytes(data)
			remoteData = data
		}

		// If this file is absent locally AND the only base match came from
		// an ancestor profile (not this profile's own history), treat the
		// base as empty. Otherwise Decide interprets the missing local as
		// an explicit user delete against the inherited content, producing
		// a DeleteRemote or a delete-vs-modify conflict. On a machine that
		// has never had the file (e.g. a fresh work laptop with no
		// ~/.claude.json yet) we want a clean pull of the inherited data.
		if localSHA == "" && baseFromAncestor {
			baseSHA = ""
		}

		action := manifest.Decide(localSHA, baseSHA, remoteSHA)
		if localAbs == "" {
			localAbs = repoPathToLocal(path, in.Profile, in.ClaudeDir, in.ClaudeJSON)
		}
		// Stale-exclude GC: when a repo-only path (no local equivalent)
		// matches the active .syncignore, the user has excluded it since
		// the last sync. We don't want to pull it down (violates their
		// explicit intent) or surface it as a conflict. If remote still
		// holds it, DeleteRemote silently; otherwise NoOp. Profile
		// excludes stay orthogonal — they have their own path below.
		if localSHA == "" && remoteSHA != "" && matchesSyncignore(matcher, path, profilePrefix) {
			if remoteSHA != "" {
				action = manifest.ActionDeleteRemote
			} else {
				action = manifest.ActionNoOp
			}
		}
		excluded := profileExcluded(profileMatcher, path, profilePrefix)
		denied := pathDenied(state, path, profilePrefix)
		// Every path the discover walk or mcpextract step staged
		// resolves to exactly one category. Empty return ("") means
		// the path is meta (manifest.json, .syncignore, the repo
		// README, etc.) and isn't routed through a per-category
		// policy.
		rel := strings.TrimPrefix(path, profilePrefix)
		cat := category.Classify(rel)
		// Honour the content-chunk toggle. A chunk turned off in
		// Settings → content disables sync of every path that maps
		// to it, both directions. Lump into the existing "denied"
		// flag so the review/inspector flow treats it as an opt-out.
		if chunk := contentChunkForRelPath(rel); chunk != "" && !state.IsContentEnabled(chunk) {
			denied = true
		}
		plan.Actions = append(plan.Actions, FileAction{
			Path: path, LocalAbs: localAbs, Action: action,
			ExcludedByProfile: excluded,
			ExcludedByDeny:    denied,
			Category:          cat,
		})

		// Profile excludes and per-machine denies both take precedence
		// over the three-way merge. The file is invisible to this
		// machine's sync; we neither push nor pull nor delete. If it
		// already exists locally, the user can remove it by hand (or
		// lift the deny via the review screen).
		if excluded || denied {
			continue
		}

		// Selective sync: only apply actions for paths in the filter.
		if in.Selective() && !in.OnlyPaths[path] {
			continue
		}

		if in.DryRun {
			continue
		}

		// Detect managed-slice paths so pulls can be routed through
		// mcpextract.Inject (preserving the source file's other keys)
		// rather than blindly writing the slice bytes over the source.
		var managedSlice *mcpextract.Slice
		if le, ok := localEntries[path]; ok && le.managedSlice != nil {
			managedSlice = le.managedSlice
		} else if s := mcpextract.SliceByManagedPath(rel); s != nil {
			managedSlice = s
		}

		switch action {
		case manifest.ActionNoOp:
			// nothing
		case manifest.ActionAddRemote, manifest.ActionPush:
			pendingRepoWrites[path] = localData
		case manifest.ActionAddLocal, manifest.ActionPull:
			if managedSlice != nil {
				queueInject(localAbs, managedSlice, remoteData)
			} else {
				pendingLocalWrites[localAbs] = remoteData
			}
		case manifest.ActionDeleteLocal:
			if managedSlice != nil {
				queueInject(localAbs, managedSlice, nil)
			} else {
				pendingLocalWrites[localAbs] = nil
			}
		case manifest.ActionDeleteRemote:
			pendingRepoWrites[path] = nil
		case manifest.ActionMerge:
			// mtimes fuel the binary-LWW tie-break. Local: os.Stat of the
			// file on disk; remote: the manifest's last-write record.
			// Zero times mean "fall back to the other side" — safe, since
			// a missing stat/manifest entry is a rare edge.
			var localMT, remoteMT time.Time
			if localAbs != "" {
				if info, err := os.Stat(localAbs); err == nil {
					localMT = info.ModTime()
				}
			}
			if ent, ok := mf.Files[path]; ok {
				remoteMT = ent.MTime
			}
			merged, clean := mergeFile(path, nil, localData, remoteData, localMT, remoteMT)
			if !clean.Clean() {
				// First sync for this profile: no prior base to merge
				// against, so any "both sides differ" falls here. Since
				// the user is joining an existing repo, remote wins by
				// default — align local to what the shared state says,
				// then subsequent edits diverge normally with a real
				// base to merge from. Without this, every machine #2
				// fresh setup surfaces a settings.json conflict because
				// local + remote both have content with no base.
				if baseCommit == "" {
					pendingRepoWrites[path] = remoteData
					if managedSlice != nil {
						queueInject(localAbs, managedSlice, remoteData)
					} else if localAbs != "" {
						pendingLocalWrites[localAbs] = remoteData
					}
					continue
				}
				plan.Conflicts = append(plan.Conflicts, FileConflict{
					Path:       path,
					Conflicts:  clean.Conflicts,
					LocalData:  localData,
					RemoteData: remoteData,
					MergedData: merged.Merged,
					IsJSON:     isJSONPath(path),
				})
				continue
			}
			pendingRepoWrites[path] = merged.Merged
			if managedSlice != nil {
				queueInject(localAbs, managedSlice, merged.Merged)
			} else if localAbs != "" {
				pendingLocalWrites[localAbs] = merged.Merged
			}
		case manifest.ActionConflict:
			// Same first-sync treatment for structural conflicts
			// (add-vs-add-with-different-bytes, etc.).
			if baseCommit == "" {
				if remoteData != nil {
					pendingRepoWrites[path] = remoteData
					if managedSlice != nil {
						queueInject(localAbs, managedSlice, remoteData)
					} else if localAbs != "" {
						pendingLocalWrites[localAbs] = remoteData
					}
				} else if localAbs != "" {
					// Remote side absent, local deleting → delete local.
					pendingLocalWrites[localAbs] = nil
				}
				continue
			}
			plan.Conflicts = append(plan.Conflicts, FileConflict{
				Path: path,
				Conflicts: []merge.Conflict{{
					Path: path, Kind: merge.ConflictJSONDeleteMod,
					Local: jsonString(localData), Remote: jsonString(remoteData),
					LocalPresent: localData != nil, RemotePresent: remoteData != nil,
				}},
				LocalData:  localData,
				RemoteData: remoteData,
				MergedData: localData,
				IsJSON:     isJSONPath(path),
			})
		}
	}

	if in.DryRun {
		return Result{Plan: plan}, nil
	}

	var snapID string
	if len(pendingLocalWrites) > 0 || len(managedInjects) > 0 {
		emit("snapshot", "taking pre-sync snapshot", "")
		seen := map[string]struct{}{}
		var absPaths []string
		add := func(p string) {
			if p == "" {
				return
			}
			if _, ok := seen[p]; ok {
				return
			}
			seen[p] = struct{}{}
			absPaths = append(absPaths, p)
		}
		for abs := range pendingLocalWrites {
			add(abs)
		}
		// Source files (~/.claude.json, ~/.claude/settings.json) get
		// snapshotted before any managed-slice inject so a botched
		// merge can be rolled back per-file the same way regular
		// pulls can.
		for abs := range managedInjects {
			add(abs)
		}
		snapRoot := filepath.Join(in.StateDir, "snapshots")
		meta, err := snapshot.Take(snapRoot, "sync", in.Profile, absPaths)
		if err != nil {
			return Result{}, fmt.Errorf("snapshot: %w", err)
		}
		snapID = meta.ID
		// Prune after take — config drives retention; zero values fall back
		// to defaults in state.SnapshotRetention().
		keepCount, keepDays := state.SnapshotRetention()
		_ = snapshot.Prune(snapRoot, keepCount, time.Duration(keepDays)*24*time.Hour)
	}

	// Apply managed-slice injections. For each source file with
	// pending ops, read the live document, splice every queued slice
	// into its named JSONPath, and write the document back. This
	// preserves machine-local keys (sessionId, theme, oauthAccount,
	// permissions.allow, …) that aren't part of any synced slice.
	for sourceAbs, ops := range managedInjects {
		current, readErr := os.ReadFile(sourceAbs)
		if readErr != nil && !os.IsNotExist(readErr) {
			return Result{}, fmt.Errorf("read %s for inject: %w", sourceAbs, readErr)
		}
		next := current
		for _, op := range ops {
			injected, err := mcpextract.Inject(next, op.Data, op.JSONPath)
			if err != nil {
				return Result{}, fmt.Errorf("inject %s into %s: %w", op.JSONPath, sourceAbs, err)
			}
			next = injected
		}
		if err := os.MkdirAll(filepath.Dir(sourceAbs), 0o755); err != nil {
			return Result{}, err
		}
		if err := writeFileAtomic(sourceAbs, next); err != nil {
			return Result{}, fmt.Errorf("write %s: %w", sourceAbs, err)
		}
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
			// Preserve any machine-local keys the rule excludes from sync
			// (oauthAccount, userID, permissions.allow, etc.). Without
			// this, the full-file os.WriteFile below would wipe them on
			// every pull — hence "I have to re-login on work every time."
			// We also augment the preserve list with any per-machine
			// denied mcp servers so "don't pull this server's config"
			// means what the user thinks it means.
			excludes := append([]string(nil), jsonRules[abs].Exclude...)
			if abs == in.ClaudeJSON {
				for _, name := range state.DeniedMCPServers {
					excludes = append(excludes, "$.mcpServers."+name)
				}
			}
			if len(excludes) > 0 {
				existing, _ := os.ReadFile(abs)
				preserved, err := jsonfilter.PreserveLocalExcludes(data, existing, excludes)
				if err != nil {
					return Result{}, fmt.Errorf("preserve local excludes %s: %w", abs, err)
				}
				data = preserved
			}
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
		payload, err := maybeEncrypt(encKey, path, data)
		if err != nil {
			return Result{}, fmt.Errorf("encrypt %s: %w", path, err)
		}
		if err := writeFileAtomic(abs, payload); err != nil {
			return Result{}, err
		}
	}

	// Only touch README + manifest when the content writes above actually
	// produced a diff. Otherwise time.Now() inside README/manifest makes
	// every sync "dirty" and we commit no-op after no-op — the exact bug
	// v0.2.0 tried to fix but only partially. The content writes happened
	// earlier (pendingRepoWrites loop above); check HasChanges now, and
	// bail out if the merged content was identical to what was on disk.
	var commitSHA string
	hasChanges, err := repo.HasChanges()
	if err != nil {
		return Result{}, err
	}
	if hasChanges {
		profiles := listProfilesFromRepo(in.RepoPath)
		_ = writeRepoREADME(in.RepoPath, profiles, state, in.HostName)

		now := time.Now().UTC()
		for path, data := range pendingRepoWrites {
			if data == nil {
				mf.Delete(path)
				continue
			}
			// Prefer the local file's real mtime over sync time — downstream
			// machines need to tell "A edited the file an hour ago then
			// synced" from "A edited five days ago and hasn't synced since"
			// for binary LWW merges. Pre-fix every manifest entry was
			// stamped time.Now(), so LWW always picked whichever side
			// sync'd most recently, regardless of when the file was
			// actually edited. Fall back to now for paths we can't map
			// back to a local file (merged output that hasn't been
			// written yet, edge repo-only metadata).
			mt := now
			localAbs := repoPathToLocal(path, in.Profile, in.ClaudeDir, in.ClaudeJSON)
			if localAbs != "" {
				if info, err := os.Stat(localAbs); err == nil {
					mt = info.ModTime().UTC()
				}
			}
			mf.Set(path, manifest.Entry{
				SHA256: manifest.SHA256Bytes(data), Size: int64(len(data)),
				MTime: mt, LastModifiedBy: in.HostUUID,
			})
		}
		mf.UpdatedBy = in.HostUUID
		if err := mf.Save(manifestPath); err != nil {
			return Result{}, err
		}

		emit("commit", "committing", "")
		if err := repo.AddAll(); err != nil {
			return Result{}, err
		}
		msg := commitMessage(in.Profile, in.HostName, plan, remoteEntries, pendingRepoWrites)
		commitSHA, err = repo.Commit(msg, in.HostName, in.AuthorEmail)
		if err != nil {
			return Result{}, err
		}
		emit("push", "pushing to remote", "")
		if err := repo.Push(ctx, in.Auth); err != nil {
			return Result{}, err
		}
	}

	// Advance state.LastSyncedSHA ONLY for full syncs with no unresolved
	// conflicts. Selective syncs leave the base commit alone so the skipped
	// files remain pending next time. Unresolved conflicts do the same — if
	// state advanced here, the next sync would see base==remote for the
	// conflicted file (because SyncToRemote already aligned the worktree to
	// remote), classify the user's still-divergent local bytes as a plain
	// ActionPush, and silently overwrite the other machine's edit. Callers
	// that want to resolve conflicts (the TUI conflict resolver) push their
	// own commit and then advanceStateToHead; the CLI `ccsync sync` path
	// returns with conflicts pending and nothing to advance toward.
	if !in.Selective() && len(plan.Conflicts) == 0 {
		if newHead, err := repo.HeadSHA(); err == nil && newHead != "" {
			state.LastSyncedSHA[in.Profile] = newHead
			if state.LastSyncedAt == nil {
				state.LastSyncedAt = map[string]time.Time{}
			}
			state.LastSyncedAt[in.Profile] = time.Now().UTC()
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
	// managedSlice marks this entry as a JSON-slice managed file
	// (.ccsync.mcp.json, ccsync.mcp.json, ccsync.hooks.json). When
	// set, abs points at the underlying source file (~/.claude.json
	// or ~/.claude/settings.json) rather than at a real on-disk
	// equivalent of the managed file. Pulls inject the managed bytes
	// back into abs at slice.JSONPath; the managed file itself is
	// only ever materialised inside the repo, never on the user's
	// disk.
	managedSlice *mcpextract.Slice
}

// managedInjectOp is one pending splice operation against a source
// JSON file. The pull loop appends one of these per managed slice
// that needs writing back to its source; the apply loop reads the
// source once, plays the ops in order, and writes the source once.
type managedInjectOp struct {
	JSONPath string
	Data     []byte
}

// sliceSourceAbs returns the absolute path of the live source file
// that the given slice extracts from. ".claude.json" maps to
// in.ClaudeJSON; ".claude/settings.json" lives under in.ClaudeDir.
func sliceSourceAbs(in Inputs, s mcpextract.Slice) string {
	switch s.SourcePath {
	case ".claude.json":
		return in.ClaudeJSON
	case ".claude/settings.json":
		return filepath.Join(in.ClaudeDir, "settings.json")
	}
	return ""
}

// contentChunkForRelPath maps a repo-relative path (already stripped
// of the profiles/<name>/ prefix) to its content-chunk identifier so
// the orchestrator can honour state.IsContentEnabled before staging
// or applying it. Empty return means the path is not gated by a
// content toggle (meta files like manifest.json fall through).
func contentChunkForRelPath(rel string) string {
	switch rel {
	case "claude/CLAUDE.md":
		return ccstate.ContentChunkClaudeMD
	case mcpextract.Slices()[0].ManagedPath:
		return ccstate.ContentChunkMCPClaudeJSON
	case mcpextract.Slices()[1].ManagedPath:
		return ccstate.ContentChunkMCPSettingsJSON
	case mcpextract.Slices()[2].ManagedPath:
		return ccstate.ContentChunkHooksWiring
	}
	switch {
	case strings.HasPrefix(rel, "claude/agents/"):
		return ccstate.ContentChunkAgents
	case strings.HasPrefix(rel, "claude/skills/"):
		return ccstate.ContentChunkSkills
	case strings.HasPrefix(rel, "claude/commands/"):
		return ccstate.ContentChunkCommands
	case strings.HasPrefix(rel, "claude/hooks/"):
		return ccstate.ContentChunkHooks
	case strings.HasPrefix(rel, "claude/output-styles/"):
		return ccstate.ContentChunkOutputStyles
	case strings.HasPrefix(rel, "claude/memory/"):
		return ccstate.ContentChunkMemory
	}
	return ""
}

// mergeFile picks the right merge strategy based on path extension and content.
// localMTime / remoteMTime are only used on the binary fallback path for
// last-write-wins tie-breaking; JSON and text merges are deterministic and
// ignore them. Either may be the zero Time, in which case the other side
// wins by definition (zero.After(non-zero) is false).
func mergeFile(path string, base, local, remote []byte, localMTime, remoteMTime time.Time) (merge.Result, merge.Result) {
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
	res := merge.Binary(local, localMTime, remote, remoteMTime)
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

// loadKeyringForJSON walks data, finds placeholders, and pulls each from
// keychain. Each placeholder carries the profile it was redacted under, so
// inherited content (e.g. a work profile pulling default's claude.json)
// looks up secrets under the original redacting profile rather than the
// active one. Falls back to the active profile to keep older redactions
// addressable if the keychain key format ever drifts.
func loadKeyringForJSON(profile string, data []byte) (map[string]string, error) {
	values := map[string]string{}
	for _, ref := range findPlaceholdersInJSON(data) {
		key := secrets.Key(ref.Profile, ref.Path)
		raw, err := secrets.Fetch(key)
		if err != nil && ref.Profile != profile {
			raw, err = secrets.Fetch(secrets.Key(profile, ref.Path))
		}
		if err != nil {
			continue
		}
		values[ref.Path] = raw
	}
	return values, nil
}

// repoPathToLocal inverts profile-prefixed repo paths back to an abs local path.
//
// Managed slice files (.ccsync.mcp.json, ccsync.mcp.json,
// ccsync.hooks.json) resolve to the *source file* they're extracted
// from — ~/.claude.json or ~/.claude/settings.json — rather than to a
// matching disk path under ~/.claude. Pulls and conflict resolutions
// for these paths inject through mcpextract.Inject before writing.
func repoPathToLocal(repoPath, profile, claudeDir, claudeJSON string) string {
	prefix := "profiles/" + profile + "/"
	rel := strings.TrimPrefix(repoPath, prefix)
	if slice := mcpextract.SliceByManagedPath(rel); slice != nil {
		switch slice.SourcePath {
		case ".claude.json":
			return claudeJSON
		case ".claude/settings.json":
			return filepath.Join(claudeDir, "settings.json")
		}
	}
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

// pathDenied reports whether a repo path is on this machine's per-user
// denylist (state.DeniedPaths). Denied paths are machine-local: they
// take the same "ignore at sync time" shape as profile excludes but
// don't propagate across machines via the repo.
func pathDenied(st *ccstate.State, repoPath, profilePrefix string) bool {
	if st == nil {
		return false
	}
	rel := strings.TrimPrefix(repoPath, profilePrefix)
	if rel == repoPath {
		return false
	}
	return st.IsPathDenied(rel)
}

// profileExcluded reports whether a repo path (profiles/<name>/<rel>) matches
// the active profile's exclude rules. Rules are written relative to the
// sync-repo tree (e.g. "claude/agents/secret-*.md"), so we strip the profile
// prefix before testing.
func profileExcluded(m *ignore.Matcher, repoPath, profilePrefix string) bool {
	if m == nil {
		return false
	}
	rel := strings.TrimPrefix(repoPath, profilePrefix)
	if rel == repoPath { // path didn't carry the profile prefix
		return false
	}
	return m.Matches(rel)
}

// ensureRepoGitignore writes the default .gitignore at the repo root if
// it isn't already present. Only writes when missing — never overwrites
// an existing file, so users who want to broaden the ignore list can do
// so without the next sync wiping their changes. See config.DefaultGitignore
// for the rationale on what's excluded and why.
func ensureRepoGitignore(repoPath string) error {
	path := filepath.Join(repoPath, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, config.DefaultGitignore(), 0o644)
}

// matchesSyncignore reports whether a repo path (profiles/<name>/<rel>)
// would be skipped by discover.Walk under the active .syncignore. The
// matcher is configured with rules relative to ~/.claude (e.g. "projects/
// ", "cache-*.dat"), which in repo-space map to paths under
// "profiles/<profile>/claude/". We strip the "profiles/<profile>/claude/"
// prefix and test the remainder; anything outside that prefix (e.g. a
// "profiles/<profile>/claude.json" root file, or repo-level metadata) is
// never syncignore-matched here.
func matchesSyncignore(m *ignore.Matcher, repoPath, profilePrefix string) bool {
	if m == nil {
		return false
	}
	rel := strings.TrimPrefix(repoPath, profilePrefix)
	if rel == repoPath {
		return false
	}
	// Only the "claude/" subtree is walked by discover — the "claude.json"
	// root file and any future repo-level paths bypass syncignore.
	claudeRel, ok := strings.CutPrefix(rel, "claude/")
	if !ok {
		return false
	}
	return m.Matches(claudeRel)
}
