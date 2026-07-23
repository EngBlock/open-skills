#!/usr/bin/env bash
set -euo pipefail

if [[ $(uname -s) != Darwin || $(uname -m) != arm64 ]]; then
  echo "Homebrew smoke tests require macOS ARM64" >&2
  exit 1
fi
if [[ $# -ne 1 ]]; then
  echo "usage: $0 <open-skills.rb>" >&2
  exit 1
fi

formula=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=$(ruby -ne 'puts $1 if $_ =~ /^  version "([^"]+)"$/' "$formula")
checksum=$(ruby -ne 'puts $1 if $_ =~ /^  sha256 "([0-9a-f]{64})"$/' "$formula")
archive="open-skills_${version}_darwin_arm64.tar.gz"
canonical_url="https://github.com/EngBlock/open-skills/releases/download/v${version}/${archive}"

if [[ -z $version || -z $checksum ]] || ! grep -Fq "  url \"${canonical_url}\"" "$formula"; then
  echo "formula must contain one version, SHA-256, and canonical macOS ARM64 GitHub Release URL" >&2
  exit 1
fi

work=$(mktemp -d)
tap="engblock/open-skills-smoke-$$-$RANDOM"
formula_name="${tap}/open-skills"
server_pid=
cleanup() {
  HOMEBREW_NO_AUTO_UPDATE=1 brew uninstall --force "$formula_name" >/dev/null 2>&1 || true
  HOMEBREW_NO_AUTO_UPDATE=1 brew untap "$tap" >/dev/null 2>&1 || true
  if [[ -n $server_pid ]]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$work/artifacts"
old_version=0.1.999
old_binary="$work/artifacts/open-skills"
(
  cd "$root"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
    -ldflags "-s -w -X github.com/EngBlock/open-skills/internal/application.Version=${old_version}" \
    -o "$old_binary" ./cmd/open-skills
)
tar -C "$work/artifacts" -czf "$work/artifacts/open-skills_${old_version}_darwin_arm64.tar.gz" open-skills
rm "$old_binary"
old_checksum=$(shasum -a 256 "$work/artifacts/open-skills_${old_version}_darwin_arm64.tar.gz" | awk '{print $1}')

port_file="$work/server-port"
python3 - "$work/artifacts" "$port_file" <<'PY' &
import functools
import http.server
import pathlib
import socketserver
import sys

root, port_file = sys.argv[1:]

class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, format, *args):
        pass


class QuietServer(socketserver.TCPServer):
    def handle_error(self, request, client_address):
        pass


handler = functools.partial(QuietHandler, directory=root)
with QuietServer(("127.0.0.1", 0), handler) as server:
    pathlib.Path(port_file).write_text(str(server.server_address[1]))
    server.serve_forever()
PY
server_pid=$!
for _ in {1..100}; do
  [[ -s $port_file ]] && break
  sleep 0.05
done
[[ -s $port_file ]] || { echo "local artifact server did not start" >&2; exit 1; }
port=$(<"$port_file")

current_formula="$work/open-skills.rb"
cp "$formula" "$current_formula"
if [[ -n ${OPEN_SKILLS_HOMEBREW_ARTIFACT:-} ]]; then
  actual_checksum=$(shasum -a 256 "$OPEN_SKILLS_HOMEBREW_ARTIFACT" | awk '{print $1}')
  if [[ $actual_checksum != "$checksum" ]]; then
    echo "local Homebrew artifact checksum ${actual_checksum} does not match formula ${checksum}" >&2
    exit 1
  fi
  cp "$OPEN_SKILLS_HOMEBREW_ARTIFACT" "$work/artifacts/$archive"
  OPEN_SKILLS_HOMEBREW_ARTIFACT_URL="http://127.0.0.1:${port}/${archive}"
fi
if [[ -n ${OPEN_SKILLS_HOMEBREW_ARTIFACT_URL:-} ]]; then
  replacement=${OPEN_SKILLS_HOMEBREW_ARTIFACT_URL}
  FORMULA="$current_formula" CANONICAL_URL="$canonical_url" REPLACEMENT="$replacement" ruby <<'RUBY'
path = ENV.fetch("FORMULA")
content = File.read(path)
canonical = ENV.fetch("CANONICAL_URL")
abort "canonical formula URL missing" unless content.include?(canonical)
File.write(path, content.sub(canonical, ENV.fetch("REPLACEMENT")))
RUBY
fi

HOMEBREW_NO_AUTO_UPDATE=1 brew untap "$tap" >/dev/null 2>&1 || true
HOMEBREW_NO_AUTO_UPDATE=1 brew tap-new "$tap" >/dev/null
tap_dir=$(brew --repository "$tap")
cat >"$tap_dir/Formula/open-skills.rb" <<EOF
class OpenSkills < Formula
  desc "CLI for the open agent skills ecosystem"
  homepage "https://github.com/EngBlock/open-skills"
  url "http://127.0.0.1:${port}/open-skills_${old_version}_darwin_arm64.tar.gz"
  version "${old_version}"
  sha256 "${old_checksum}"
  license "MIT"

  depends_on arch: :arm64
  depends_on :macos

  def install
    bin.install "open-skills"
  end
end
EOF
git -C "$tap_dir" add Formula/open-skills.rb
git -C "$tap_dir" -c user.name=Homebrew -c user.email=homebrew@example.invalid commit -m "Old smoke fixture" >/dev/null

HOMEBREW_NO_AUTO_UPDATE=1 brew install "$formula_name"
[[ $("$(brew --prefix "$formula_name")/bin/open-skills" --version) == "$old_version" ]]

cp "$current_formula" "$tap_dir/Formula/open-skills.rb"
git -C "$tap_dir" add Formula/open-skills.rb
git -C "$tap_dir" -c user.name=Homebrew -c user.email=homebrew@example.invalid commit -m "Current formula" >/dev/null
HOMEBREW_NO_AUTO_UPDATE=1 brew upgrade "$formula_name"

prefix=$(brew --prefix "$formula_name")
[[ $("$prefix/bin/open-skills" --version) == "$version" ]]
"$prefix/bin/open-skills" --help | grep -Fq 'Usage:'
[[ $(find "$prefix/bin" -mindepth 1 -maxdepth 1 \( -type f -o -type l \) | wc -l | tr -d ' ') == 1 ]]
[[ -x "$prefix/bin/open-skills" ]]
HOMEBREW_NO_AUTO_UPDATE=1 brew test "$formula_name"

HOMEBREW_NO_AUTO_UPDATE=1 brew uninstall "$formula_name" >/dev/null
HOMEBREW_NO_AUTO_UPDATE=1 brew install "$formula_name"
prefix=$(brew --prefix "$formula_name")
[[ $("$prefix/bin/open-skills" --version) == "$version" ]]
"$prefix/bin/open-skills" --help | grep -Fq 'Usage:'
[[ $(find "$prefix/bin" -mindepth 1 -maxdepth 1 \( -type f -o -type l \) | wc -l | tr -d ' ') == 1 ]]
[[ -x "$prefix/bin/open-skills" ]]
