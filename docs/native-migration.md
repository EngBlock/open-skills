# Native preview migration notes

The native preview preserves the npm 0.1.2 command contract except for the approved divergences in the [native compatibility baseline](native-compatibility-baseline.md#intentional-divergences).

## D01: one native executable name

Native distributions contain only `open-skills`. They do not install the npm compatibility aliases `skills` or `add-skill`. The npm package keeps its existing aliases until the later production cutover; installing a native preview does not claim those names.

## D02: offline command shell

Starting `open-skills`, displaying help or version information, initializing a local `SKILL.md`, handling an unknown command, and showing the retired `find`/`search` migration guidance do not perform automatic network access or launch a network tool. The migration handlers return failure after directing users to decentralized GitHub and web discovery.

The native executable has no binary self-updater. `open-skills update` continues to mean updating installed skills; package managers or verified release artifacts own executable upgrades.

Process-level regressions for these rules are labeled `D01` and `D02` in `internal/compatibility`. Offline shell scenarios use recorded proxy and child-command traps, an application dependency boundary, and a network-disabled Linux CI run.

## D03-D06: remote-source acquisition

Native well-known HTTP acquisition follows at most five redirects and reports the
sanitized final host whenever a redirect succeeds. Redirect loops and excessive
chains fail deterministically. Cross-host redirects never forward authorization,
proxy-authorization, cookie, token, API-key, credential, or secret headers.
Plaintext HTTP sources and redirect targets require
`--allow-insecure-transport`; authorization emits a warning and `--yes` never
implies it. Request URLs, redirect diagnostics, persisted provenance, and JSON
strip user-info, query tokens, and fragments, while HTTP failures never print
response bodies or headers.

`open-skills add`, `use`, `check`, and `update` accept GitHub/GitLab shorthands
and URLs plus generic `file`, SSH, and HTTPS Git sources. Plaintext HTTP and
`git` transports are rejected unless the dedicated `--allow-insecure-transport`
flag is present; authorized plaintext acquisition always prints a warning and
`--yes` never implies that authorization. HTTP user credentials are never
accepted and SSH userinfo is excluded from lock metadata.

The native command clones without checkout, resolves one exact commit, and
extracts that commit with `git archive`. It does not run repository hooks,
submodules, filters, or scripts. Option-like/invalid refs and command-capable
Git transports are rejected before subprocess launch, and every subprocess uses
an argument vector without a shell. Git LFS pointer content is rejected before
installation or update mutation. Acquisition uses a temporary workspace that
is removed on both success and failure; it keeps no repository cache. Archive
extraction accepts only regular files, directories, and confined repository links.
Remote processing
defaults to 10 MiB per file, 100 MiB of total content, 5,000 files, and 20
directory levels. `--max-file-bytes`,
`--max-total-bytes`, `--max-files`, and `--max-depth` accept positive decimal
values as deliberate finite overrides. The same limits cover well-known HTTPS
content, remote `use`, and checked updates. System Git first uses the user's
normal credential helpers, askpass, SSH, proxy, and authentication configuration.
After an authentication-class failure for GitHub HTTPS only, the executable
announces and attempts an optional `gh repo clone` fallback; no token or raw
subprocess output is persisted or printed. Selected add content is validated
as one aggregate before any installation or lock mutation; each update source
is likewise preflighted before that source is changed. Local file/byte/count
behavior stays compatible, while full-depth traversal always retains its finite
ceiling.

## D07: provenance and local-content-safe replacement

An installed skill may be reinstalled or updated from the same credential-free,
normalized source identity without additional authorization. Add and sync
preflight every selected skill against the existing scope lock before changing
any placement. A same-named skill owned by another source is rejected in
automation unless `--replace` is passed explicitly; `--yes` does not imply that
authorization. Interactive replacement displays the sanitized installed and
incoming source types and identities before asking for confirmation.

Native installs also record a portable owned-path manifest for each canonical,
copied, linked, and Eve placement. Add, replacement, update, sync, and removal
compare every affected placement before mutation, including independent copies
across multiple agent targets. Changed, added, deleted, executable-mode-changed,
and retargeted linked paths are reported without following unexpected links.

A terminal user sees the affected paths and must confirm discarding local work.
Automation must pass `--force`; `--yes` never grants this authorization. Source
replacement remains a separate decision, so an unattended operation with both
provenance and content conflicts requires both `--replace` and `--force`.
Legacy installations without enough native ownership metadata fail closed when
their current content cannot be verified.

Rejected replacements leave content, placements, and lock state unchanged.
Authorized replacements snapshot affected placements and restore them when a
placement or lock write fails.

## D08: crash-recoverable installation transactions

Add, sync, and update finish source, content, destination, provenance, and
local-change preflight before committing an installation placement. They stage
every selected placement and the final lock beside its destination, then commit
the declared write set in a deterministic order with the lock last. Fresh
installs, same-source reinstalls, authorized cross-source replacements, and
updates use the same transaction path.

Before staging, the executable writes a durable journal under
`$XDG_STATE_HOME/open-skills/transactions` (or
`~/.local/state/open-skills/transactions`). The journal includes durable backups,
the current commit step, and the exact destination/stage paths. An ordinary
failure rolls back completed steps. After interruption, the next invocation in
the affected project automatically restores the prior placements and lock before
running its command; a completed transaction only needs journal cleanup.

If recovery cannot restore a recorded backup, the journal and remaining backups
are preserved and the command stops with deterministic manual cleanup
instructions. Each destination is staged beside itself so its final rename stays
on that destination's filesystem. A transaction spanning several filesystems is
therefore crash-recoverable through its journal and ordered rollback, but is
**not atomic across filesystems**; the journal records that commit model
explicitly. On Windows, Go cannot flush directory handles, so process-interruption
recovery is journaled but power-loss persistence retains the guarantees of the
underlying Windows filesystem rather than a portable directory-`fsync` claim.

## D09: safe existing-state inspection

`open-skills list` reads project state from `./skills-lock.json`. Global listing reads `$XDG_STATE_HOME/skills/.skill-lock.json` when `XDG_STATE_HOME` is set and otherwise uses `~/.agents/.skill-lock.json`. Existing project schema version 1 and global schema version 3 state from upstream skills v1.5.20 and npm open-skills 0.1.2 is used in place; no first-run migration occurs.

Malformed state, unsupported older schemas, and unknown newer schemas stop inspection with recovery guidance. The native executable does not reinterpret them as empty state, rewrite them, or attempt a downgrade. Supported documents retain unknown JSON fields when passed through the native state encoder so later validated mutations can preserve extensions.

## D10: exact remote instruction trust

Native `open-skills use` supports local, Git, and well-known HTTPS sources. Remote prompts identify the credential-free source and immutable Git commit before the selected `SKILL.md`; well-known content uses an exact `sha256:` content revision in the same commit-scoped trust field.

`use --agent` never injects a previously untrusted remote revision implicitly. A terminal user must confirm the first exact source/revision pair, while automation must pass the dedicated `--trust` authorization. `--yes` does not grant instruction trust. An installed skill whose lock entry records the same source and exact revision is already approved; a changed revision requires new consent.

Approvals are stored under the platform user configuration directory at `open-skills/trust.json` (honoring `XDG_CONFIG_HOME`). Each record contains only sanitized source identity, exact commit/content revision, and approval time. `open-skills trust list [--json]`, exact `trust revoke <source> --commit <revision>`, broad source revocation, and `trust clear` are offline. Broad revocation and clearing prompt in a terminal or require `--yes` in automation.

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
