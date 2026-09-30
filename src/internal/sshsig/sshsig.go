// Package sshsig signs and checks payloads with SSH keys, through ssh-keygen
// (the format git uses for SSH-signed commits). It is how a person's identity
// is proven: on a change certificate (staircase sign) and on each decision.
package sshsig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Sign signs payload with the private key in keyFile (or the agent, for a
// public key file) in namespace, and returns the armored signature.
func Sign(keyFile, namespace string, payload []byte) ([]byte, error) {
	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-q", "-f", keyFile, "-n", namespace)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stderr = bytes.NewReader(payload), &stderr
	sig, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ssh-keygen -Y sign with %s: %w: %s", keyFile, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return sig, nil
}

var fingerprint = regexp.MustCompile(`key (\S+:\S+)`)

// Check verifies that sig is a valid signature of payload in namespace, by
// whichever key made it, and returns that key's fingerprint. It says nothing
// about who the key belongs to: see Verify.
func Check(namespace string, payload, sig []byte) (string, error) {
	f, err := tempFile(sig)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(f) }()
	cmd := exec.Command("ssh-keygen", "-Y", "check-novalidate", "-n", namespace, "-s", f)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("the signature is not valid: %s", strings.TrimSpace(string(out)))
	}
	m := fingerprint.FindStringSubmatch(string(out))
	if m == nil {
		return "", errors.New("the signature is valid but names no key")
	}
	return m[1], nil
}

// Verify checks sig against the allowed_signers file (git's format): principal
// must be listed there for the key that signed payload in namespace.
func Verify(allowedSigners, principal, namespace string, payload, sig []byte) error {
	f, err := tempFile(sig)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f) }()
	cmd := exec.Command("ssh-keygen", "-Y", "verify", "-f", allowedSigners, "-I", principal, "-n", namespace, "-s", f)
	cmd.Stdin = bytes.NewReader(payload)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s is not a trusted signer of this: %s", principal, strings.TrimSpace(string(out)))
	}
	return nil
}

func tempFile(b []byte) (string, error) {
	f, err := os.CreateTemp("", "staircase-sig-*")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	return f.Name(), nil
}
