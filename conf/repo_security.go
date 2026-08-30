package conf

import (
	"fmt"
	"os"
	"path/filepath"
)

// Modes for everything the provider writes into $CP_PATH.
//
// The repo holds the node's identity: private_key is what proves this provider
// is this provider, and config.toml carries the sk-prov API key. Anyone who can
// read them can impersonate the node.
const (
	SecretFileMode os.FileMode = 0o600
	SecretDirMode  os.FileMode = 0o700
)

// secretFiles are created and kept owner-only.
var secretFiles = []string{
	"private_key",
	"machine_fingerprint",
	"config.toml",
	"models.json",
	EnvFileName,
}

// secretDirs are kept owner-only.
var secretDirs = []string{"keystore"}

// SecureRepo tightens permissions on an existing repo.
//
// Early versions created these world-readable, so this repairs installs that
// already exist rather than only getting new ones right — an operator has no
// reason to know their node key is readable, and nothing else would tell them.
// Each change is reported so the tightening is never silent.
func SecureRepo(cpRepoPath string) []string {
	var changed []string

	if fixed, ok := tighten(cpRepoPath, SecretDirMode); ok {
		changed = append(changed, fixed)
	}
	for _, name := range secretDirs {
		if fixed, ok := tighten(filepath.Join(cpRepoPath, name), SecretDirMode); ok {
			changed = append(changed, fixed)
		}
	}
	for _, name := range secretFiles {
		if fixed, ok := tighten(filepath.Join(cpRepoPath, name), SecretFileMode); ok {
			changed = append(changed, fixed)
		}
	}
	return changed
}

// tighten narrows a path's mode if it is readable or writable by anyone other
// than the owner. It never widens permissions.
func tighten(path string, want os.FileMode) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	perm := info.Mode().Perm()
	if perm&0o077 == 0 {
		return "", false
	}
	if err := os.Chmod(path, want); err != nil {
		return "", false
	}
	return fmt.Sprintf("%s (%04o -> %04o)", filepath.Base(path), perm, want), true
}

// GitRepoContaining walks up from dir looking for a .git entry, returning the
// working tree root that contains it.
//
// A provider repo inside a git working tree is a live hazard: .gitignore stops
// the files being committed, but `git clean -xfd` removes ignored files too,
// and private_key has no backup. Losing it loses the node's identity.
func GitRepoContaining(dir string) (string, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, ".git")); err == nil {
			return abs, true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", false
		}
		abs = parent
	}
}
