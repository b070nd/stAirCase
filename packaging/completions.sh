#!/bin/sh
# completions.sh - generate the shell completions shipped in release archives
# (goreleaser before-hook; output is gitignored).
set -eu
rm -rf completions && mkdir completions
for shell in bash zsh fish; do
  go run ./src/cmd/staircase completion "$shell" > "completions/staircase.$shell"
done
