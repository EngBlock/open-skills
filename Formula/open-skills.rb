class OpenSkills < Formula
  desc "CLI for the open agent skills ecosystem"
  homepage "https://github.com/EngBlock/open-skills"
  url "https://github.com/EngBlock/open-skills/releases/download/v0.2.1/open-skills_0.2.1_darwin_arm64.tar.gz"
  version "0.2.1"
  sha256 "ed94848be50e2f77e1c21fba2a4df3f007c35d4ccaa643418af4b56cc4ee38db"
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
