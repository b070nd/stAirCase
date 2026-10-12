# The verify Action under a ruleset, observed in GitHub (2026-10-12)

What this is: the read-back of `demo/ruleset-evidence.sh b070nd/staircase-ruleset`, taken with a read-only token at 09:12 UTC on
2026-10-12, after the fixture's third build was published and its five pull requests (#13 to #17) had run. The required check is
`staircase-admission`; the ruleset (id 24917911, no bypass actors) was imported just before the read. The Action ran from the commit
`e673581` (the executable-bit fix, not yet in a release) and installed the verifier `v1.0.0`, whose build attestation it verified
(`Installed staircase v1.0.0 (build attestation verified)` in each run log).

What the rows #13 to #17 show, each from its own run and head commit:

| PR | head -> base | check | merge state | why (from the run log) |
|---|---|---|---|---|
| #13 | pr-certified -> main | success | clean | rebuilt from 3 approved proposals, tree identical, 1 commit checked |
| #14 | pr-no-notes -> main | failure | blocked | the commit has no change certificate |
| #15 | pr-sealed -> other-key | failure | blocked | no valid signature by the trusted key (the base trusts another key) |
| #16 | pr-below-strict -> strict | failure | blocked | the commit reached CAL 2, below the required 4 |
| #17 | pr-edits-the-check -> main | failure | blocked | no change certificate: the pull request's own edit of the workflow (min-cal 1, all false) was not used |

What it does not show: the merge itself. #13 is eligible (`clean`); merging it, and trying to merge #14 to #17, is the owner's step,
and this file is to be extended with that result. Rows #1 to #12 are pull requests of two earlier builds of the fixture, closed
and superseded: the first run found that the Action's script was not executable (exit 126, fixed in `e673581`), the second that
two pull requests shared a head commit and so shared their check results. The table below is the script's output, unedited.

# Admission evidence: b070nd/staircase-ruleset

Read at 2026-10-12T09:13:55Z with `ruleset-evidence.sh` (with a token); the required check is `staircase-admission`. Nothing here was edited.

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
| [#13](https://github.com/b070nd/staircase-ruleset/pull/13) | open | pr-certified -> main | `c3db5bc28aa8` | success | https://github.com/b070nd/staircase-ruleset/actions/runs/38228242852 (attempt 1) | clean / mergeable | v1.0.0 (run log) |
| [#14](https://github.com/b070nd/staircase-ruleset/pull/14) | open | pr-no-notes -> main | `87d51fee726a` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38228246496 (attempt 1) | blocked / mergeable | v1.0.0 (run log) |
| [#15](https://github.com/b070nd/staircase-ruleset/pull/15) | open | pr-sealed -> other-key | `b3a45f1e6529` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38228250518 (attempt 1) | blocked / mergeable | v1.0.0 (run log) |
| [#16](https://github.com/b070nd/staircase-ruleset/pull/16) | open | pr-below-strict -> strict | `d5ed077ee989` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38228253793 (attempt 1) | blocked / mergeable | v1.0.0 (run log) |
| [#17](https://github.com/b070nd/staircase-ruleset/pull/17) | open | pr-edits-the-check -> main | `2e83f80d77f3` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38228256424 (attempt 1) | blocked / mergeable | v1.0.0 (run log) |

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
    "state": "open",
    "head": "c3db5bc28aa804f39b3c6f715326b66dbc80c770",
    "base": "main",
    "check": "success",
    "mergeable": true,
    "mergeable_state": "clean",
    "run": {
      "id": 38228242852,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38228242852",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "c3db5bc28aa804f39b3c6f715326b66dbc80c770"
    },
    "verifier": "v1.0.0 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.0_linux_amd64.tar.gz",
        "digest": "sha256:c1a756a365ae8e648e781e8afd6461cbbccf317288335ab38897ed3a21fd9c87"
      }
    ]
  },
  {
    "pr": 14,
    "state": "open",
    "head": "87d51fee726a44eeaff33ba3f8aad545b09da36d",
    "base": "main",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38228246496,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38228246496",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "87d51fee726a44eeaff33ba3f8aad545b09da36d"
    },
    "verifier": "v1.0.0 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.0_linux_amd64.tar.gz",
        "digest": "sha256:c1a756a365ae8e648e781e8afd6461cbbccf317288335ab38897ed3a21fd9c87"
      }
    ]
  },
  {
    "pr": 15,
    "state": "open",
    "head": "b3a45f1e652963390dd9ee0ed22125bc1c57e1db",
    "base": "other-key",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38228250518,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38228250518",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "b3a45f1e652963390dd9ee0ed22125bc1c57e1db"
    },
    "verifier": "v1.0.0 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.0_linux_amd64.tar.gz",
        "digest": "sha256:c1a756a365ae8e648e781e8afd6461cbbccf317288335ab38897ed3a21fd9c87"
      }
    ]
  },
  {
    "pr": 16,
    "state": "open",
    "head": "d5ed077ee989aac7284b468768b32345a9f6f756",
    "base": "strict",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38228253793,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38228253793",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "d5ed077ee989aac7284b468768b32345a9f6f756"
    },
    "verifier": "v1.0.0 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.0_linux_amd64.tar.gz",
        "digest": "sha256:c1a756a365ae8e648e781e8afd6461cbbccf317288335ab38897ed3a21fd9c87"
      }
    ]
  },
  {
    "pr": 17,
    "state": "open",
    "head": "2e83f80d77f3e94fe5bf69e88f7c03711645921d",
    "base": "main",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38228256424,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38228256424",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "2e83f80d77f3e94fe5bf69e88f7c03711645921d"
    },
    "verifier": "v1.0.0 (run log)",
    "release_asset": [
      {
        "name": "staircase_1.0.0_linux_amd64.tar.gz",
        "digest": "sha256:c1a756a365ae8e648e781e8afd6461cbbccf317288335ab38897ed3a21fd9c87"
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
        "ruleset_id": 24917911
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
        "ruleset_id": 24917911
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24917911
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24917911
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
        "ruleset_id": 24917911
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
        "ruleset_id": 24917911
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24917911
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24917911
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
        "ruleset_id": 24917911
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
        "ruleset_id": 24917911
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24917911
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24917911
      }
    ]
  }
]
```

## Rulesets

```json
[
  {
    "id": 24917911,
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
