class OpenSkills < Formula
  desc "CLI for the open agent skills ecosystem"
  homepage "https://github.com/EngBlock/open-skills"
  url "https://github.com/EngBlock/open-skills/releases/download/v0.2.0-preview.1/open-skills_0.2.0-preview.1_darwin_arm64.tar.gz"
  version "0.2.0-preview.1"
  sha256 "192db297fdebfb65c8f27791f7116acbacf0ab5d95641f7abb2087e82578ec6a"
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
