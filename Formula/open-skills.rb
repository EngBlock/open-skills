class OpenSkills < Formula
  desc "CLI for the open agent skills ecosystem"
  homepage "https://github.com/EngBlock/open-skills"
  url "https://github.com/EngBlock/open-skills/releases/download/v0.2.0/open-skills_0.2.0_darwin_arm64.tar.gz"
  version "0.2.0"
  sha256 "0be8e9e388ea468b32762dc361d32a70ce51784ed7447e3f7b60771238dfd82e"
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
