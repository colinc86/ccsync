// Package updater downloads and installs ccsync release binaries from GitHub.
package updater

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repoOwner = "colinc86"
	repoName  = "ccsync"
)

// CurrentVersion is set by main() at startup via SetCurrentVersion. Other
// packages (notably the TUI's Settings screen) read it to display the
// running version without importing main.
var currentVersion string

// SetCurrentVersion records the binary's version string. Called once from
// main.go so the TUI can present it consistently with `ccsync --version`.
func SetCurrentVersion(v string) { currentVersion = v }

// CurrentVersion returns whatever was last passed to SetCurrentVersion, or
// "dev" if main never called it (e.g. in tests).
func CurrentVersion() string {
	if currentVersion == "" {
		return "dev"
	}
	return currentVersion
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// LatestTag returns the latest release tag (e.g. "v0.3.0") from the ccsync
// GitHub repo. Falls back to authenticated access when the public API
// returns 404 / 403 — which happens when the user has forked ccsync into
// a private repo of their own. We pick up credentials from GH_TOKEN /
// GITHUB_TOKEN env vars, or via `gh auth token` if the GitHub CLI is set
// up. Public repos need none of this.
func LatestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	r, err := fetchLatest(url, "")
	if err == nil {
		return r.TagName, nil
	}
	if !isPrivateRepoErr(err) {
		return "", err
	}
	auth, ok := authHeader()
	if !ok {
		return "", fmt.Errorf("%w (set GITHUB_TOKEN or run `gh auth login`)", err)
	}
	r, err = fetchLatest(url, auth)
	if err != nil {
		return "", err
	}
	return r.TagName, nil
}

// fetchLatest does one request to the /releases/latest endpoint. `auth`
// is used verbatim as the Authorization header when non-empty.
func fetchLatest(url, auth string) (release, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return release{}, &httpStatusErr{status: resp.StatusCode, url: url}
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return release{}, fmt.Errorf("github api decode: %w", err)
	}
	if r.TagName == "" {
		return release{}, fmt.Errorf("no tag in latest release response")
	}
	return r, nil
}

// httpStatusErr carries the non-200 status back up so the caller can
// decide whether to retry with auth. We don't want to paper over network
// errors as "maybe private" — only the specific 404/403 signal that.
type httpStatusErr struct {
	status int
	url    string
}

func (e *httpStatusErr) Error() string {
	return fmt.Sprintf("github api %s: status %d", e.url, e.status)
}

func isPrivateRepoErr(err error) bool {
	var hs *httpStatusErr
	if !errors.As(err, &hs) {
		return false
	}
	return hs.status == 401 || hs.status == 403 || hs.status == 404
}

// authHeader returns a GitHub API Authorization header from the first
// available source: GH_TOKEN, GITHUB_TOKEN, or `gh auth token`. Returns
// ok=false when none are configured, so callers can surface a clear
// "you need to set this up" error instead of retrying into the same
// failure mode with empty credentials.
func authHeader() (string, bool) {
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if t := strings.TrimSpace(os.Getenv(env)); t != "" {
			return "Bearer " + t, true
		}
	}
	if _, err := exec.LookPath("gh"); err == nil {
		out, err := exec.Command("gh", "auth", "token").Output()
		if err == nil {
			t := strings.TrimSpace(string(out))
			if t != "" {
				return "Bearer " + t, true
			}
		}
	}
	return "", false
}

// IsHomebrew reports whether `path` looks like a Homebrew-managed binary.
// Used by the update command to defer to `brew upgrade` instead of replacing
// the file out from under brew.
func IsHomebrew(path string) bool {
	return strings.Contains(path, "/Cellar/") ||
		strings.Contains(path, "/homebrew/") ||
		strings.Contains(path, "/linuxbrew/")
}

// InstallRelease downloads the ccsync release asset for `tag` matching the
// current OS/arch, extracts the binary, and atomically replaces `target`. On
// Unix, renaming over a running binary is safe — the kernel keeps the old
// inode alive for the current process.
//
// Uses the public download URL first, which is fast and auth-free for
// public repos. Falls back to the authenticated assets API when that 404s
// or 403s — same credential sources as LatestTag (GH_TOKEN, GITHUB_TOKEN,
// or `gh auth token`). A private fork / rehomed repo works the same as
// the canonical one as long as the user is logged in.
func InstallRelease(tag, target string) error {
	goos := runtime.GOOS
	if goos != "darwin" && goos != "linux" {
		return fmt.Errorf("self-update not supported on %s; reinstall manually", goos)
	}
	archName, err := archLabel(runtime.GOARCH)
	if err != nil {
		return err
	}
	version := strings.TrimPrefix(tag, "v")
	asset := fmt.Sprintf("ccsync_%s_%s_%s.tar.gz", version, goos, archName)

	body, err := openReleaseAsset(tag, asset)
	if err != nil {
		return err
	}
	defer body.Close()

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".ccsync-update-*")
	if err != nil {
		return fmt.Errorf("can't write to %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := extractBinary(body, tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	cleanup = false
	return nil
}

// openReleaseAsset returns an io.ReadCloser over the named asset's bytes
// for the given tag. Tries the public /releases/download URL first (fast
// path, auth-free). On 404/403 — which is what GitHub returns for both
// "asset doesn't exist" and "repo is private and you're not logged in" —
// falls back to resolving the asset ID via the authenticated API and
// streaming via /releases/assets/<id> with Accept: octet-stream.
func openReleaseAsset(tag, asset string) (io.ReadCloser, error) {
	directURL := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		repoOwner, repoName, tag, asset)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(directURL)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode == 200 {
		return resp.Body, nil
	}
	directStatus := resp.StatusCode
	resp.Body.Close()
	if !(directStatus == 404 || directStatus == 403) {
		return nil, fmt.Errorf("download %s: status %d", directURL, directStatus)
	}
	// Private-repo fallback — resolve asset ID, then stream via the
	// authenticated assets endpoint.
	auth, ok := authHeader()
	if !ok {
		return nil, fmt.Errorf("asset %s not publicly downloadable (status %d); set GITHUB_TOKEN or `gh auth login` and retry", asset, directStatus)
	}
	tagURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s",
		repoOwner, repoName, tag)
	rel, err := fetchLatest(tagURL, auth)
	if err != nil {
		return nil, fmt.Errorf("resolve release %s: %w", tag, err)
	}
	var assetID int64
	for _, a := range rel.Assets {
		if a.Name == asset {
			assetID = a.ID
			break
		}
	}
	if assetID == 0 {
		return nil, fmt.Errorf("asset %s not found in release %s", asset, tag)
	}
	assetURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/assets/%d",
		repoOwner, repoName, assetID)
	req, err := http.NewRequest("GET", assetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	// octet-stream tells the API to redirect us to the signed blob URL
	// rather than returning metadata JSON.
	req.Header.Set("Accept", "application/octet-stream")
	resp, err = client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download asset %d: %w", assetID, err)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, fmt.Errorf("download asset %d: status %d", assetID, resp.StatusCode)
	}
	return resp.Body, nil
}

func archLabel(arch string) (string, error) {
	switch arch {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported arch: %s", arch)
	}
}

func extractBinary(r io.Reader, out io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("ccsync binary not found in release tarball")
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(h.Name) != "ccsync" {
			continue
		}
		if _, err := io.Copy(out, tr); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		return nil
	}
}
