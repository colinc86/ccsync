package gitx

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type AuthKind int

const (
	AuthNone AuthKind = iota
	AuthSSH
	AuthHTTPS
)

func (k AuthKind) String() string {
	switch k {
	case AuthNone:
		return "none"
	case AuthSSH:
		return "ssh"
	case AuthHTTPS:
		return "https"
	}
	return "unknown"
}

// AuthConfig describes how to authenticate with a remote.
type AuthConfig struct {
	Kind          AuthKind
	SSHKeyPath    string // empty → auto-discover in ~/.ssh
	SSHPassphrase string
	HTTPSUser     string // empty → "x-access-token"
	HTTPSToken    string
}

// Resolve turns the config into a go-git AuthMethod.
//
// Important: never leak a typed-nil AuthMethod. If construction fails on
// any branch, return an explicitly-nil interface + the error. Callers that
// ignore the error and use the first return value would otherwise get a
// non-nil interface wrapping a nil pointer, which go-git method-calls and
// segfaults on (happened in v0.1.0 bootstrap for passphrase-protected keys).
func (c AuthConfig) Resolve() (transport.AuthMethod, error) {
	switch c.Kind {
	case AuthNone:
		return nil, nil
	case AuthSSH:
		// Prefer ssh-agent when available — it's what the user's shell
		// `git clone` uses, so honoring the agent matches their existing
		// setup on multi-key machines. File-based discovery can't read
		// ~/.ssh/config for IdentityFile directives and will happily pick
		// a key that doesn't match the remote (github will then reject
		// with "no supported methods"). Only fall back to file-based when
		// no agent is running. An explicit SSHKeyPath overrides both.
		if c.SSHKeyPath == "" {
			// 1Password's SSH agent (and similar: keepassxc, secretive)
			// configures itself via ~/.ssh/config's IdentityAgent rather
			// than setting SSH_AUTH_SOCK. go-git doesn't read ssh_config,
			// so bridge the gap. Always override SSH_AUTH_SOCK when an
			// IdentityAgent is configured — macOS's launchd-started
			// default ssh-agent usually has SSH_AUTH_SOCK pointing at an
			// empty listener, which beats 1Password to the draw if we
			// only read IdentityAgent on empty SSH_AUTH_SOCK.
			if sock := sshConfigIdentityAgent(); sock != "" {
				_ = os.Setenv("SSH_AUTH_SOCK", sock)
			}
			if agent, err := gitssh.NewSSHAgentAuth("git"); err == nil {
				return agent, nil
			}
		}
		keyPath := c.SSHKeyPath
		if keyPath == "" {
			p, err := DiscoverSSHKey()
			if err != nil {
				return nil, err
			}
			keyPath = p
		}
		pk, err := gitssh.NewPublicKeysFromFile("git", keyPath, c.SSHPassphrase)
		if err != nil {
			// Passphrase-protected keys without a cached passphrase fail
			// here. Last-ditch: re-try the agent even if SSHKeyPath was
			// set, so a misconfigured setting doesn't lock the user out.
			if agent, aerr := gitssh.NewSSHAgentAuth("git"); aerr == nil {
				return agent, nil
			}
			return nil, err
		}
		return pk, nil
	case AuthHTTPS:
		if c.HTTPSToken == "" {
			return nil, errors.New("HTTPS auth requires a token")
		}
		user := c.HTTPSUser
		if user == "" {
			user = "x-access-token"
		}
		return &githttp.BasicAuth{Username: user, Password: c.HTTPSToken}, nil
	}
	return nil, fmt.Errorf("unknown auth kind: %d", c.Kind)
}

// sshConfigIdentityAgent scans ~/.ssh/config for any IdentityAgent directive
// and returns its resolved path, or "" when no such directive is set.
//
// We deliberately ignore host-pattern scoping: ccsync only talks to one
// remote (the sync repo), and users configure IdentityAgent either
// globally or for their git host specifically — both cases should point
// at the same agent for our purposes. Returning the first match we see
// keeps the logic simple and matches what real users want 99% of the
// time (1Password, keepassxc, and similar set a single agent).
//
// Includes basic ~ expansion and quote stripping. Unknown ssh_config
// tokens like %h or %u aren't supported — users with those should set
// SSHKeyPath explicitly or pre-export SSH_AUTH_SOCK.
func sshConfigIdentityAgent() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".ssh", "config")
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := scanner.Text()
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// ssh_config tokenizes on whitespace OR "=" for key=value.
		kv := strings.SplitN(line, " ", 2)
		if len(kv) < 2 {
			kv = strings.SplitN(line, "=", 2)
		}
		if len(kv) < 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		if key != "identityagent" {
			continue
		}
		val := strings.TrimSpace(kv[1])
		val = strings.Trim(val, `"`)
		if strings.HasPrefix(val, "~") {
			val = home + val[1:]
		}
		return val
	}
	return ""
}

// DiscoverSSHKey returns the first default SSH key path found in ~/.ssh.
func DiscoverSSHKey() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ecdsa"} {
		p := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no SSH private key found in ~/.ssh (looked for id_ed25519, id_rsa, id_ecdsa)")
}
