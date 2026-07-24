#!/usr/bin/env bash
set -euo pipefail

repository="${GITHUB_REPOSITORY:-EngBlock/open-skills}"
pattern='refs/tags/v*'
require_creator_bypass="${REQUIRE_RELEASE_CREATOR_BYPASS:-true}"

ruleset_ids="$(gh api --paginate "repos/${repository}/rulesets" --jq '.[].id')"
immutable=false
restricted_creation=false

while IFS= read -r ruleset_id; do
  [[ -n "${ruleset_id}" ]] || continue
  ruleset="$(gh api "repos/${repository}/rulesets/${ruleset_id}")"
  if ! jq -e --arg pattern "${pattern}" '
    .target == "tag" and
    .enforcement == "active" and
    ((.conditions.ref_name.include // []) | index($pattern) != null) and
    ((.conditions.ref_name.exclude // []) | length == 0)
  ' <<<"${ruleset}" >/dev/null; then
    continue
  fi

  if jq -e '
    ((.rules // []) | map(.type) | index("update") != null) and
    ((.rules // []) | map(.type) | index("deletion") != null) and
    ((.bypass_actors // []) | length == 0)
  ' <<<"${ruleset}" >/dev/null; then
    immutable=true
  fi

  if jq -e '((.rules // []) | map(.type) | index("creation") != null)' <<<"${ruleset}" >/dev/null; then
    if [[ "${require_creator_bypass}" == false ]] || jq -e '
      ((.bypass_actors // []) | length == 1) and
      (.bypass_actors[0].actor_type == "RepositoryRole") and
      (.bypass_actors[0].actor_id == 5) and
      (.bypass_actors[0].bypass_mode == "always")
    ' <<<"${ruleset}" >/dev/null; then
      restricted_creation=true
    fi
  fi
done <<<"${ruleset_ids}"

if [[ "${immutable}" != true ]]; then
  echo "no active no-bypass ruleset prevents updates and deletion for ${pattern}" >&2
  exit 1
fi
if [[ "${restricted_creation}" != true ]]; then
  echo "no active ruleset restricts creation of ${pattern} to repository administrators" >&2
  exit 1
fi

printf 'verified immutable administrator-created release tags matching %s\n' "${pattern}"
