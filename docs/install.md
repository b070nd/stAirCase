# Install

stAirCase is one program, `staircase`. It needs **git**; nothing else (no Python, no
database server).

| Platform | Status |
|---|---|
| macOS (Apple silicon, Intel) | supported |
| Linux (amd64, arm64) | supported |
| Windows (amd64) | experimental and untested; `run_shell` and `--agent claude-code` need a POSIX shell |

## Choose a way to install

**Homebrew** (macOS, Linux) - also installs shell completion:

```bash
brew install b070nd/staircase/staircase
```

**Go** (Go 1.26 or newer):

```bash
go install github.com/b070nd/stAirCase/src/cmd/staircase@latest
```

**Download a release archive** from the
[releases page](https://github.com/b070nd/stAirCase/releases/latest), check it (see
below), unpack it and put `staircase` on your `PATH`.

**Build from source:**

```bash
git clone https://github.com/b070nd/stAirCase.git
cd stAirCase
CGO_ENABLED=0 go build -o staircase ./src/cmd/staircase
```

## Check that it works

```bash
staircase version
staircase init      # creates the workspace (~/.staircase-workspace)
staircase doctor    # checks the workspace, keys, database and git
```

## Verify a download

Every release is built from its tag by a public GitHub workflow. The build is
reproducible: the same tag always gives the same bytes. Each release includes:

- `checksums.txt` - the SHA-256 of every archive;
- a signature over `checksums.txt`, made with [Sigstore](https://www.sigstore.dev/)
  cosign "keyless" signing (no key to manage: the signature proves which workflow
  made it);
- an SBOM (`*.spdx.json`, the list of everything inside) for every archive;
- a build-provenance attestation for every archive.

Check the signature, then the archive:

```bash
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/b070nd/stAirCase/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt

shasum -a 256 -c checksums.txt --ignore-missing
```

Release **v0.2.0** has `checksums.txt.sig` and `checksums.txt.pem` instead of the
bundle. For it, replace `--bundle checksums.txt.sigstore.json` with
`--signature checksums.txt.sig --certificate checksums.txt.pem`.

With the GitHub CLI you can also check who built an archive:

```bash
gh attestation verify staircase_0.2.0_darwin_arm64.tar.gz --repo b070nd/stAirCase
```

## Shell completion

Homebrew installs it for you. Otherwise, generate it for your shell:

```bash
staircase completion zsh  > "${fpath[1]}/_staircase"   # zsh
staircase completion bash > /usr/local/etc/bash_completion.d/staircase
staircase completion fish > ~/.config/fish/completions/staircase.fish
```

## Upgrade and uninstall

```bash
brew upgrade b070nd/staircase/staircase    # or: go install …@latest again
brew uninstall staircase
```

Uninstalling does not delete your workspace. It is a normal folder; remove
`~/.staircase-workspace` yourself if you no longer need its runs and evidence.

Next: [Getting started](../QUICKSTART.md).
