#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?GH_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${GITHUB_REF_TYPE:?GITHUB_REF_TYPE is required}"
: "${GITHUB_REF_NAME:?GITHUB_REF_NAME is required}"
: "${GITHUB_SHA:?GITHUB_SHA is required}"

if [[ "${GITHUB_REF_TYPE}" != "tag" ]]; then
  echo "native releases must run from a tag, got ${GITHUB_REF_TYPE}" >&2
  exit 1
fi
if [[ ! "${GITHUB_REF_NAME}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
  echo "native release tag ${GITHUB_REF_NAME} is not a canonical version tag" >&2
  exit 1
fi
if ! git merge-base --is-ancestor origin/main "${GITHUB_SHA}"; then
  echo "release tag ${GITHUB_REF_NAME} does not descend from origin/main" >&2
  exit 1
fi
REQUIRE_RELEASE_CREATOR_BYPASS=false scripts/verify-release-rulesets.sh

ref_json="$(gh api "repos/${GITHUB_REPOSITORY}/git/ref/tags/${GITHUB_REF_NAME}")"
ref_type="$(jq -r '.object.type' <<<"${ref_json}")"
tag_object_sha="$(jq -r '.object.sha' <<<"${ref_json}")"
if [[ "${ref_type}" != "tag" ]]; then
  echo "release tag ${GITHUB_REF_NAME} must be an annotated signed tag, got ${ref_type}" >&2
  exit 1
fi

tag_json="$(gh api "repos/${GITHUB_REPOSITORY}/git/tags/${tag_object_sha}")"
actual_tag="$(jq -r '.tag' <<<"${tag_json}")"
target_type="$(jq -r '.object.type' <<<"${tag_json}")"
target_sha="$(jq -r '.object.sha' <<<"${tag_json}")"
verified="$(jq -r '.verification.verified' <<<"${tag_json}")"
verification_reason="$(jq -r '.verification.reason' <<<"${tag_json}")"

if [[ "${actual_tag}" != "${GITHUB_REF_NAME}" ]]; then
  echo "tag object ${tag_object_sha} names ${actual_tag}, not ${GITHUB_REF_NAME}" >&2
  exit 1
fi
if [[ "${target_type}" != "commit" || "${target_sha}" != "${GITHUB_SHA}" ]]; then
  echo "signed tag ${GITHUB_REF_NAME} targets ${target_type} ${target_sha}, not workflow commit ${GITHUB_SHA}" >&2
  exit 1
fi
if [[ "${verified}" != "true" ]]; then
  echo "GitHub did not verify the signature on ${GITHUB_REF_NAME}: ${verification_reason}" >&2
  exit 1
fi
if [[ "$(git cat-file -t "${GITHUB_REF_NAME}")" != "tag" ]]; then
  echo "checked-out ref ${GITHUB_REF_NAME} is not an annotated tag" >&2
  exit 1
fi
if [[ "$(git rev-parse "${GITHUB_REF_NAME}^{tag}")" != "${tag_object_sha}" ]]; then
  echo "checked-out tag object does not match GitHub's canonical tag object ${tag_object_sha}" >&2
  exit 1
fi
if [[ "$(git rev-parse "${GITHUB_REF_NAME}^{commit}")" != "${GITHUB_SHA}" ]]; then
  echo "checked-out tag does not resolve to workflow commit ${GITHUB_SHA}" >&2
  exit 1
fi

printf 'verified signed annotated tag %s (%s -> %s)\n' "${GITHUB_REF_NAME}" "${tag_object_sha}" "${GITHUB_SHA}"
