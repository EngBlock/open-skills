# Native preview migration notes

The native preview preserves the npm 0.1.2 command contract except for the approved divergences in the [native compatibility baseline](native-compatibility-baseline.md#intentional-divergences).

## D01: one native executable name

Native distributions contain only `open-skills`. They do not install the npm compatibility aliases `skills` or `add-skill`. The npm package keeps its existing aliases until the later production cutover; installing a native preview does not claim those names.

## D02: offline command shell

Starting `open-skills`, displaying help or version information, initializing a local `SKILL.md`, handling an unknown command, and showing the retired `find`/`search` migration guidance do not perform automatic network access or launch a network tool. The migration handlers return failure after directing users to decentralized GitHub and web discovery.

The native executable has no binary self-updater. `open-skills update` continues to mean updating installed skills; package managers or verified release artifacts own executable upgrades.

Process-level regressions for these rules are labeled `D01` and `D02` in `internal/compatibility`. Offline shell scenarios use recorded proxy and child-command traps, an application dependency boundary, and a network-disabled Linux CI run.

## D03-D06: Git-source acquisition

`open-skills add` accepts GitHub/GitLab shorthands and URLs plus generic `file`,
SSH, and HTTPS Git sources. Plaintext HTTP and `git` transports are rejected;
HTTP user credentials are never accepted and SSH userinfo is excluded from lock
metadata.

The native command clones without checkout, resolves one exact commit, and
extracts that commit with `git archive`. It does not run repository hooks,
submodules, filters, or scripts. Git LFS pointer content is rejected before
installation. Acquisition uses a temporary workspace that is removed on both
success and failure; it keeps no repository cache. Archive extraction accepts
only regular files and directories and is bounded to 32 MiB, 10,000 entries,
and 20 path components.

## D09: safe existing-state inspection

`open-skills list` reads project state from `./skills-lock.json`. Global listing reads `$XDG_STATE_HOME/skills/.skill-lock.json` when `XDG_STATE_HOME` is set and otherwise uses `~/.agents/.skill-lock.json`. Existing project schema version 1 and global schema version 3 state from upstream skills v1.5.20 and npm open-skills 0.1.2 is used in place; no first-run migration occurs.

Malformed state, unsupported older schemas, and unknown newer schemas stop inspection with recovery guidance. The native executable does not reinterpret them as empty state, rewrite them, or attempt a downgrade. Supported documents retain unknown JSON fields when passed through the native state encoder so later validated mutations can preserve extensions.

## D11: versioned list JSON

`open-skills list --json` writes only one JSON document to stdout and sends human diagnostics to stderr. Success uses schema version 1:

```json
{
  "schemaVersion": 1,
  "scope": "project",
  "skills": []
}
```

Skills are ordered by name and agent names use deterministic baseline registry order. Failures retain the baseline numeric exit status and return a versioned `error` object with a symbolic code such as `state_malformed`, `state_version_older`, `state_version_newer`, or `invalid_agent`. JSON mode does not emit colors or prompts.
