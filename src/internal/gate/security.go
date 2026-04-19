package gate

import (
	"fmt"
	"os"
	"path/filepath"
)

func init() {
	Register(&secretAnthropicKeyGate{})
	Register(&secretKeyFileGate{})
	Register(&secretNoDuplicatesGate{})
}

// ─── secret.anthropic_key ─────────────────────────────────────────────────────

type secretAnthropicKeyGate struct{}

func (*secretAnthropicKeyGate) Name() string       { return "secret.anthropic_key" }
func (*secretAnthropicKeyGate) Category() string   { return "security" }
func (*secretAnthropicKeyGate) Severity() Severity { return SeverityBlock }

func (*secretAnthropicKeyGate) Run(ctx Context) Result {
	const name = "secret.anthropic_key"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	var projectID *int64
	if c != nil {
		projectID = &c.ProjectID
	}
	sec, err := ctx.Store.GetSecret("ANTHROPIC_API_KEY", projectID)
	if err != nil {
		return fail(name, "security", SeverityBlock, "store error: "+err.Error())
	}
	if sec == nil {
		return fail(name, "security", SeverityBlock,
			"ANTHROPIC_API_KEY not found — use 'staircase secret set ANTHROPIC_API_KEY <value>'")
	}
	scope := "global"
	if sec.ScopedToProjectID != nil {
		scope = fmt.Sprintf("project %d", *sec.ScopedToProjectID)
	}
	return pass(name, "security", SeverityBlock,
		fmt.Sprintf("ANTHROPIC_API_KEY present (scope: %s)", scope))
}

// ─── secret.key_file ──────────────────────────────────────────────────────────

type secretKeyFileGate struct{}

func (*secretKeyFileGate) Name() string       { return "secret.key_file" }
func (*secretKeyFileGate) Category() string   { return "security" }
func (*secretKeyFileGate) Severity() Severity { return SeverityBlock }

func (*secretKeyFileGate) Run(ctx Context) Result {
	const name = "secret.key_file"
	keyPath := filepath.Join(ctx.WsDir, ".key")
	info, err := os.Stat(keyPath)
	if err != nil {
		return fail(name, "security", SeverityBlock,
			fmt.Sprintf(".key missing at %s — run 'staircase init'", keyPath))
	}
	if info.Size() != 32 {
		return fail(name, "security", SeverityBlock,
			fmt.Sprintf(".key is %d bytes; expected 32 (AES-256 key)", info.Size()))
	}
	if perm := info.Mode().Perm(); perm&0o177 != 0 {
		return warn(name, "security",
			fmt.Sprintf(".key permissions are %04o; should be 0600 — run: chmod 0600 %s", perm, keyPath))
	}
	return pass(name, "security", SeverityBlock, ".key present, 32 bytes, mode 0600")
}

// ─── secret.no_duplicate_keys ─────────────────────────────────────────────────

type secretNoDuplicatesGate struct{}

func (*secretNoDuplicatesGate) Name() string       { return "secret.no_duplicate_keys" }
func (*secretNoDuplicatesGate) Category() string   { return "security" }
func (*secretNoDuplicatesGate) Severity() Severity { return SeverityWarn }

func (*secretNoDuplicatesGate) Run(ctx Context) Result {
	const name = "secret.no_duplicate_keys"
	c, _ := ctx.Store.GetCase(ctx.CaseID)
	if c == nil {
		return skip(name, "security", SeverityWarn, "case not found")
	}
	secrets, err := ctx.Store.ListSecretsByProject(c.ProjectID)
	if err != nil {
		return skip(name, "security", SeverityWarn, "cannot list secrets: "+err.Error())
	}
	count := map[string]int{}
	for _, s := range secrets {
		count[s.KeyName]++
	}
	var dups []string
	for k, n := range count {
		if n > 1 {
			dups = append(dups, fmt.Sprintf("%q (×%d)", k, n))
		}
	}
	if len(dups) > 0 {
		return warn(name, "security",
			fmt.Sprintf("duplicate project-scoped keys: %v — only the first will be used", dups))
	}
	return pass(name, "security", SeverityWarn, "no duplicate secret keys")
}
