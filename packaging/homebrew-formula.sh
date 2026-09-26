#!/usr/bin/env bash
# homebrew-formula.sh <version> <checksums.txt> <out.rb> — write the Homebrew formula
# for a release from its checksums.txt (archive names as goreleaser writes them),
# for the b070nd/homebrew-staircase tap:
#   gh release download v0.2.0 -R b070nd/stAirCase -p checksums.txt -D /tmp/sc
#   packaging/homebrew-formula.sh 0.2.0 /tmp/sc/checksums.txt ../homebrew-staircase/Formula/staircase.rb
set -euo pipefail
V="$1"; SUMS="$2"; OUT="$3"
sum() { awk -v f="staircase_${V}_$1.tar.gz" '$2 == f {print $1}' "$SUMS" | grep -E '^[0-9a-f]{64}$' || { echo "no checksum for $1" >&2; exit 1; }; }
U="https://github.com/b070nd/stAirCase/releases/download/v${V}"
cat > "$OUT" <<RB
class Staircase < Formula
  desc "Enforcement gate between AI agent plans and your codebase"
  homepage "https://github.com/b070nd/stAirCase"
  version "${V}"
  license "MIT"
  version_scheme 1 # the Go rewrite restarts at 0.x after the Bash 1.x line

  depends_on "git"

  on_macos do
    on_arm do
      url "${U}/staircase_${V}_darwin_arm64.tar.gz"
      sha256 "$(sum darwin_arm64)"
    end
    on_intel do
      url "${U}/staircase_${V}_darwin_amd64.tar.gz"
      sha256 "$(sum darwin_amd64)"
    end
  end

  on_linux do
    on_arm do
      url "${U}/staircase_${V}_linux_arm64.tar.gz"
      sha256 "$(sum linux_arm64)"
    end
    on_intel do
      url "${U}/staircase_${V}_linux_amd64.tar.gz"
      sha256 "$(sum linux_amd64)"
    end
  end

  def install
    bin.install "staircase"
    bash_completion.install "completions/staircase.bash" => "staircase"
    zsh_completion.install "completions/staircase.zsh" => "_staircase"
    fish_completion.install "completions/staircase.fish"
  end

  test do
    assert_match "stAirCase v#{version}", shell_output("#{bin}/staircase version")
    ENV["STAIRCASE_DIR"] = testpath/"ws"
    system bin/"staircase", "init"
    system bin/"staircase", "doctor"
  end
end
RB
echo "wrote $OUT"
