# Canonical formula for the rchekalov/homebrew-silo tap.
#
# This file lives at scripts/homebrew/silo.rb in rchekalov/silo. On every
# tagged release, .github/workflows/release.yml copies it into the tap's
# Formula/silo.rb and seds in the real version/url/sha256 — so the tap is
# always structurally in sync with source.
#
# Users install with (the three-part form avoids a name collision with
# the homebrew-cask `silo` cask, which is an unrelated macOS app):
#   brew install rchekalov/silo/silo

class Silo < Formula
  desc "Run dev tools in isolated Apple Container VMs"
  homepage "https://github.com/rchekalov/silo"
  url "https://github.com/rchekalov/silo/releases/download/v0.4.0/silo-0.4.0-macos-arm64.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "Apache-2.0"
  version "0.4.0"

  depends_on :macos
  depends_on arch: :arm64
  depends_on macos: :tahoe # macOS 26

  def install
    bin.install "bin/silo"
    (lib/"silo").install Dir["lib/silo/*"]
  end

  def caveats
    <<~EOS
      Add silo shims to your PATH (so `python`, `npm`, etc. route through silo):
        eval "$(silo shellenv)"                              # current shell
        echo 'eval "$(silo shellenv)"' >> ~/.zshrc           # permanent (zsh)
        echo 'eval "$(silo shellenv)"' >> ~/.bashrc          # permanent (bash)

      First `silo install` downloads a ~285 MB prebuilt runtime (one-time, ~30 seconds).
      Without network access it falls back to building from source (~5 minutes).
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/silo --version")
  end
end
