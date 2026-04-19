package gitx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
func (c AuthConfig) Resolve() (transport.AuthMethod, error) {
	switch c.Kind {
	case AuthNone:
		return nil, nil
	case AuthSSH:
		keyPath := c.SSHKeyPath
		if keyPath == "" {
			p, err := DiscoverSSHKey()
			if err != nil {
				return nil, err
			}
			keyPath = p
		}
		return gitssh.NewPublicKeysFromFile("git", keyPath, c.SSHPassphrase)
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
