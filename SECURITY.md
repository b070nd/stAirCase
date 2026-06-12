# Security Policy

stAirCase is a security tool: its entire purpose is to keep autonomous agents
from making unreviewed, unprovable changes to your code. We take vulnerabilities
in it seriously.

## Reporting a vulnerability

**Please do not open a public issue for security vulnerabilities.**

Report privately to **botond.biro.dev@gmail.com** (or use GitHub's
[private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
on this repository). Include a description, reproduction steps, and the affected
version or commit. We aim to acknowledge within a few business days and will
coordinate a fix and disclosure timeline with you.

## Security model

The design assumes **the agent runtime is potentially compromised**. The Go
control plane is the trust boundary; every security decision is enforced there,
never delegated to the Python side.

- **Trust boundary at the orchestrator.** The Python/LangGraph runtime speaks an
  authenticated IPC protocol over a `0600` Unix domain socket with a per-run
  token. It is never trusted to police itself — path sandboxing, approval,
  secret delivery, and audit logging are all enforced in Go.
- **Content-bound approval.** A `file_edit` approval is bound to a SHA-256 of the
  exact post-edit content. Before commit, the orchestrator re-hashes the file and
  refuses to commit (recording `approval_content_mismatch`) if the bytes differ
  from what was approved.
- **Repository path sandbox.** Approved file paths that are absolute or escape the
  project root are rejected at the commit boundary (recording
  `approval_path_escape`) — even if the agent bypasses the runtime's own checks.
- **Tamper-evident audit chain.** Every run event is hash-chained and
  Ed25519-signed. Checkpoints can be anchored in a public Rekor transparency log
  (`audit export --anchor`) and re-verified (`audit verify --check-anchor`) for an
  external, append-only witness independent of the workspace key.
- **Secret isolation.** Secrets are stored AES-256-encrypted; they are decrypted
  in Go and delivered over IPC only on request. The AES key never crosses into the
  agent runtime, cross-project requests are rejected, reserved (`__`-prefixed) keys
  are never deliverable to agents, and delivered plaintext is scrubbed from every
  log, audit, and stderr line.
- **Shell execution off by default.** `run_shell` is unavailable unless a run is
  started with `--allow-shell-exec`, enforced independently at the template, IPC,
  and policy layers; rejected attempts are audited.
- **Supply chain.** Releases ship an SBOM and a cosign (keyless, Sigstore OIDC)
  signature over the checksums; GitHub Actions are SHA-pinned.

### Known limitations (pre-1.0)

These are documented, not hidden:

- When `--allow-shell-exec` is enabled, commands run as the orchestrator OS user
  **without** an OS-level sandbox (namespaces/seccomp). Run such workloads in a
  container until native isolation lands.
- The audit chain uses raw Ed25519 over canonical JSON, not yet DSSE envelopes;
  Rekor anchoring proves log inclusion and content match but not (yet) full Merkle
  inclusion-proof verification.
- A single per-run token authenticates the agent; per-agent-persona identity and
  per-tool credential scoping are on the roadmap.

## Supported versions

stAirCase is pre-1.0; security fixes are applied to the latest release and `master`.
