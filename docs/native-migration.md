# Migrate from the retired npm package to native open-skills 0.2.0

The JavaScript package `@engblock/open-skills` is retired and frozen at 0.1.2. Native `open-skills` 0.2.0 is the production release and preserves the npm command contract except for the approved, security- and reliability-focused divergences documented below. The old package does not download the native executable and will receive no new features.

System Git is the native executable's only runtime dependency. Node.js and npm are not required after the old global package is removed. Active development, testing, packaging, and release automation are Go-only; the protected `v0.1.2` tag and reviewed compatibility corpus retain the old source and behavior for historical inspection.

## Install the native executable

### Homebrew (supported macOS ARM64)

```sh
brew tap EngBlock/open-skills https://github.com/EngBlock/open-skills
brew install EngBlock/open-skills/open-skills
```

Homebrew owns executable upgrades. `open-skills update` continues to update installed skills, not the executable.

### Scoop (experimental Windows x86-64)

```powershell
scoop bucket add open-skills https://github.com/EngBlock/open-skills
scoop install open-skills/open-skills
```

Windows x86-64 remains experimental. Scoop owns executable upgrades; use `scoop update open-skills` rather than expecting `open-skills update` to replace the executable.

### Verified direct archive

GitHub Releases are the canonical artifact source. Choose the archive for your target from the [v0.2.0 release](https://github.com/EngBlock/open-skills/releases/tag/v0.2.0):

| Target | Archive | Status |
| --- | --- | --- |
| macOS ARM64 | `open-skills_0.2.0_darwin_arm64.tar.gz` | Supported |
| Linux x86-64 | `open-skills_0.2.0_linux_amd64.tar.gz` | Supported |
| macOS x86-64 | `open-skills_0.2.0_darwin_amd64.tar.gz` | Experimental |
| Linux ARM64 | `open-skills_0.2.0_linux_arm64.tar.gz` | Experimental |
| Windows x86-64 | `open-skills_0.2.0_windows_amd64.zip` | Experimental |

For example, set `ARCHIVE` to the selected filename and download only immutable release assets:

```sh
ARCHIVE=open-skills_0.2.0_linux_amd64.tar.gz
gh release download v0.2.0 --repo EngBlock/open-skills \
  --pattern "$ARCHIVE" \
  --pattern "$ARCHIVE.sigstore.json" \
  --pattern checksums.txt \
  --pattern provenance.sigstore.json
```

Before extracting or executing the archive, verify its checksum, keyless signature, repository, tag, and producing workflow identity:

```sh
grep "  $ARCHIVE$" checksums.txt | sha256sum --check -
cosign verify-blob \
  --bundle "$ARCHIVE.sigstore.json" \
  --certificate-identity 'https://github.com/EngBlock/open-skills/.github/workflows/native-preview.yml@refs/tags/v0.2.0' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  "$ARCHIVE"
gh attestation verify "$ARCHIVE" \
  --bundle provenance.sigstore.json \
  --repo EngBlock/open-skills \
  --signer-workflow EngBlock/open-skills/.github/workflows/native-preview.yml
```

On macOS, replace `sha256sum --check -` with `shasum -a 256 --check -`. After every verification succeeds, extract the archive and place `open-skills` in a directory on `PATH`; for example:

```sh
tar -xzf "$ARCHIVE"
mkdir -p "$HOME/.local/bin"
install -m 0755 open-skills "$HOME/.local/bin/open-skills"
```

For the Windows ZIP, verify it before using `Expand-Archive`, then place `open-skills.exe` on `PATH`. Never install an unverified archive.

## Remove the retired npm package

After confirming the native executable is the one on `PATH`, remove the frozen JavaScript package and its `skills` and `add-skill` aliases:

```sh
open-skills --version
npm uninstall --global @engblock/open-skills
```

Native distributions intentionally expose only `open-skills`. They never claim the `skills` or `add-skill` executable names.

## Existing state is recognized in place

No state import or skill reinstallation is required. Native `open-skills` reads existing npm and upstream state directly:

- project state: `./skills-lock.json` schema version 1;
- global state with `XDG_STATE_HOME`: `$XDG_STATE_HOME/skills/.skill-lock.json` schema version 3;
- legacy global state otherwise: `~/.agents/.skill-lock.json` schema version 3.

The native CLI also recognizes the existing canonical and agent skill placements recorded by those locks. Run `open-skills list` before the first mutation to inspect the recognized project and global state.

Native writes can add optional provenance, content-ownership, and transaction metadata. This migration is forward-only: the native CLI preserves required legacy fields and safe unknown fields, but the retired npm CLI is not guaranteed to preserve or understand native metadata. After native `open-skills` mutates state, do not use npm 0.1.2 to mutate that same state. Checked-in project locks remain readable and should be reviewed like any other repository change.

## Migrate environment variables

Canonical native variables use the `OPEN_SKILLS_` prefix. The native 1.x line continues to accept the two baseline legacy names, emits a migration warning, and gives a present canonical value precedence even when that value is empty or invalid:

| npm/legacy name | Native canonical name |
| --- | --- |
| `SKILLS_CLONE_TIMEOUT_MS` | `OPEN_SKILLS_CLONE_TIMEOUT_MS` |
| `INSTALL_INTERNAL_SKILLS` | `OPEN_SKILLS_INSTALL_INTERNAL_SKILLS` |

Legacy names remain supported through 1.x and are removed no earlier than 2.0. `OPEN_SKILLS_SUPPRESS_LEGACY_ENV_WARNINGS=1` suppresses only legacy-name warnings. Standard or third-party variables such as `NO_COLOR`, `XDG_*`, Git credentials, and agent-owned home variables keep their established names. The complete contract is in [D12](#d12-namespaced-configuration-and-exact-authorization).

## Intentional behavior changes

The most visible native differences are:

- only the `open-skills` executable is installed;
- startup, help, local operations, and retired search guidance remain offline, and there is no binary self-updater;
- remote sources gain immutable commit provenance, transport restrictions, credential redaction, resource limits, and confined link handling;
- replacing another source requires `--replace`, discarding local changes requires `--force`, remote instruction trust requires `--trust`, and insecure transport requires `--allow-insecure-transport`; `--yes` grants none of these;
- malformed or unsupported state fails safely instead of being reset, while mutations use advisory locks and recoverable transactions;
- native-owned configuration uses `OPEN_SKILLS_*`, and supported commands offer deterministic versioned JSON.

The D01-D13 sections below are the complete migration record. They describe every approved divergence and link to externally observable regression coverage through the [native compatibility baseline](native-compatibility-baseline.md#intentional-divergences).

## D01: one native executable name

Native distributions contain only `open-skills`. They do not install the npm compatibility aliases `skills` or `add-skill`. The frozen npm artifact retains its historical aliases, so uninstall it to remove them; installing the native release does not claim those names.

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

## D08: crash-recoverable mutation transactions

Add, remove, update, restore, and sync finish source, content, destination,
provenance, and local-change preflight before committing user-visible state.
They stage every selected placement and the final lock beside its destination,
then commit the complete declared write set in a deterministic order with the
lock last. Fresh installs, same-source reinstalls, authorized cross-source
replacements, removals, missing-upstream deletions, lock restoration, and
node_modules sync use the same transaction path. Deletions are explicit
journaled targets rather than direct filesystem writes, so their prior content
is backed up and recoverable like a replacement.

Before staging, the executable writes a durable journal under
`$XDG_STATE_HOME/open-skills/transactions` (or
`~/.local/state/open-skills/transactions`). The journal includes durable backups,
the current commit step, and the exact destination/stage paths. An ordinary
failure rolls back completed steps. After interruption, the next invocation in
the affected project automatically restores the prior placements and lock before
running its command; a completed transaction only needs journal cleanup. A
successful rollback is durably marked before backups are retired, and the
journal directory is atomically moved out of the recoverable namespace before
its artifacts are deleted, making repeated cleanup safe after interruption.

Restore resolves every recorded local source before changing any destination,
and multi-skill remove, restore, and sync commands commit only after the whole
selected write set has staged successfully. Update combines an approved
missing-upstream removal with replacements from that source in one transaction.
Success output is emitted only after commit, so a failed workflow does not
silently report a partially applied result.

If recovery cannot restore a recorded backup, the journal and remaining backups
are preserved and the command stops with deterministic manual cleanup
instructions. Each replacement destination is staged beside itself so its final
rename stays on that destination's filesystem. A transaction spanning several filesystems is
therefore crash-recoverable through its journal and ordered rollback, but is
**not atomic across filesystems**; the journal records that commit model
explicitly. On Windows, Go cannot flush directory handles, so process-interruption
recovery is journaled but power-loss persistence retains the guarantees of the
underlying Windows filesystem rather than a portable directory-`fsync` claim.

Mutating commands hold exclusive OS advisory locks for the affected state and
installation resources from state-dependent preflight through commit. Project
and global operations that reach the same canonical installation directory use
the same installation lock even when their state locations differ. Unix builds
use `flock`; Windows builds use `LockFileEx`. The OS releases these leases on
normal completion, errors, cancellation, and process death. Recovery treats a
journal directory as a lock-free hint only: it acquires the exclusive state and
installation leases, re-enumerates journals, and then repairs state, so it never
recovers a live writer that completed while recovery was waiting. Shared readers
also recheck for a journal after acquiring the state lease; if a writer died
between the startup hint and that acquisition, the reader releases its shared
lease, recovers under exclusive leases, and retries inspection.

`list`, `check`, installed-commit trust inspection, and `trust list` use shared
leases, allowing read-only inspections to run together while excluding a
commit. Installation identities are locked even before their directories exist;
the lock registry is outside managed installation paths, so read-only inspection
does not create those directories. A contended lease prints one status line to
stderr. Waiting defaults to 10 seconds and is bounded by
`OPEN_SKILLS_LOCK_TIMEOUT_MS`, a non-negative decimal millisecond value.
Invalid values fail before managed state is touched; timeouts identify the
contended lease and recommend waiting for the other
command or increasing the setting. Correctness takes priority over lock hold
time, so this preview may retain a lease through state-dependent prompts and
remote update acquisition.

## D09: safe existing-state inspection

`open-skills list` reads project state from `./skills-lock.json`. Global listing reads `$XDG_STATE_HOME/skills/.skill-lock.json` when `XDG_STATE_HOME` is set and otherwise uses `~/.agents/.skill-lock.json`. Existing project schema version 1 and global schema version 3 state from upstream skills v1.5.20 and npm open-skills 0.1.2 is used in place; no first-run migration occurs.

Malformed state, unsupported older schemas, and unknown newer schemas stop inspection with recovery guidance. The native executable does not reinterpret them as empty state, rewrite them, or attempt a downgrade. Supported documents retain unknown JSON fields when passed through the native state encoder so later validated mutations can preserve extensions.

## D10: exact remote instruction trust

Native `open-skills use` supports local, Git, and well-known HTTPS sources. Remote prompts identify the credential-free source and immutable Git commit before the selected `SKILL.md`; well-known content uses an exact `sha256:` content revision in the same commit-scoped trust field.

`use --agent` never injects a previously untrusted remote revision implicitly. A terminal user must confirm the first exact source/revision pair, while automation must pass the dedicated `--trust` authorization. `--yes` does not grant instruction trust. An installed skill whose lock entry records the same source and exact revision is already approved; a changed revision requires new consent.

Approvals are stored under the platform user configuration directory at `open-skills/trust.json` (honoring `XDG_CONFIG_HOME`). Each record contains only sanitized source identity, exact commit/content revision, and approval time. `open-skills trust list [--json]`, exact `trust revoke <source> --commit <revision>`, broad source revocation, and `trust clear` are offline. Broad revocation and clearing prompt in a terminal or require `--yes` in automation.

## D11: versioned JSON automation

`list`, `check`, `add`, `remove`, `update`, and `trust list` expose documented schema-versioned JSON. `--json` can be placed before the command or among its arguments. Each invocation writes exactly one machine-readable document to stdout; diagnostics and subprocess notices remain on stderr. Results use deterministic scope/name ordering and failures expose stable symbolic error codes.

JSON mode cannot prompt and does not grant ordinary or risk-specific authorization. Callers must supply explicit selection and authorization flags such as `--skill`, `--agent`, `--yes`, `--force`, `--replace`, or `--allow-insecure-transport` when the operation requires them. Existing numeric success/failure behavior is unchanged, and JSON remains unsupported for `use` so launched-agent stream and exit passthrough are preserved.

The complete version 1 schemas, ordering rules, statuses, and symbolic code registry are defined in the [native JSON automation contract](json-contract.md).

## D12: namespaced configuration and exact authorization

Open Skills-owned environment settings use the `OPEN_SKILLS_` namespace:

| Canonical variable | Purpose | Default | Legacy name supported through 1.x |
| --- | --- | --- | --- |
| `OPEN_SKILLS_CLONE_TIMEOUT_MS` | Positive Git subprocess timeout in decimal milliseconds | `300000` (5 minutes) | `SKILLS_CLONE_TIMEOUT_MS` |
| `OPEN_SKILLS_INSTALL_INTERNAL_SKILLS` | Set to exactly `1` or `true` to include skills whose frontmatter has `metadata.internal: true` | Internal skills are hidden | `INSTALL_INTERNAL_SKILLS` |
| `OPEN_SKILLS_LOCK_TIMEOUT_MS` | Non-negative advisory-lock wait in decimal milliseconds | `10000` (10 seconds) | None; this setting was introduced namespaced |
| `OPEN_SKILLS_SUPPRESS_LEGACY_ENV_WARNINGS` | Set to exactly `1` or `true` to suppress only the migration warnings for legacy environment names | Warnings enabled | None |

A present canonical variable always wins, including an empty, false, or invalid
value; Open Skills never falls back to a conflicting legacy value. Using either
legacy name writes a migration diagnostic to stderr and remains supported
through every 1.x release. When both names are present, the diagnostic also says
that the legacy value was ignored. Legacy names are removed no earlier than 2.0.
The suppression control affects only these migration diagnostics. It never hides
configuration errors, acquisition notices, insecure-transport warnings, or
other operational diagnostics.

Ecosystem and third-party variables keep their established names. In particular,
`NO_COLOR`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `HOME`, `GH_HOST`,
`GITHUB_TOKEN`, `GH_TOKEN`, `GIT_SSH_COMMAND`, platform application-data
variables, agent-owned home overrides, and agent-runtime detection variables do
not gain branded aliases.

Non-interactive authorization follows the same exact-name rule as configuration.
`--yes` skips only ordinary selection, scope, removal, or trust-store management
confirmations. It never implies `--replace`, `--force`, `--trust`, or
`--allow-insecure-transport`; when more than one risk applies, every corresponding
flag is required independently.

## D13: canonical native release supply chain

Native releases come from immutable signed Git tags through the reviewed GitHub Actions workflow. Every supported and experimental archive is CGO-disabled, contains only the canonical `open-skills` executable, and is covered by SHA-256 checksums, keyless Sigstore signatures, and GitHub build provenance tied to the repository, tag, and workflow identity. Homebrew and Scoop metadata reference the same immutable GitHub Release artifacts; Windows remains explicitly experimental.

The retired npm package is never used to download or bootstrap a native executable. Package managers or manually verified direct archives own executable installation and upgrades, while `open-skills update` continues to update installed skills. Documentation does not recommend `curl | sh` or any other mutable network-to-shell installer.
