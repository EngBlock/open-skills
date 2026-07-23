class OpenSkills < Formula
  desc "CLI for the open agent skills ecosystem"
  homepage "https://github.com/EngBlock/open-skills"
  url "https://github.com/EngBlock/open-skills/releases/download/v0.2.0-preview.2/open-skills_0.2.0-preview.2_darwin_arm64.tar.gz"
  version "0.2.0-preview.2"
  sha256 "dff942838839bb278780942a8145ff512e9e945bdd2734c57015db6ba3d8842e"
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
