# Marketplace listing: checklist

For the verify Action (`action.yml` at the repository root). Nothing here lists, tags or publishes anything: listing is
the owner's step, after v1.0.0 is accepted ([readiness](v1.0.0-readiness.md)). Checked on 2026-10-07 against GitHub's
[publishing rules](https://docs.github.com/en/actions/sharing-automations/creating-actions/publishing-actions-in-github-marketplace).

## Checked now, from this repository and GitHub's public pages

| Rule | State |
|---|---|
| The repository is public | yes (`b070nd/stAirCase`, MIT licence) |
| One action metadata file at the root | yes: `action.yml` is the only `action.yml` or `action.yaml` in the repository |
| Branding | `icon: shield`, `color: green` |
| The name is not taken | `stAirCase verify`: a Marketplace search for "staircase" returns no action; no GitHub user or organisation is called `staircase-verify`. A user `staircase` exists (not this project); the name differs from it, but GitHub's form has the last word |
| Pinned usage in the documented example | [audit](../audit.md#require-certificates-on-pull-requests): the Action and `version` pinned to a release tag, `all: true`, `rebuild: true`, the trust read from the protected base |

Not stated by GitHub's page, so **not claimed**: whether a repository with workflow files (`.github/workflows`, as this one has) can be listed, and the
icon and colour rules. The listing form validates both; if it refuses, that is the answer.

## Only the owner can do

- Accept the GitHub Marketplace Developer Agreement and have two-factor authentication on the account.
- Choose the categories in the release form (proposed: Security first, Code quality second) and publish from a release.
- Do it for the accepted v1.0.0 only.

## After publishing (to be done and kept)

1. Confirm the listing URL.
2. `packaging/verify-release.sh v1.0.0` (checksums, Sigstore signature of `checksums.txt`, build attestation, and its two controls that must fail).
3. Run the documented pinned setup in a fresh protected test repository and certify and rebuild a real positive range. This is the
   same repository and run as the ruleset fixture ([testing](../testing.md)): one test repository can serve both.
