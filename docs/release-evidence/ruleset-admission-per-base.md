# The ruleset test with one required check per base, observed in GitHub (2026-10-13)

What this is: the read-back of `demo/ruleset-evidence.sh b070nd/staircase-ruleset` after the fixture was rebuilt so that **each base has its own
required check and its own ruleset** (`staircase-admission-main`, `-other-key`, `-strict`; rulesets 24981040, 24981041, 24981042, no bypass
actors), the Action used by its published tag (`b070nd/stAirCase@v1.0.1`, `Installed staircase v1.0.1 (build attestation verified)` in each run). It
follows the [first run at v1.0.1](ruleset-admission-v1.0.1.md), which showed that one shared check name let a pass for `main` unlock the same commit
for `other-key`.

| PR | head -> base | the base's check | merge state read | why (from the run log) |
|---|---|---|---|---|
| #27 | pr-certified -> main | success | clean | rebuilt from 3 approved proposals, tree identical |
| #28 | pr-sealed -> main | success | **blocked** (see below) | a change certified after the fact (CAL 2), main's key, main requires CAL 2 |
| #29 | pr-sealed -> other-key | failure | blocked | the same commit; the base trusts another key |
| #30 | pr-below-strict -> strict | failure | blocked | CAL 2, below the required 4 |
| #31 | pr-no-notes -> main | failure | blocked | no change certificate |
| #32 | pr-edits-the-check -> main | failure | blocked | no change certificate: the pull request's edit of the workflow was not used |

**The case that failed before.** #28 and #29 share the head commit `e016d415132f`. The commit carries a passing `staircase-admission-main` and a
failing `staircase-admission-other-key`. A merge of #29 by `PUT /repos/b070nd/staircase-ruleset/pulls/29/merge` was refused: HTTP 405,
"Repository rule violations found. Required status check "staircase-admission-other-key" is failing."; `other-key` did not move. GitHub's own
GraphQL data for #29 lists exactly one required check, `staircase-admission-other-key`, with conclusion FAILURE. Under the shared name the same
situation merged (#22 in the previous section). The evidence script now gives each of the two pull requests the run of its own base (#28 success,
#29 failure) although both runs list both.

**Not explained, and not recorded as passing:** #28 read `blocked` although GitHub's GraphQL data listed its only required check,
`staircase-admission-main`, as SUCCESS, and the commit's overall status was FAILURE only because of #29's check for the other base. It was never
merged: the owner merged #27 and then closed #28 to #32, seeing them as unusable. The one thing #27 (clean) and #28 (blocked) differ in is that
#28's commit also carried a failing check run of another base; that is a suspicion, not an observation. The fixture was rebuilt on 2026-10-13 so that
**no commit heads two pull requests** (`pr-sealed` into `main`, `pr-wrong-signer` into `other-key`, each with its own sealed change), and the shared-head
case above stays recorded as it happened. The refusal of #29 (HTTP 405 by its own rule) is the proof that the per-base name holds.

The table below is the script's output, unedited. Rows #1 to #26 are pull requests of earlier builds.

# Admission evidence: b070nd/staircase-ruleset

Read at 2026-10-13T08:35:14Z with `ruleset-evidence.sh` (with a token); the required check of a base is `staircase-admission-<base>`. Nothing here was edited.

| PR | state | head -> base | head SHA | check | run (attempt) | mergeability | verifier |
|---|---|---|---|---|---|---|---|
| [#1](https://github.com/b070nd/staircase-ruleset/pull/1) | closed | pr-certified -> main | `b3a384fbe9c8` | no run | - (attempt -) | blocked / mergeable | - |
| [#2](https://github.com/b070nd/staircase-ruleset/pull/2) | closed | pr-no-notes -> main | `dece0eed8dea` | no run | - (attempt -) | blocked / mergeable | - |
| [#3](https://github.com/b070nd/staircase-ruleset/pull/3) | closed | pr-certified -> other-key | `b3a384fbe9c8` | no run | - (attempt -) | blocked / mergeable | - |
| [#4](https://github.com/b070nd/staircase-ruleset/pull/4) | closed | pr-certified -> strict | `b3a384fbe9c8` | no run | - (attempt -) | blocked / mergeable | - |
| [#5](https://github.com/b070nd/staircase-ruleset/pull/5) | closed | pr-edits-the-check -> main | `81b9dbc5c7cf` | no run | - (attempt -) | blocked / mergeable | - |
| [#6](https://github.com/b070nd/staircase-ruleset/pull/6) | closed | other-key -> main | `d957a1a8f054` | no run | - (attempt -) | blocked / mergeable | - |
| [#7](https://github.com/b070nd/staircase-ruleset/pull/7) | closed | strict -> main | `bd5cba0ef96b` | no run | - (attempt -) | dirty / not mergeable | - |
| [#8](https://github.com/b070nd/staircase-ruleset/pull/8) | closed | pr-certified -> main | `172db61aec1c` | no run | - (attempt -) | blocked / mergeable | - |
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
| [#20](https://github.com/b070nd/staircase-ruleset/pull/20) | merged | pr-certified -> main | `42808930ba8f` | no run | - (attempt -) | unknown / not computed | - |
| [#21](https://github.com/b070nd/staircase-ruleset/pull/21) | closed | pr-no-notes -> main | `2c368565333f` | no run | - (attempt -) | blocked / mergeable | - |
| [#22](https://github.com/b070nd/staircase-ruleset/pull/22) | merged | pr-sealed -> other-key | `c7887375adc0` | no run | - (attempt -) | unknown / not computed | - |
| [#23](https://github.com/b070nd/staircase-ruleset/pull/23) | closed | pr-below-strict -> strict | `e8116aa3b898` | no run | - (attempt -) | blocked / mergeable | - |
| [#24](https://github.com/b070nd/staircase-ruleset/pull/24) | closed | pr-edits-the-check -> main | `5f69f6805ef4` | no run | - (attempt -) | blocked / mergeable | - |
| [#25](https://github.com/b070nd/staircase-ruleset/pull/25) | merged | pr-sealed -> main | `c7887375adc0` | no run | - (attempt -) | unknown / not computed | - |
| [#26](https://github.com/b070nd/staircase-ruleset/pull/26) | closed | other-key -> main | `0bfb697a5946` | no run | - (attempt -) | blocked / mergeable | - |
| [#27](https://github.com/b070nd/staircase-ruleset/pull/27) | open | pr-certified -> main | `da3d3af8436a` | success | https://github.com/b070nd/staircase-ruleset/actions/runs/38370038811 (attempt 1) | clean / mergeable | v1.0.1 (run log) |
| [#28](https://github.com/b070nd/staircase-ruleset/pull/28) | open | pr-sealed -> main | `e016d415132f` | success | https://github.com/b070nd/staircase-ruleset/actions/runs/38370044353 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |
| [#29](https://github.com/b070nd/staircase-ruleset/pull/29) | open | pr-sealed -> other-key | `e016d415132f` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38370048859 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |
| [#30](https://github.com/b070nd/staircase-ruleset/pull/30) | open | pr-below-strict -> strict | `8ba1607a6a26` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38370055844 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |
| [#31](https://github.com/b070nd/staircase-ruleset/pull/31) | open | pr-no-notes -> main | `f14c338ab874` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38370055525 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |
| [#32](https://github.com/b070nd/staircase-ruleset/pull/32) | open | pr-edits-the-check -> main | `3ae575b49e22` | failure | https://github.com/b070nd/staircase-ruleset/actions/runs/38370060230 (attempt 1) | blocked / mergeable | v1.0.1 (run log) |

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
    "pr": 4,
    "state": "closed",
    "head": "b3a384fbe9c84cf5e14bae469df072d2fc455fa2",
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
    "state": "closed",
    "head": "42808930ba8fe5040b5e0a12b8038502fdccadfc",
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
    "pr": 21,
    "state": "closed",
    "head": "2c368565333ff4012ac45c750964352cddaead46",
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
    "pr": 22,
    "state": "closed",
    "head": "c7887375adc0b3f19106c62c67e688bc4b12fadd",
    "base": "other-key",
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
    "pr": 23,
    "state": "closed",
    "head": "e8116aa3b8985a4d781a11dbc06826080883d746",
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
    "pr": 24,
    "state": "closed",
    "head": "5f69f6805ef4662b7b3d7978ffd1a17a87a23e16",
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
    "pr": 25,
    "state": "closed",
    "head": "c7887375adc0b3f19106c62c67e688bc4b12fadd",
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
    "pr": 26,
    "state": "closed",
    "head": "0bfb697a59467eb5e2c486aa59c79415c391f7df",
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
    "pr": 27,
    "state": "open",
    "head": "da3d3af8436a7713fb02d22a129903fd4025ea6c",
    "base": "main",
    "check": "success",
    "mergeable": true,
    "mergeable_state": "clean",
    "run": {
      "id": 38370038811,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38370038811",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "da3d3af8436a7713fb02d22a129903fd4025ea6c"
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
    "pr": 28,
    "state": "open",
    "head": "e016d415132f337437077c629bf3e677ef8eb952",
    "base": "main",
    "check": "success",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38370044353,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38370044353",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "e016d415132f337437077c629bf3e677ef8eb952"
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
    "pr": 29,
    "state": "open",
    "head": "e016d415132f337437077c629bf3e677ef8eb952",
    "base": "other-key",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38370048859,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38370048859",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "e016d415132f337437077c629bf3e677ef8eb952"
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
    "pr": 30,
    "state": "open",
    "head": "8ba1607a6a26cd051b1a08f46ef583f88b064260",
    "base": "strict",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38370055844,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38370055844",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "8ba1607a6a26cd051b1a08f46ef583f88b064260"
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
    "pr": 31,
    "state": "open",
    "head": "f14c338ab874fac8059fd16965ecde1741cfc22c",
    "base": "main",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38370055525,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38370055525",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "f14c338ab874fac8059fd16965ecde1741cfc22c"
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
    "pr": 32,
    "state": "open",
    "head": "3ae575b49e2270828eca78be2dfb204e4503444a",
    "base": "main",
    "check": "failure",
    "mergeable": true,
    "mergeable_state": "blocked",
    "run": {
      "id": 38370060230,
      "url": "https://github.com/b070nd/staircase-ruleset/actions/runs/38370060230",
      "attempt": 1,
      "event": "pull_request_target",
      "path": ".github/workflows/staircase-admission.yml",
      "head_sha": "3ae575b49e2270828eca78be2dfb204e4503444a"
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
        "ruleset_id": 24981040
      },
      {
        "type": "required_status_checks",
        "parameters": {
          "strict_required_status_checks_policy": false,
          "do_not_enforce_on_create": false,
          "required_status_checks": [
            {
              "context": "staircase-admission-main"
            }
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981040
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981040
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981040
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
        "ruleset_id": 24981041
      },
      {
        "type": "required_status_checks",
        "parameters": {
          "strict_required_status_checks_policy": false,
          "do_not_enforce_on_create": false,
          "required_status_checks": [
            {
              "context": "staircase-admission-other-key"
            }
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981041
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981041
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981041
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
        "ruleset_id": 24981042
      },
      {
        "type": "required_status_checks",
        "parameters": {
          "strict_required_status_checks_policy": false,
          "do_not_enforce_on_create": false,
          "required_status_checks": [
            {
              "context": "staircase-admission-strict"
            }
          ]
        },
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981042
      },
      {
        "type": "non_fast_forward",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981042
      },
      {
        "type": "deletion",
        "ruleset_source_type": "Repository",
        "ruleset_source": "b070nd/staircase-ruleset",
        "ruleset_id": 24981042
      }
    ]
  }
]
```

## Rulesets

```json
[
  {
    "id": 24981040,
    "name": "staircase-admission-main",
    "target": "branch",
    "enforcement": "active",
    "conditions": {
      "ref_name": {
        "exclude": [],
        "include": [
          "refs/heads/main"
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
              "context": "staircase-admission-main"
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
  },
  {
    "id": 24981041,
    "name": "staircase-admission-other-key",
    "target": "branch",
    "enforcement": "active",
    "conditions": {
      "ref_name": {
        "exclude": [],
        "include": [
          "refs/heads/other-key"
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
              "context": "staircase-admission-other-key"
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
  },
  {
    "id": 24981042,
    "name": "staircase-admission-strict",
    "target": "branch",
    "enforcement": "active",
    "conditions": {
      "ref_name": {
        "exclude": [],
        "include": [
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
              "context": "staircase-admission-strict"
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
