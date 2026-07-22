# Native compatibility baseline: npm 0.1.2

Status: **reviewed; approval takes effect when issue #11 closes**. [Issue #10](https://github.com/EngBlock/open-skills/issues/10) approved the compatibility boundary and intentional divergences using a provisional 0.1.0 version. [Issue #11](https://github.com/EngBlock/open-skills/issues/11) is the explicit decision that updates the completed baseline to 0.1.2; closing it approves this inventory before Go behavior begins. Native behavior must not be implemented against another release unless a new explicit decision replaces this document and the machine-readable oracle together.

## Selected oracle

The byte-level oracle is exactly npm package `@engblock/open-skills@0.1.2`, not the mutable `latest` dist-tag:

| Property | Pinned value |
| --- | --- |
| Published | `2026-07-22T14:13:45.715Z` |
| Artifact | `https://registry.npmjs.org/@engblock/open-skills/-/open-skills-0.1.2.tgz` |
| npm SRI | `sha512-TTD/WemKLYiem5bM+vEtuxXORSZExvP4wdDxCELP1xpSqlSnEzMa2xe61JO7xBMFEbSrtBqEyys+cHVEDyCMFg==` |
| SHA-1 | `159fb3c760ea72b731674c6a93059a37040a0c1f` |
| SHA-256 | `2871993290bb28ae40d3a1c59f64b1e29564de4145a62bd3c9bdf7b85aef39c9` |
| SHA-512 | `4d30ff59e98a2d889e9b96ccfaf12dbb15ce452644c6f3f8c1d0f10842cfd71a52aa54a713331adb17bad493bbc4130511b4abb41a84cb2b3e7075440f208c16` |
| Archive size | `108613` bytes |
| Packed content | 17 files, `463008` unpacked bytes |
| Tag protection | Active GitHub ruleset [`19578936`](https://github.com/EngBlock/open-skills/rules/19578936) rejects updates and deletion of `refs/tags/v0.1.2`, with no bypass actors configured |

The semantic source oracle is the annotated repository tag chain:

```text
refs/tags/v0.1.2
  -> tag b3117c12d841b5fdfc3c2fead72c39d01e148ab2
  -> commit a91eb79d035d7a33300d2cc60b18db3f81a94621
  -> tree f766eaf80048c8f5232eaa981bfd1fa45485fc70
```

The tag object and npm artifact are content-addressed in [`compatibility/npm-0.1.2/oracle.json`](../compatibility/npm-0.1.2/oracle.json). GitHub ruleset `19578936` prevents updates and deletion of `refs/tags/v0.1.2`; the verifier additionally fails if that protection is disabled or its update/deletion rules disappear. Future npm releases, changes to a dist-tag, or GitHub configuration drift therefore cannot silently select a different oracle because verification compares the bytes, protection, annotated tag object, commit, and tree to fixed identities.

Run the reproducible online check from the repository root:

```sh
pnpm verify:native-baseline
```

The verifier reads the checked-in manifest, fetches the version-specific npm and GitHub API resources, recomputes all artifact digests, and checks the tag protection plus complete tag-to-tree chain. GitHub authentication via `GITHUB_TOKEN`, `GH_TOKEN`, or `gh auth login` is required so the API exposes bypass configuration. It does not resolve `latest`.

Primary records: [npm version metadata](https://registry.npmjs.org/%40engblock%2Fopen-skills/0.1.2), [npm publication history](https://registry.npmjs.org/%40engblock%2Fopen-skills), [tag ref](https://api.github.com/repos/EngBlock/open-skills/git/refs/tags/v0.1.2), [annotated tag object](https://api.github.com/repos/EngBlock/open-skills/git/tags/b3117c12d841b5fdfc3c2fead72c39d01e148ab2), and [source commit](https://github.com/EngBlock/open-skills/commit/a91eb79d035d7a33300d2cc60b18db3f81a94621).

### Source-correlation limits

The npm record has no `gitHead`. The annotated tag is unsigned, and there is no GitHub Release for it. The publish workflow built `dist/` before creating the version commit and tag, so the tag alone does not prove a byte-reproducible archive. These limitations are recorded rather than hidden.

As an independent check, building commit `a91eb79d035d7a33300d2cc60b18db3f81a94621` with its frozen pnpm lock produced bytes identical to the npm artifact for all 16 packed files other than `package.json`. The package manifest difference was npm publication normalization: `prepare` and `packageManager` were omitted and the remaining `scripts` object was relocated. Archive bytes can also differ because of packing metadata. Therefore:

- the integrity-pinned npm tarball is authoritative for executable behavior and package contents;
- the content-addressed tag, commit, and tree are authoritative for readable source behavior;
- no claim of byte-for-byte rebuild reproducibility is made.

## Compatibility rule

Compatibility means equivalent **observable semantics** after normalizing explicitly non-contractual presentation. The native harness should compare process exit status, stdout/stderr roles and semantic messages, prompts and non-interactive decisions, filesystem trees and link topology, lock state, network/Git interactions, and launched child behavior.

Logo art, ANSI styling, spinners, exact whitespace, temporary paths, path separators, timestamps, and other decorative terminal layout are not contracts. TypeScript module boundaries and internal algorithms are not contracts. Known unsafe behavior listed under [Intentional divergences](#intentional-divergences) must not be copied.

The immutable [tagged source tree](https://github.com/EngBlock/open-skills/tree/a91eb79d035d7a33300d2cc60b18db3f81a94621) and tests decide details not summarized here.

## Reviewed observable surface

### Entry point, commands, and flags

Preserve `--help`/`-h`, `--version`/`-v`, no-command behavior (including agent-environment banner suppression), subcommand-help short-circuiting, and failure for unknown commands.

| Command | Baseline aliases and options |
| --- | --- |
| `add <source>` | `a`; `install` and `i` route through the same add/restore flow. `-g/--global`, multi-value `-a/--agent`, `-s/--skill`, `-l/--list`, `-y/--yes`, `--copy`, `--subagent`, `--all`, `--full-depth` |
| `use <source>` | `-s/--skill`, `-a/--agent`, `--full-depth`, `--dangerously-accept-openclaw-risks` |
| `remove [skills...]` | `rm`, `r`; `-g/--global`, multi-value `-a/--agent`, `-s/--skill`, `-y/--yes`, `--all` |
| `list` | `ls`; `-g/--global`, multi-value `-a/--agent`, `--json` |
| `find [query]` | `search`, `f`, `s`; offline decentralized-discovery guidance and failure status |
| `check [skills...]` | `-g/--global`, `-p/--project`, `-y/--yes` |
| `update [skills...]` | `upgrade`; `-g/--global`, `-p/--project`, `-y/--yes` |
| `init [name]` | Creates the baseline `SKILL.md` template in the current directory or named child directory |
| `experimental_install` | Restores skills from `skills-lock.json` |
| `experimental_sync` | Multi-value `-a/--agent`, `-y/--yes`, `-f/--force` reinstall |

Parser validation, repeatable/multi-value option behavior, `--all` expansion, interactive choices, ordinary `--yes` semantics, scope auto-detection, and normal numeric exits are part of the contract. `use` prints only its generated prompt to stdout unless `--agent` is supplied; baseline launch support is `claude-code` and `codex`, and child failure status propagates. See the [tagged CLI router](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/cli.ts) and [`use` implementation](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/use.ts).

### Sources and discovery

The accepted source language includes:

- local absolute and relative paths;
- GitHub `owner/repo`, `github:`, `owner/repo@skill`, repository URLs, and tree/subpath URLs;
- GitLab shorthand, repository URLs, nested groups, and tree/subpath URLs;
- generic HTTP(S), SSH, scp-style, `file`, and direct Git URLs;
- `#ref` and `#ref@skill` selectors;
- GitHub Enterprise selected through `GH_HOST`;
- HTTP well-known indexes and the `coinbase/agentWallet` source alias.

Discovery includes `SKILL.md` frontmatter parsing, path traversal rejection, deterministic name sanitization, plugin manifests, common skill containers, root-skill shallow shadowing, the explicit `--full-depth` override, and internal-skill opt-in behavior. See the [source parser](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/source-parser.ts), [discovery implementation](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/skills.ts), and [tagged README](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/README.md).

### Installation and agent topology

Preserve canonical project installation under `.agents/skills`, global/project scope decisions, symlink-versus-`--copy` behavior, agent detection and target paths, Eve subagent placement, deterministic sanitized names, and prompt selection behavior.

The reviewed baseline has these 75 agent identifiers:

```text
aider-desk, amp, antigravity, antigravity-cli, astrbot, autohand-code,
augment, bob, claude-code, openclaw, cline, codearts-agent, codebuddy,
codemaker, codestudio, codex, command-code, continue, cortex, crush, cursor,
deepagents, devin, dexto, droid, eve, firebender, forgecode, gemini-cli,
github-copilot, goose, grok, hermes-agent, inference-sh, iflow-cli, jazz,
junie, kilo, kimchi, kimi-code-cli, kiro-cli, kode, lingma, loaf, mcpjam,
mistral-vibe, moxby, mux, neovate, opencode, openhands, ona, pi, qoder,
qoder-cn, qwen-code, replit, reasonix, roo, rovodev, tabnine-cli, terramind,
tinycloud, trae, trae-cn, warp, windsurf, zed, zcode, zencoder, zenflow,
pochi, promptscript, adal, universal
```

The immutable [`AgentType` list](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/types.ts) and [agent path table](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/README.md#supported-agents) are normative for identifiers, detection, and exact locations.

### State and updates

Project state is `./skills-lock.json`, schema version 1, written in deterministic skill-name order. Its observable entries include source identity/type, optional source URL, ref, skill path and subagents, and computed content hash.

Global state is `$XDG_STATE_HOME/skills/.skill-lock.json` when `XDG_STATE_HOME` is set, otherwise `~/.agents/.skill-lock.json`, schema version 3. Its observable data includes source/ref/path identity, GitHub skill-folder tree hash, timestamps, plugin name, dismissed prompts, and last-selected agents.

List, install, remove, check, and update behavior includes scope selection, installed topology, selected agents, GitHub tree lookup and authentication fallback, hash comparisons, and lock effects. The baseline's silent empty-state fallback for malformed or older global locks is explicitly **not** preserved; see divergence D09. Sources: [project lock](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/local-lock.ts), [global lock](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/skill-lock.ts), and [update implementation](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/update.ts).

### Environment, Git, and network inputs

The baseline explicitly reads these environment inputs:

- source/state/network: `GH_HOST`, `GITHUB_TOKEN`, `GH_TOKEN`, `GIT_SSH_COMMAND`, `XDG_STATE_HOME`, `SKILLS_CLONE_TIMEOUT_MS`, and `INSTALL_INTERNAL_SKILLS`;
- agent path overrides: `XDG_CONFIG_HOME`, `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `VIBE_HOME`, `HERMES_HOME`, `AUTOHAND_HOME`, `GROK_HOME`, `APPDATA`, and `FLATPAK_XDG_CONFIG_HOME`;
- agent-environment detection: `AI_AGENT`, `CURSOR_TRACE_ID`, `CURSOR_AGENT`, `CURSOR_EXTENSION_HOST_ROLE`, `GEMINI_CLI`, `CODEX_SANDBOX`, `CODEX_CI`, `CODEX_THREAD_ID`, `ANTIGRAVITY_AGENT`, `AUGMENT_AGENT`, `OPENCODE_CLIENT`, `CLAUDECODE`, `CLAUDE_CODE`, `CLAUDE_CODE_IS_COWORK`, `REPL_ID`, `COPILOT_MODEL`, `COPILOT_ALLOW_ALL`, and `COPILOT_GITHUB_TOKEN`;
- Node wrapper: `NODE_DISABLE_COMPILE_CACHE` disables the optional startup compile cache but does not change CLI semantics.

The baseline clone timeout is 300 seconds; LFS smudge is disabled. GitHub access tries ordinary Git/API access before visible `gh` and SSH fallbacks. The baseline accepts Git protocols `https`, `http`, `ssh`, `git`, and `file` while rejecting command-executing `ext::` transports. Security-sensitive changes to these behaviors are catalogued below. See the [tagged Git implementation](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/git.ts), [agent paths](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/agents.ts), and [agent-environment detection](https://github.com/EngBlock/open-skills/blob/a91eb79d035d7a33300d2cc60b18db3f81a94621/src/detect-agent.ts).

## Intentional divergences

These decisions are already approved by [issue #10](https://github.com/EngBlock/open-skills/issues/10). They are compatibility exceptions, not optional enhancements. Every implemented divergence requires a migration note and externally observable regression coverage using its ID.

| ID | Required native divergence |
| --- | --- |
| D01 | Ship only `open-skills`; native packages must not install `skills` or `add-skill` aliases. Git is the only documented runtime dependency. |
| D02 | No automatic telemetry, audit, registry/search calls, version checks, crash reporting, or self-update. Local commands and decentralized `find` guidance remain offline. |
| D03 | Reject plaintext HTTP/Git by default; require dedicated authorization and warning, bound redirects, drop credentials across hosts, and redact secrets from persistence and diagnostics. |
| D04 | Resolve and record an exact Git commit, then install the checked commit so branch movement cannot create a check/install race. |
| D05 | Treat repositories as inert untrusted data: execute no repository scripts/hooks, invoke subprocesses without a shell, initialize no submodules, and reject selected Git LFS pointer content. |
| D06 | Bound files, bytes, and traversal depth; confine copied symlink targets and reject escaping/broken/cyclic links; detect normalized-name ambiguity and require explicit selection. |
| D07 | Prevent silent cross-source replacement and local-change loss. `--replace` and `--force` authorize distinct risks; `--yes` implies neither. |
| D08 | Stage and journal recoverable mutations, roll back where practical, report cross-filesystem limits honestly, and protect writes with bounded OS advisory locks. |
| D09 | Preserve and safely reject malformed, older, newer, and unknown lock data rather than resetting it; retain safe unknown fields and make native migration forward-only. |
| D10 | Gate remote `use --agent` instruction injection on sanitized source plus exact-commit trust. Provide dedicated non-interactive authorization and offline trust list/revoke/clear. |
| D11 | Add deterministic versioned JSON for management commands, machine-only stdout, stderr diagnostics, symbolic JSON errors, and no prompts/colors in JSON mode while retaining baseline numeric exits. |
| D12 | Prefer `OPEN_SKILLS_*`; retain baseline legacy variables through 1.x with deterministic precedence, deprecation diagnostics, and namespaced warning suppression. |
| D13 | Distribute checksummed, signed, provenanced native artifacts from immutable tags. Do not turn npm into a native downloader and do not recommend `curl | sh`. |

## Change control

The oracle manifest and this document form one reviewed decision record:

1. Differential tests must invoke the artifact identified by the manifest, not `latest`, current `main`, or a freshly packed current checkout.
2. New npm or upstream releases do not alter native compatibility expectations.
3. A proposed baseline replacement requires a dedicated issue that records a new artifact, integrity, tag object, source commit/tree, compatibility review, and divergence review.
4. Editing a digest or Git identity without that decision is a review blocker; `pnpm verify:native-baseline` fails if remote bytes or object links differ.
5. Native implementation may add only the approved divergences above until another explicit product decision is accepted.
