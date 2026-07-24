#!/usr/bin/env bash
set -euo pipefail

: "${TAG:?TAG must be set to a version without the v prefix, for example TAG=0.2.1}"

version="${TAG}"
tag="v${version}"
branch="release/v${version}"
repository="${GITHUB_REPOSITORY:-EngBlock/open-skills}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"

if [[ ! "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "TAG must be a canonical version without the v prefix, got ${version}" >&2
  exit 1
fi
if [[ "$(git branch --show-current)" != "${branch}" ]]; then
  echo "release must run from branch ${branch}" >&2
  exit 1
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "release worktree must be clean before generating package metadata" >&2
  git status --short >&2
  exit 1
fi

for command in git go gh jq; do
  if ! command -v "${command}" >/dev/null 2>&1; then
    echo "release requires ${command} on PATH" >&2
    exit 1
  fi
done

GITHUB_REPOSITORY="${repository}" scripts/verify-release-rulesets.sh

required_go="go$(awk '$1 == "go" { print $2; exit }' go.mod)"
export GOTOOLCHAIN="${required_go}"
if [[ "$(go env GOVERSION)" != "${required_go}" ]]; then
  echo "release requires ${required_go}, got $(go env GOVERSION)" >&2
  exit 1
fi

git fetch origin main --tags
if ! git merge-base --is-ancestor origin/main HEAD; then
  echo "release branch must contain origin/main" >&2
  exit 1
fi
if git rev-parse --verify --quiet "refs/tags/${tag}" >/dev/null ||
   git ls-remote --exit-code --tags origin "refs/tags/${tag}" >/dev/null 2>&1; then
  echo "release tag ${tag} already exists" >&2
  exit 1
fi
if gh release view "${tag}" --repo "${repository}" >/dev/null 2>&1; then
  echo "GitHub release ${tag} already exists" >&2
  exit 1
fi

rm -rf .native-dist
go run ./internal/release/cmd/native-preview \
  --version "${version}" \
  --output .native-dist \
  --homebrew-formula Formula/open-skills.rb \
  --scoop-manifest bucket/open-skills.json \
  --skip-linux-smoke

test -z "$(gofmt -l cmd internal)"
go vet ./...
go test ./... -count=1

git add Formula/open-skills.rb bucket/open-skills.json
if git diff --cached --quiet; then
  metadata_commit="$(git log -1 --format=%s -- Formula/open-skills.rb bucket/open-skills.json)"
  if [[ "${metadata_commit}" != "Prepare ${tag} native release" ]]; then
    echo "generated package metadata did not change and no matching release metadata commit exists" >&2
    exit 1
  fi
else
  git commit -m "Prepare ${tag} native release"
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "release worktree is dirty after committing generated metadata" >&2
  git status --short >&2
  exit 1
fi
git fetch origin main
if ! git merge-base --is-ancestor origin/main HEAD; then
  echo "origin/main advanced during release verification; rebase the candidate and retry" >&2
  exit 1
fi
git tag -s "${tag}" -m "open-skills ${tag} native release"
if [[ "$(git config --get gpg.format || true)" == "ssh" ]]; then
  signing_key="$(git config --get user.signingkey)"
  if [[ -f "${signing_key}" ]]; then
    signing_key="$(cat "${signing_key}")"
  fi
  allowed_signers="$(mktemp)"
  trap 'rm -f "${allowed_signers}"' EXIT
  printf '%s %s\n' "$(git config user.email)" "${signing_key}" >"${allowed_signers}"
  git -c gpg.ssh.allowedSignersFile="${allowed_signers}" verify-tag "${tag}"
else
  git verify-tag "${tag}"
fi
git push --atomic origin "HEAD:refs/heads/${branch}" "refs/tags/${tag}"

printf 'pushed signed release candidate %s from %s\n' "${tag}" "${branch}"
printf 'approve the native-production environment after build, Homebrew, and Scoop checks pass\n'
