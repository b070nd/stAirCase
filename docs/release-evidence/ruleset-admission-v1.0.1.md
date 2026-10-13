# The verify Action at v1.0.1 under a ruleset, observed in GitHub (2026-10-12)

What this is: the read-back of `demo/ruleset-evidence.sh b070nd/staircase-ruleset`, taken with a read-only token on 2026-10-12 after the
fixture was rebuilt to use the **published** Action, `uses: b070nd/stAirCase@v1.0.1` with `version: v1.0.1` (the run logs show both, and
`Installed staircase v1.0.1 (build attestation verified)`). It follows the [first observation](ruleset-admission-2026-10-12.md), which ran the
Action from a commit before the fix was released. The ruleset (id 24927610, no bypass actors) requires a pull request and the check
`staircase-admission` on `main`, `other-key` and `strict`.

| PR | head -> base | check | merge state | why (from the run log) |
|---|---|---|---|---|
| #20 | pr-certified -> main | success | clean | rebuilt from 3 approved proposals, tree identical, 1 commit checked |
| #21 | pr-no-notes -> main | failure | blocked | the commit has no change certificate |
| #22 | pr-sealed -> other-key | failure | blocked | no valid signature by the trusted key (the base trusts another key) |
| #23 | pr-below-strict -> strict | failure | blocked | the commit reached CAL 2, below the required 4 |
| #24 | pr-edits-the-check -> main | failure | blocked | no change certificate: the pull request's own edit of the workflow (min-cal 1, all false) was not used |

**The merge.** GitHub recorded that the owner merged #20, the certified commit into `main`, on 2026-10-12 at 14:26:54Z (merge commit
`9990059`), with `staircase-admission` and `certified-range-passes` both `success` on its head: the positive control, on this build. The
earlier build's #13 was merged the same way at 09:39:32Z. The owner also armed auto-merge (squash) on #22, the wrong-signer pull request:
GitHub accepted the request, and at 2026-10-13 the pull request is still open and `blocked`, because the required check failed and cannot
pass. A merge of #21 (no certificate) by `PUT /repos/b070nd/staircase-ruleset/pulls/21/merge` on 2026-10-13 was refused: HTTP 405, "Repository
rule violations found. Required status check "staircase-admission" is failing."; the pull request stayed open, `main` did not move. The fixture was rebuilt and force-pushed on top of the earlier build's
merge (the earlier ruleset was removed for the push and imported again). The table below was read before #20 was merged, so it shows #20 as
open and `clean`.

Rows #1 to #19 are pull requests of earlier builds, closed. The table below is the script's output, unedited.

# Admission evidence: b070nd/staircase-ruleset

Read at 2026-10-12T12:32:14Z with `ruleset-evidence.sh` (with a token); the required check is `staircase-admission`. Nothing here was edited.

| PR | state | head -> base | head SHA | check | run (attempt) | mergeability | verifier |
|---|---|---|---|---|---|---|---|
| [#1](https://github.com/b070nd/staircase-ruleset/pull/1) | closed | pr-certified -> main | `b3a384fbe9c8` | no run | - (attempt -) | clean / mergeable | - |
| [#2](https://github.com/b070nd/staircase-ruleset/pull/2) | closed | pr-no-notes -> main | `dece0eed8dea` | no run | - (attempt -) | blocked / mergeable | - |
| [#3](https://github.com/b070nd/staircase-ruleset/pull/3) | closed | pr-certified -> other-key | `b3a384fbe9c8` | no run | - (attempt -) | clean / mergeable | - |
| [#4](https://github.com/b070nd/staircase-ruleset/pull/4) | closed | pr-certified -> strict | `b3a384fbe9c8` | no run | - (attempt -) | clean / mergeable | - |
| [#5](https://github.com/b070nd/staircase-ruleset/pull/5) | closed | pr-edits-the-check -> main | `81b9dbc5c7cf` | no run | - (attempt -) | blocked / mergeable | - |
| [#6](https://github.com/b070nd/staircase-ruleset/pull/6) | closed | other-key -> main | `d957a1a8f054` | no run | - (attempt -) | blocked / mergeable | - |
| [#7](https://github.com/b070nd/staircase-ruleset/pull/7) | closed | strict -> main | `bd5cba0ef96b` | no run | - (attempt -) | dirty / not mergeable | - |
| [#8](https://github.com/b070nd/staircase-ruleset/pull/8) | closed | pr-certified -> main | `172db61aec1c` | no run | - (attempt -) | clean / mergeable | - |
| [#9](https://github.com/b070nd/staircase-ruleset/pull/9) | closed | pr-no-notes -> main | `96022a5d4cfd` | no run | - (attempt -) | blocked / mergeable | - |
| [#10](https://github.com/b070nd/staircase-ruleset/pull/10) | closed | pr-sealed -> other-key | `303c18e0a64e` | no run | - (attempt -) | blocked / mergeable | - |
| [#11](https://github.com/b070nd/staircase-ruleset/pull/11) | closed | pr-sealed -> strict | `303c18e0a64e` | no run | - (attempt -) | blocked / mergeable | - |
| [#12](https://github.com/b070nd/staircase-ruleset/pull/12) | closed | pr-edits-the-check -> main | `e9419a0a2d6f` | no run | - (attempt -) | blocked / mergeable | - |
| [#13](https://github.com/b070nd/staircase-ruleset/pull/13) | merged | pr-certified -> main | `c3db5bc28aa8` | no run | - (attempt -) | unknown / not computed | - |
| [#14](https://github.com/b070nd/staircase-ruleset/pull/14) | closed | pr-no-notes -> main | `87d51fee726a` | no run | - (attempt -) | blocked / mergeable | - |
| [#15](https://github.com/b070nd/staircase-ruleset/pull/15) | closed | pr-sealed -> other-key | `b3a45f1e6529` | no run | - (attempt -) | blocked / mergeable | - |
| [#16](https://github.com/b070nd/staircase-ruleset/pull/16) | closed | pr-below-strict -> strict | `d5ed077ee989` | no run | - (attempt -) | blocked / mergeable | - |
| [#17](https://github.com/b070nd/staircase-ruleset/pull/17) | closed | pr-edits-the-check -> main | `2e83f80d77f3` | no run | - (attempt -) | blocked / mergeable | - |
| [#18](https://github.com/b070nd/staircase-ruleset/pull/18) | closed | other-key -> main | `72fb0e19f8f8` | no run | - (attempt -) | blocked / mergeable | - |
| [#19](https://github.com/b070nd/staircase-ruleset/pull/19) | closed | strict -> main | `d02d4e00308a` | no run | - (attempt -) | blocked / mergeable | - |
| [#20](https://github.com/b070nd/staircase-ruleset/pull/20) | open | pr-certified -> main | `42808930ba8f` | success | https://github.com/b070nd/staircase-ruleset/actions/runs/38249395302 (attempt 1) | clean / mergeable | v1.0.1 (run log) |
| [#21](https://github.com/b070nd/staircase-ruleset/pull/21) | open | pr-no-notes -> main | `2c368565333f` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38249409287 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |
| [#22](https://github.com/b070nd/staircase-ruleset/pull/22) | open | pr-sealed -> other-key | `c7887375adc0` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38249423532 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |
| [#23](https://github.com/b070nd/staircase-ruleset/pull/23) | open | pr-below-strict -> strict | `e8116aa3b898` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38249441626 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |
| [#24](https://github.com/b070nd/staircase-ruleset/pull/24) | open | pr-edits-the-check -> main | `5f69f6805ef4` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38249464009 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |

## Per pull request

```json
[
  {
    "pr": 1,
    "state": "closed",
    "head": "b3a384fbe9c84cf5e14bae469df072d2fc455fa2",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "clean",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 2,
    "state": "closed",
    "head": "dece0eed8deaacf3eea9f3f3c03296e7ca7ceda6",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 3,
    "state": "closed",
    "head": "b3a384fbe9c84cf5e14bae469df072d2fc455fa2",
    "base": "other-key",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "clean",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 4,
    "state": "closed",
    "head": "b3a384fbe9c84cf5e14bae469df072d2fc455fa2",
    "base": "strict",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "clean",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 5,
    "state": "closed",
    "head": "81b9dbc5c7cf01c452a789a7533c34f65853a857",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 6,
    "state": "closed",
    "head": "d957a1a8f054aaa4f6b6a0425ddc599015a05cde",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 7,
    "state": "closed",
    "head": "bd5cba0ef96b69ddaec94b2b13e261f389839168",
    "base": "main",
    "check": "no run",
    "mergeable": false,
    "mergeable_state": "dirty",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 8,
    "state": "closed",
    "head": "172db61aec1c1119ac50c78697532d0c7fa80873",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "clean",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 9,
    "state": "closed",
    "head": "96022a5d4cfd0eb2845ac955a6ea8812300db4cb",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 10,
    "state": "closed",
    "head": "303c18e0a64ea40ee659fb77df140dedbf2cdf4b",
    "base": "other-key",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 11,
    "state": "closed",
    "head": "303c18e0a64ea40ee659fb77df140dedbf2cdf4b",
    "base": "strict",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 12,
    "state": "closed",
    "head": "e9419a0a2d6f38d58b72b2c48b00f1737f122e49",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 13,
    "state": "closed",
    "head": "c3db5bc28aa804f39b3c6f715326b66dbc80c770",
    "base": "main",
    "check": "no run",
    "mergeable": null,
    "mergeable_state": "unknown",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 14,
    "state": "closed",
    "head": "87d51fee726a44eeaff33ba3f8aad545b09da36d",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 15,
    "state": "closed",
    "head": "b3a45f1e652963390dd9ee0ed22125bc1c57e1db",
    "base": "other-key",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 16,
    "state": "closed",
    "head": "d5ed077ee989aac7284b468768b32345a9f6f756",
    "base": "strict",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 17,
    "state": "closed",
    "head": "2e83f80d77f3e94fe5bf69e88f7c03711645921d",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 18,
    "state": "closed",
    "head": "72fb0e19f8f84a210d71630e1c13b5e643989211",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 19,
    "state": "closed",
    "head": "d02d4e00308a8a96e71a15cd223611f692aa01ba",
    "base": "main",
    "check": "no run",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": null,
      "url": null,
      "attempt": null,
      "event": null,
      "path": null,
      "head_sha": null
    },
    "verifier": "-",
    "release_asset": null
  },
  {
    "pr": 20,
    "state": "open",
    "head": "42808930ba8fe5040b5e0a12b8038502fdccadfc",
    "base": "main",
    "check": "success",
    "mergeable": true,
    "mergeable_state": "clean",
    "run": {
      "id": 38249395302,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38249395302",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "42808930ba8fe5040b5e0a12b8038502fdccadfc"
    },
    "verifier": "v1.0.1 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.1_linux_amd64.tar.gz",
        "digest": "sha256:65a90fa5159e50aa006bba1ab236c0d6812cdb8ef4f5a5699eec9087f1c92e91"
      }
    ]
  },
  {
    "pr": 21,
    "state": "open",
    "head": "2c368565333ff4012ac45c750964352cddaead46",
    "base": "main",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38249409287,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38249409287",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "2c368565333ff4012ac45c750964352cddaead46"
    },
    "verifier": "v1.0.1 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.1_linux_amd64.tar.gz",
        "digest": "sha256:65a90fa5159e50aa006bba1ab236c0d6812cdb8ef4f5a5699eec9087f1c92e91"
      }
    ]
  },
  {
    "pr": 22,
    "state": "open",
    "head": "c7887375adc0b3f19106c62c67e688bc4b12fadd",
    "base": "other-key",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38249423532,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38249423532",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "c7887375adc0b3f19106c62c67e688bc4b12fadd"
    },
    "verifier": "v1.0.1 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.1_linux_amd64.tar.gz",
        "digest": "sha256:65a90fa5159e50aa006bba1ab236c0d6812cdb8ef4f5a5699eec9087f1c92e91"
      }
    ]
  },
  {
    "pr": 23,
    "state": "open",
    "head": "e8116aa3b8985a4d781a11dbc06826080883d746",
    "base": "strict",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38249441626,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38249441626",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "e8116aa3b8985a4d781a11dbc06826080883d746"
    },
    "verifier": "v1.0.1 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.1_linux_amd64.tar.gz",
        "digest": "sha256:65a90fa5159e50aa006bba1ab236c0d6812cdb8ef4f5a5699eec9087f1c92e91"
      }
    ]
  },
  {
    "pr": 24,
    "state": "open",
    "head": "5f69f6805ef4662b7b3d7978ffd1a17a87a23e16",
    "base": "main",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38249464009,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38249464009",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "5f69f6805ef4662b7b3d7978ffd1a17a87a23e16"
    },
    "verifier": "v1.0.1 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.1_linux_amd64.tar.gz",
        "digest": "sha256:65a90fa5159e50aa006bba1ab236c0d6812cdb8ef4f5a5699eec9087f1c92e91"
      }
    ]
  }
]
```

## Rules in force on each base branch

```json
[
  {
    "branch": "main",
    "rules": [
      {
        "type": "pull_request",
        "parameters": {
          "required_approving_review_count": 0,
          "dismiss_stale_reviews_on_push": false,
          "required_reviewers": [],
          "require_code_owner_review": false,
          "require_last_push_approval": false,
          "required_review_thread_resolution": false,
          "require_extra_approval_for_unattributed_changes": true,
          "allowed_merge_methods": [
            "merge",
            "squash",
            "rebase"
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "required_status_checks",
        "parameters": {
          "strict_required_status_checks_policy": false,
          "do_not_enforce_on_create": false,
          "required_status_checks": [
            {
              "context": "staircase-admission"
            }
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      }
    ]
  },
  {
    "branch": "other-key",
    "rules": [
      {
        "type": "pull_request",
        "parameters": {
          "required_approving_review_count": 0,
          "dismiss_stale_reviews_on_push": false,
          "required_reviewers": [],
          "require_code_owner_review": false,
          "require_last_push_approval": false,
          "required_review_thread_resolution": false,
          "require_extra_approval_for_unattributed_changes": true,
          "allowed_merge_methods": [
            "merge",
            "squash",
            "rebase"
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "required_status_checks",
        "parameters": {
          "strict_required_status_checks_policy": false,
          "do_not_enforce_on_create": false,
          "required_status_checks": [
            {
              "context": "staircase-admission"
            }
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      }
    ]
  },
  {
    "branch": "strict",
    "rules": [
      {
        "type": "pull_request",
        "parameters": {
          "required_approving_review_count": 0,
          "dismiss_stale_reviews_on_push": false,
          "required_reviewers": [],
          "require_code_owner_review": false,
          "require_last_push_approval": false,
          "required_review_thread_resolution": false,
          "require_extra_approval_for_unattributed_changes": true,
          "allowed_merge_methods": [
            "merge",
            "squash",
            "rebase"
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "required_status_checks",
        "parameters": {
          "strict_required_status_checks_policy": false,
          "do_not_enforce_on_create": false,
          "required_status_checks": [
            {
              "context": "staircase-admission"
            }
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24927610
      }
    ]
  }
]
```

## Rulesets

```json
[
  {
    "id": 24927610,
    "name": "staircase-admission",
    "target": "branch",
    "enforcement": "active",
    "conditions": {
      "ref_name": {
        "exclude": [],
        "include": [
          "refs/heads/main",
          "refs/heads/other-key",
          "refs/heads/strict"
        ]
      }
    },
    "bypass_actors": [],
    "rules": [
      {
        "type": "pull_request",
        "parameters": {
          "required_approving_review_count": 0,
          "dismiss_stale_reviews_on_push": false,
          "required_reviewers": [],
          "require_code_owner_review": false,
          "require_last_push_approval": false,
          "required_review_thread_resolution": false,
          "require_extra_approval_for_unattributed_changes": true,
          "allowed_merge_methods": [
            "merge",
            "squash",
            "rebase"
          ]
        }
      },
      {
        "type": "required_status_checks",
        "parameters": {
          "strict_required_status_checks_policy": false,
          "do_not_enforce_on_create": false,
          "required_status_checks": [
            {
              "context": "staircase-admission"
            }
          ]
        }
      },
      {
        "type": "non_fast_forward"
      },
      {
        "type": "deletion"
      }
    ]
  }
]
```
