// Package updater downloads and installs ccsync release binaries from GitHub.
package updater

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	TagName string `json:"tag_name"`
}

// LatestTag returns the latest release tag (e.g. "v0.2.0") from the ccsync
// GitHub repo. It does not require authentication.
func LatestTag() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("github api: %s", resp.Status)
	}
	var r release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("github api decode: %w", err)
	}
	if r.TagName == "" {
		return "", fmt.Errorf("no tag in latest release response")
	}
	return r.TagName, nil
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
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s",
		repoOwner, repoName, tag, asset)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}

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

	if err := extractBinary(resp.Body, tmp); err != nil {
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
