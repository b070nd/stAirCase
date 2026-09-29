// Package governance installs a team's rules and trusted keys from a
// governance repository into a workspace, pinned to an exact commit (ROADMAP
// phase 4: git as the control plane). The repository holds:
//
//	policy.json      the rules and limits every run uses (required)
//	allowed_signers  people trusted for two-person review (optional)
//	keys/*.pub       team members' workspace signing keys (optional)
package governance

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/crypto"
	"github.com/b070nd/stAirCase/src/internal/policy"
)

// Files in the workspace.
const (
	PinFile        = "governance.json" // where the rules came from
	AllowedSigners = "allowed_signers"
	TrustedKeysDir = "trusted-keys"
	checkoutDir    = "governance-repo"
)

// Pin is where the workspace's rules came from, and the digest of each file
// installed from there.
type Pin struct {
	Source string            `json:"source"`
	Ref    string            `json:"ref"`
	Commit string            `json:"commit"`
	Files  map[string]string `json:"files"` // workspace path → hex SHA-256
}

// State is a pin compared with its source and the workspace.
type State struct {
	Pin      Pin
	Latest   string   // the ref's commit in the source now
	Behind   bool     // the source has moved on
	Modified []string // installed files changed in the workspace since
}

// Use fetches source, reads the bundle at ref's commit, checks it, and
// installs it in the workspace wsDir. Nothing changes when a check fails.
func Use(wsDir, source, ref string) (Pin, error) {
	commit, err := fetch(wsDir, source, ref)
	if err != nil {
		return Pin{}, err
	}
	dir := filepath.Join(wsDir, checkoutDir)
	show := func(p string) ([]byte, error) { return git(dir, "show", commit+":"+p) }
	pol, err := show("policy.json")
	if err != nil {
		return Pin{}, fmt.Errorf("%s has no policy.json at %.12s", source, commit)
	}
	tmp, err := os.CreateTemp("", "governance-policy-*.json")
	if err != nil {
		return Pin{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, _ = tmp.Write(pol)
	_ = tmp.Close()
	if _, err := policy.LoadEngineFile(tmp.Name()); err != nil { // the rules runs would load, strictly
		return Pin{}, fmt.Errorf("policy.json at %.12s: %w", commit, err)
	}
	signers, err := show(AllowedSigners) // optional
	if err != nil {
		signers = nil // git prints nothing, not nil, for a missing file
	}
	keys := map[string][]byte{}
	if out, err := git(dir, "ls-tree", "--name-only", commit, "keys/"); err == nil {
		for _, p := range strings.Fields(string(out)) {
			if path.Ext(p) != ".pub" {
				continue
			}
			k, err := show(p)
			if err != nil || len(k) != ed25519.PublicKeySize {
				return Pin{}, fmt.Errorf("%s at %.12s is not a raw Ed25519 public key (a workspace's .signing.pub)", p, commit)
			}
			keys[path.Base(p)] = k
		}
	}

	pin := Pin{Source: source, Ref: ref, Commit: commit, Files: map[string]string{}}
	write := func(name string, b []byte) error {
		pin.Files[name] = digest(b)
		return writeAtomic(filepath.Join(wsDir, name), b)
	}
	if err := write("policy.json", pol); err != nil {
		return Pin{}, err
	}
	if priv, err := crypto.LoadSigningKey(wsDir); err == nil { // runs refuse a policy whose signature does not match
		if err := os.WriteFile(filepath.Join(wsDir, policy.PolicySigFile), []byte(crypto.Sign(priv, pol)), 0o644); err != nil {
			return Pin{}, err
		}
	}
	if signers != nil {
		if err := write(AllowedSigners, signers); err != nil {
			return Pin{}, err
		}
	} else {
		_ = os.Remove(filepath.Join(wsDir, AllowedSigners))
	}
	if err := os.RemoveAll(filepath.Join(wsDir, TrustedKeysDir)); err != nil {
		return Pin{}, err
	}
	for name, k := range keys {
		if err := write(filepath.Join(TrustedKeysDir, name), k); err != nil {
			return Pin{}, err
		}
	}
	b, _ := json.MarshalIndent(pin, "", "  ")
	return pin, writeAtomic(filepath.Join(wsDir, PinFile), b)
}

// Status compares the workspace's pin with its source (fetched now) and with
// the files in the workspace.
func Status(wsDir string) (State, error) {
	b, err := os.ReadFile(filepath.Join(wsDir, PinFile))
	if err != nil {
		return State{}, errors.New("this workspace does not use a governance repository (staircase governance use <repository>)")
	}
	var st State
	if err := json.Unmarshal(b, &st.Pin); err != nil {
		return State{}, fmt.Errorf("%s: %w", PinFile, err)
	}
	if st.Latest, err = fetch(wsDir, st.Pin.Source, st.Pin.Ref); err != nil {
		return st, err
	}
	st.Behind = st.Latest != st.Pin.Commit
	for _, name := range slices.Sorted(maps.Keys(st.Pin.Files)) {
		cur, err := os.ReadFile(filepath.Join(wsDir, name))
		if err != nil || digest(cur) != st.Pin.Files[name] {
			st.Modified = append(st.Modified, filepath.ToSlash(name))
		}
	}
	return st, nil
}

// TrustedKeys are the keys whose certificates a workspace accepts: its own
// and its team's (keys/ of the governance repository).
func TrustedKeys(wsDir string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	if own, err := crypto.LoadSigningPublicKey(wsDir); err == nil {
		keys = append(keys, own)
	}
	entries, _ := os.ReadDir(filepath.Join(wsDir, TrustedKeysDir))
	for _, e := range entries {
		k, err := os.ReadFile(filepath.Join(wsDir, TrustedKeysDir, e.Name()))
		if err != nil || len(k) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%s/%s is not an Ed25519 public key", TrustedKeysDir, e.Name())
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, errors.New("no trusted key: run 'staircase init', or pass --key")
	}
	return keys, nil
}

// fetch brings the source into the workspace's checkout and returns the
// commit ref names there.
func fetch(wsDir, source, ref string) (string, error) {
	dir := filepath.Join(wsDir, checkoutDir)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if out, err := exec.Command("git", "-c", "core.hooksPath=/dev/null", "clone", "--quiet", "--no-checkout", source, dir).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git clone %s: %w: %s", source, err, strings.TrimSpace(string(out)))
		}
	} else {
		if _, err := git(dir, "remote", "set-url", "origin", source); err != nil {
			return "", err
		}
		if _, err := git(dir, "fetch", "--quiet", "--prune", "origin"); err != nil {
			return "", fmt.Errorf("git fetch %s: %w", source, err)
		}
	}
	out, err := git(dir, "rev-parse", "--verify", "--quiet", "origin/"+ref+"^{commit}")
	if err != nil {
		if out, err = git(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil { // a tag or commit id
			return "", fmt.Errorf("%s has no branch, tag or commit %q", source, ref)
		}
	}
	return strings.TrimSpace(string(out)), nil
}

func git(dir string, args ...string) ([]byte, error) {
	return exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null"}, args...)...).Output()
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func writeAtomic(name string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		return err
	}
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}
