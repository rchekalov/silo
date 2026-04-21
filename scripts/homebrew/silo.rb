# Seed formula for the rchekalov/homebrew-silo tap.
#
# Setup (one-time):
#   1. Create the GitHub repo rchekalov/homebrew-silo (empty, public).
#   2. Copy this file to Formula/silo.rb in that repo and push.
#   3. Create a fine-grained GitHub token with contents:write on the tap repo,
#      store it as TAP_GITHUB_TOKEN on this (main) repo under:
#         Settings -> Secrets and variables -> Actions.
#   4. Tag v0.4.0 (or later) on this repo.
#      The release workflow will auto-bump version/url/sha256 in Formula/silo.rb.
#
# Users install with (the three-part form avoids a name collision with
# the homebrew-cask `silo` cask, which is an unrelated macOS app):
#   brew install rchekalov/silo/silo

class Silo < Formula
  desc "Run dev tools in isolated Apple Container VMs"
  homepage "https://github.com/rchekalov/silo"
  url "https://github.com/rchekalov/silo/archive/refs/tags/v0.4.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "Apache-2.0"
  version "0.4.0"

  depends_on :macos
  depends_on arch: :arm64
  depends_on macos: :tahoe # macOS 26
  depends_on "go" => :build
  depends_on "swift" => :build

  def install
    system "make", "release-bundle",
           "PREFIX=#{prefix}",
           "VERSION=#{version}"
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
