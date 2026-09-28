package gate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/b070nd/stAirCase/src/internal/llm"
)

func init() {
	Register(&secretProviderKeysGate{})
	Register(&secretKeyFileGate{})
	Register(&secretNoDuplicatesGate{})
}

// ─── secret.provider_keys ─────────────────────────────────────────────────────

type secretProviderKeysGate struct{}

func (*secretProviderKeysGate) Name() string       { return "secret.provider_keys" }
func (*secretProviderKeysGate) Category() string   { return "security" }
func (*secretProviderKeysGate) Severity() Severity { return SeverityBlock }

// Run requires, for every model in the case's topology, the provider secret its
// runtime client will request, so a run cannot die at its first LLM call.
func (*secretProviderKeysGate) Run(ctx Context) Result {
	const name = "secret.provider_keys"
	c, err := ctx.Store.GetCase(ctx.CaseID)
	if err != nil || c == nil {
		return fail(name, "security", SeverityBlock, fmt.Sprintf("case %d not found", ctx.CaseID))
	}
	topo, err := ctx.Store.GetLatestTopology(c.ProjectID)
	if err != nil {
		return fail(name, "security", SeverityBlock, "store error: "+err.Error())
	}
	if topo == nil {
		return pass(name, "security", SeverityBlock, "no topology yet (see topology.exists)")
	}
	nodes, err := ctx.Store.ListAgentNodes(topo.ID)
	if err != nil {
		return fail(name, "security", SeverityBlock, "store error: "+err.Error())
	}
	var problems, present []string
	checked := map[string]bool{}
	for _, n := range nodes {
		model := n.Model
		if model == "" {
			model = "claude-sonnet-4-6" // compile's default for agents without a model
		}
		key := llm.SecretFor(model)
		if key == "" {
			problems = append(problems, fmt.Sprintf("model %q (agent %q) has no known provider: use claude-, gpt-/o1-/o3-/o4-, gemini-, grok- or provider/model (LLM gateway)", model, n.Name))
			continue
		}
		if checked[key] {
			continue
		}
		checked[key] = true
		sec, err := ctx.Store.GetSecret(key, &c.ProjectID)
		if err != nil {
			return fail(name, "security", SeverityBlock, "store error: "+err.Error())
		}
		if sec == nil {
			problems = append(problems, fmt.Sprintf("%s missing - printf 'value' | staircase secret set %s", key, key))
			continue
		}
		scope := "global"
		if sec.ScopedToProjectID != nil {
			scope = fmt.Sprintf("project %d", *sec.ScopedToProjectID)
		}
		present = append(present, fmt.Sprintf("%s (%s)", key, scope))
	}
	if len(problems) > 0 {
		return fail(name, "security", SeverityBlock, strings.Join(problems, "; "))
	}
	return pass(name, "security", SeverityBlock, "present: "+strings.Join(present, ", "))
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
			fmt.Sprintf(".key missing at %s - run 'staircase init'", keyPath))
	}
	if info.Size() != 32 {
		return fail(name, "security", SeverityBlock,
			fmt.Sprintf(".key is %d bytes; expected 32 (AES-256 key)", info.Size()))
	}
	if perm := info.Mode().Perm(); perm&0o177 != 0 {
		return warn(name, "security",
			fmt.Sprintf(".key permissions are %04o; should be 0600 - run: chmod 0600 %s", perm, keyPath))
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
			fmt.Sprintf("duplicate project-scoped keys: %v - only the first will be used", dups))
	}
	return pass(name, "security", SeverityWarn, "no duplicate secret keys")
}
