class OpenSkills < Formula
  desc "CLI for the open agent skills ecosystem"
  homepage "https://github.com/EngBlock/open-skills"
  url "https://github.com/EngBlock/open-skills/releases/download/v0.2.0-preview.3/open-skills_0.2.0-preview.3_darwin_arm64.tar.gz"
  version "0.2.0-preview.3"
  sha256 "72d3f29fb63956a25652b9a5549085c09d4c23c5ce3974b6ce328c3c993bf553"
  license "MIT"

  depends_on arch: :arm64
  depends_on :macos

  def install
    bin.install "open-skills"
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/open-skills --version").strip
    assert_match "Usage:", shell_output("#{bin}/open-skills --help")
  end
end
