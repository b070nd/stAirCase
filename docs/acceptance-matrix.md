# Acceptance matrix: roadmap phases 0 to 5

What each promise of the [roadmap](../ROADMAP.md) rests on today: the source that does it, the
named tests that exercise it, and what is not yet shown. It is a map, not a score: there is no
completion percentage, and a green test run is not claimed to prove more than the tests assert.
Test names were checked to exist; they are the evidence, the status words are the judgement.

**As of** v1.0.0 (`109bc6a`; master since then differs only in documentation), with the core-closeout
commits applied to the review-findings table. Update the SHA when you update a row.

Status words: **done** (built and tested as described), **partial** (built, with a stated gap),
**open** (not built, or built and not shown), **external** (needs an account, a login, a person or a
standards body, and cannot be closed by tests alone).

## Phase 0: foundations

| Promise | Status | Source | Tests and evidence | Not shown |
|---|---|---|---|---|
| Roadmap and architecture decisions | done | `ROADMAP.md`, `docs/adr/0001` to `0004` | `TestDocLinks` keeps the links true | |
| One hook bridge for every agent: `staircase hook <agent>` | done | `internal/agent/hook.go`, `cmd/staircase/hook.go` | `TestRunHook_*` (reply checks, every failure blocks, unreadable calls not sent); `TestClaudeCode_host_hook_failures` | A host that gives up on a slow hook still runs the tool (documented: the end-of-run check commits nothing unapproved) |
| Fuzz tests for the trusted core | done | `internal/orchestrator/fuzz_test.go` | `FuzzDerive`, `FuzzCleanApprovedPath`, `FuzzUniversalNewlines`; `make fuzz` explores, `make check` runs the seeds | Exploration is a manual checkpoint (`make fuzz`), not part of `make check` |

## Phase 1: put `staircase` in front of your agent

| Promise | Status | Source | Tests and evidence | Not shown |
|---|---|---|---|---|
| Zero setup: `staircase claude "task"` | done | `cmd/staircase/session.go` | `TestClaudeSession_needs_no_setup`, `_needs_agreement`, `_outside_a_repository` | |
| Claude Code supported | done | `internal/agent/claudecode.go` | Stand-in agent tests, synthetic host tests; real runs on 2.1.236 ([compatibility](compatibility.md)) | Real controls for an ungated change; captured fixtures were not kept |
| Codex CLI supported | done | `internal/agent/codex.go` | `TestCodex_edits_are_decided_first_and_command_changes_after`, `_without_its_hooks_nothing_is_kept`; real runs recorded in [compatibility](compatibility.md) | |
| Change certificate v1: `Assisted-by:` trailer, `verify`, Action, digest-only anchoring | done | `internal/certificate`, `cmd/staircase/verify.go`, `action.yml`, `internal/audit` | `TestVerify_*`, `TestAnchorCertificate_sends_digests_only`, 13 independent vectors (`verify_vectors.py`), `tests/verify_action*.bats`, `tests/ruleset_fixture.bats` | The Action has not been run in GitHub under a required check (see V2 below) |
| Lessons from rejections | done | `cmd/staircase/lessons.go`, `internal/plan` | `TestPlan_Brief_lessons`, `TestProjectLessons` | |

## Phase 2: any agent, anywhere

| Promise | Status | Source | Tests and evidence | Not shown |
|---|---|---|---|---|
| Gemini CLI | partial | `internal/agent/gemini.go` | `TestGemini_every_tool_call_is_governed`, `_without_its_hooks_nothing_is_kept`, `TestGemini_gets_a_credential_only_by_name`; real runs on 0.46.0 | One version and model; the ungated-change control is one real run in which the test, not the agent, writes the stray file ([record](release-evidence/gemini-0.46.0-ungated.md)) |
| OpenCode | partial | `internal/agent/opencode.go` | `TestOpenCode_every_tool_call_is_governed`, `_without_its_plugin_nothing_is_kept`, `_plugin_fails_closed` | One real run on OpenCode 1.18.35 (`./demo/smoke.sh opencode`: a governed write, and a rejection with its retry, verified in a fresh clone, [record](release-evidence/opencode-1.18.35.md)); only the `write` tool, one version and model, no ungated-change control, no native session resume |
| Review-after capture (Cursor, CAL 2) | done | `internal/agent/review.go`, `cmd/staircase/seal.go` (`attach` and `seal`) | `TestSeal_certifies_the_staged_changes_on_your_branch`, `TestAttach_stops_plain_commits` | |
| `staircase review <pull request>` | done | `cmd/staircase/review.go` | `TestReview_governs_changes_made_elsewhere`, `_keeps_newer_work` | |
| Templates for managed hook settings | done | `internal/agent/managed.go`, `cmd/staircase/hooktemplate.go` | `TestHookTemplate` | Not deployed to a real managed host |
| Signed approvals with an SSH key | done | `internal/orchestrator/signed.go`, `internal/sshsig` | `TestSignedApprovals`, `_a_signature_is_good_for_one_proposal`, `TestSignCheckVerify`, `TestSignContext_does_not_outlive_its_context` | |
| Agree on the task before it starts | done | `cmd/staircase/session.go` | `TestAgreement_says_what_will_happen`, `TestSession_approve_in_scope_needs_a_scope` | |
| Evaluation of fast decision models (Jev, Laya) | done | `--signal`, `internal/signal`, `tools/jeveval` (24 labelled changes) | `TestSignal_only_sends_changes_to_a_person`, `TestSignal_url_talks_to_a_local_server`, `TestClient_Evaluate` | **Laya measured** (2026-10-07, two checkpoints, 24 synthetic cases): at the 0.5 threshold it missed 11 and 9 of 12 risky changes and flagged no safe one ([results](evaluation/README.md)); **Jev through the gateway is not measured, by the owner's decision (2026-10-08)**. What a model's wrong, late or invalid answer can do is tested (`TestClient_an_invalid_answer_*`, `TestSignal_a_wrong_late_*`) and `jeveval -out` keeps a run with its provenance; see [docs/evaluation](evaluation/README.md) |

## Phase 3: scale human attention

| Promise | Status | Source | Tests and evidence | Not shown |
|---|---|---|---|---|
| The run as an explicit state machine | done | `internal/orchestrator/runner.go` (`runMoves`) | `TestRunMoves`, `TestRun_records_its_path` | Pause/resume transitions are not in the table (see continuation below) |
| Approve the task, not every step | done | `decide.go`, `internal/policy/drift.go` | `TestApproveOnEvidence_*`, `TestDrift_*`, `TestSupervisor_*`, scenario tests in `internal/policysim` | |
| Gates as evidence (`--check`, `--validator`) | done | `checks.go`, `evidence.go`, `validator.go` | `TestRun_says_what_evidence_the_commit_has`, `TestRun_require_evidence`, `TestCheckTimeout`, `TestApproveOnEvidence_checks_see_the_proposed_state` | |
| A definition of done (Stop refused until checks pass) | partial | `done.go` | `TestDone_gives_up_after_a_few_attempts`; real Codex run | Real Claude/Gemini runs of the Stop hook are not recorded |
| Guards: dependencies, hidden Unicode, secrets | done | `guard.go` | `TestGuards_send_risky_changes_to_a_person`, `TestGuards_secret_patterns`, `TestSensitive_files`, `TestNoHiddenUnicode` | |
| Decision models as signals that only make decisions stricter | done | `signal.go` | `TestSignal_only_sends_changes_to_a_person` | Laya was measured and is weak at the fixed threshold; Jev is not measured, by decision ([results](evaluation/README.md)) |
| `staircase policy test` | done | `cmd/staircase/policytest.go`, `internal/policysim` | `TestPolicyReplay`, `TestPolicyScenarios_command`, `TestRun_a_checkpoint_goes_to_a_person_who_can_approve_or_reject`, `TestRun_evidence_decides_what_is_in_scope` | Scenario people answer uniformly per question kind |
| A deeper sandbox (no network, worktree-only writes, no credential reads, no signals) | done | `internal/sandbox` | `TestShellSandbox`, `_hides_credentials`, `TestSandbox_cannot_signal_other_processes` | Landlock restricts signals only from kernel ABI 6 (documented limit G-3) |
| Agents' own commands sandboxed (Claude Code's sandbox on) | done | `internal/agent/claudecode.go` (`sandboxSettings`, in the session settings) | `claudecode_internal_test.go` | |

## Phase 4: teams without servers

| Promise | Status | Source | Tests and evidence | Not shown |
|---|---|---|---|---|
| Local background service; long runs resume; approve from a browser | partial | `internal/approvalhttp`, `cmd/staircase/serve.go`, `recover.go` | `TestServer_*` (approval API, review page), `TestRecover_*`, kill drills | A run killed before it commits can be continued (`staircase resume`, `TestResume_*`, `tests/resume_drill.bats`) or recovered. `staircase resume` continues an interrupted run for the built-in agent, resumes the vendor session of Claude Code, Gemini CLI and Codex (`agent_session` on the chain, `TestNativeResume_*`, `tests/resume_drill.bats`) and, with `--fresh-context`, continues any harness in a new grounded session (US-008, US-009, [ADR 0005](adr/0005-durable-continuation.md)); a real Claude Code resume that remembers its conversation is not verified, see [compatibility](compatibility.md) The coordinator now owns runs and sessions across a kill (US-007): owner locks, one `serve` per workspace, dead registrations removed, a dead session's proposals decide nothing |
| Attach mode: `attach` and `seal` | done | `cmd/staircase/seal.go` (`attach` and `seal`) | `TestAttach_stops_plain_commits`, `TestSeal_*` | |
| Governance repository (rules, blueprints, keys) | done | `internal/governance`, `cmd/staircase/governance.go` | `TestGovernanceUse_imports_the_teams_blueprints`, `governance_test.go` | Distribution is a pull, not an atomic rollout or revocation |
| Evidence under git refs, `verify --rebuild` in CI | done | `internal/orchestrator/ledger.go`, `certify.go` | `TestRebuild_reproduces_the_commit`, `TestRebuild_a_changed_ledger_gives_another_tree`, 25 rebuild vectors, `tests/verify_action_release.bats` | |
| Report across repositories | done | `cmd/staircase/report.go` | `TestReport_counts_every_kind_of_commit` | |

## Phase 5: a standard others can implement

| Promise | Status | Source | Tests and evidence | Not shown |
|---|---|---|---|---|
| Written specification with test vectors and independent implementations | done | `docs/spec`, `verify_vectors.py`, `rebuild_vectors.py`, `audit_chain_vectors.py` | 13 + 25 + 6 vectors, non-vacuous inventories (`tests/spec_vectors.bats`) | The independent implementations are the maintainer's own scripts, not a third party's |
| Mappings (SLSA, OWASP LLM, SP 800-218A) and a signed SLSA verification summary | done | `docs/standards.md`, `internal/vsa`, `verify --vsa-out` | `vsa_test.go`, `verify_vsa_test.go` | |
| The certificate format proposed to in-toto | **external** | `docs/spec/in-toto-predicate.md` (field table complete, checked by `TestContract_the_in_toto_draft_names_every_certificate_field`), `docs/spec/in-toto-proposal.md` (the text, prepared) | | Not sent: submitting it needs the maintainer; acceptance by in-toto is not promised |
| Compatibility promise for the six contracts | done, audited | `docs/compatibility.md`, [contracts](contracts.md) | `tests/historical.bats` (a real run of each release from v0.3.0 on, verified, rebuilt and read by today's staircase), `TestContract_*` (a field or an event the docs miss fails the build) | One run per release, made with a stand-in agent; becomes semantic versioning at 1.0 |

## Review findings carried from the closeout review

| Finding | Status | What shows it |
|---|---|---|
| C1: journal tail classification | done | `isCutJournalLine` accepts only a prefix of what `appendJournal` writes; every prefix of a real line recovers, garbage and a stray bracket without a newline are corrupt (`TestIsCutJournalLine`, `TestRecover_only_a_cut_append_is_a_torn_tail`) |
| C2: request-audit failure or oversize | done | An oversize request kept no `action_type` and made an approved large change unrecoverable (reproduced); the stub keeps it. A request that cannot be recorded is refused, not decided (`TestRecover_an_approved_proposal_too_large_to_audit_in_full`, `TestPropose_a_request_that_cannot_be_audited_is_refused`) |
| C2: upgrade limit for runs killed by v0.8.0 after their commit | documented | The procedure is in [safety](safety.md) and tested (`TestRecover_does_not_adopt_a_copy_of_the_runs_commit`); needs the owner's acknowledgement |
| C3: deadline during the audit append | done, revised contract | Consumption is the decision on the chain; expiry is checked under the append lock right before the insert, and an insert that has begun is finished ([approvals](approvals.md)); needs owner/PM disposition of the wording (`TestConsumption_*`) |
| C5: compatibility wording | done | The Gemini probe is marked historical; the governed-tool row says what a real run exercised |

## External or account-dependent checks (not closable by tests)

| Check | Needs |
|---|---|
| V1: real ungated-change controls and retained fixtures for Claude and Gemini; OpenCode | The owner's authorization, installs and logins |
| V2: the Action under a protected-repository ruleset, with a required check that blocks the merge | The owner's test repository (the fixture, `ruleset.json` and `ruleset-evidence.sh` are ready, [testing](testing.md); not run in GitHub) |
| V2: reading the retained Linux `evidence.json` and `release-gate.json` | An authenticated download |
| in-toto proposal | A person to submit it |
| Marketplace listing | The owner's GitHub account, agreement and two-factor authentication |
