# open-skills

The standalone Go CLI for the open agent skills ecosystem. Native releases require system Git at runtime and do not require Node.js, npm, or pnpm.

<!-- agent-list:start -->
Supports **OpenCode**, **Claude Code**, **Codex**, **Cursor**, and [70 more](#supported-agents).
<!-- agent-list:end -->

[![CI](https://github.com/EngBlock/open-skills/actions/workflows/ci.yml/badge.svg)](https://github.com/EngBlock/open-skills/actions/workflows/ci.yml)
[![GitHub release](https://img.shields.io/github/v/release/EngBlock/open-skills)](https://github.com/EngBlock/open-skills/releases/latest)

## Install

### Scoop (experimental Windows x86-64 native)

The native Windows x86-64 build is distributed from its checksummed, immutable GitHub Release archive. Windows x86-64 support remains experimental rather than fully supported. The package places only `open-skills.exe` on PATH, requires system Git at runtime, and does not require Node.js or npm.

```powershell
scoop bucket add open-skills https://github.com/EngBlock/open-skills
scoop install open-skills/open-skills
```

Scoop owns executable upgrades; `open-skills update` updates installed skills instead:

```powershell
scoop update
scoop update open-skills
```

### Homebrew (macOS ARM64 native)

The supported macOS ARM64 native release is installed from its checksummed, immutable GitHub Release archive. It places only `open-skills` on PATH and requires system Git at runtime.

```bash
brew tap EngBlock/open-skills https://github.com/EngBlock/open-skills
brew install EngBlock/open-skills/open-skills
```

Homebrew owns executable upgrades; `open-skills update` updates installed skills instead:

```bash
brew update
brew upgrade EngBlock/open-skills/open-skills
```

Native 0.2.2 is the production release.

### Verified direct archives

GitHub Releases are the canonical source for supported and experimental archives. Download the archive for your platform together with `checksums.txt`, its adjacent Sigstore bundle, and `provenance.sigstore.json`. Verify the selected checksum, keyless signature, repository, tag, and producing workflow before extracting the executable. The [native migration guide](docs/native-migration.md#verified-direct-archive) gives exact download, verification, and installation commands.

Direct archives contain only `open-skills` (`open-skills.exe` on Windows), require system Git, and do not require Node.js or npm.

### Migrate from the retired npm implementation

The JavaScript npm package is retired and frozen at 0.1.2. It will receive no new features and will not download or bootstrap the native executable. Remove it after installing native `open-skills` through Homebrew, Scoop, or a verified direct archive:

```bash
npm uninstall --global @engblock/open-skills
```

Existing skill and lock state remains in place. See the [native migration guide](docs/native-migration.md) for state compatibility, forward-only metadata, legacy environment variables, and intentional behavior changes.

## Install a Skill

```bash
open-skills add EngBlock/open-skills@find-skills
```

## Use a Skill Without Installing

Generate a prompt for one skill, or start a supported coding agent interactively:

```bash
open-skills use owner/repo@web-design-guidelines | claude
open-skills use owner/repo --skill web-design-guidelines --agent claude-code
```

`open-skills use` resolves sources the same way as `open-skills add`, writes the selected skill files to a temporary directory, and prints only the generated prompt to stdout unless `--agent` is provided. With `--agent`, it starts one supported agent interactively with the generated prompt.

### Source Formats

```bash
# GitHub shorthand (owner/repo)
open-skills add owner/repo

# Full GitHub URL
open-skills add https://github.com/owner/repo

# Direct path to a skill in a repo
open-skills add https://github.com/owner/repo/tree/main/skills/web-design-guidelines

# GitLab URL
open-skills add https://gitlab.com/org/repo

# Any git URL
open-skills add git@github.com:owner/repo.git

# Local path
open-skills add ./my-local-skills
```

### Options

| Option                    | Description                                                                                                                                        |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `-g, --global`            | Install to user directory instead of project                                                                                                       |
| `-a, --agent <agents...>` | <!-- agent-names:start -->Target specific agents (e.g., `claude-code`, `codex`). See [Supported Agents](#supported-agents)<!-- agent-names:end --> |
| `-s, --skill <skills...>` | Install specific skills by name (use `'*'` for all skills)                                                                                         |
| `-l, --list`              | List available skills without installing                                                                                                           |
| `--copy`                  | Copy files instead of symlinking to agent directories                                                                                              |
| `-y, --yes`               | Skip all confirmation prompts                                                                                                                      |
| `--all`                   | Install all skills to all agents without prompts                                                                                                   |

### Examples

```bash
# List skills in a repository
open-skills add owner/repo --list

# Install specific skills
open-skills add owner/repo --skill frontend-design --skill skill-creator

# Install a skill with spaces in the name (must be quoted)
open-skills add owner/repo --skill "Convex Best Practices"

# Install to specific agents
open-skills add owner/repo -a claude-code -a opencode

# Non-interactive installation (CI/CD friendly)
open-skills add owner/repo --skill frontend-design -g -a claude-code -y

# Install all skills from a repo to all agents
open-skills add owner/repo --all

# Install all skills to specific agents
open-skills add owner/repo --skill '*' -a claude-code

# Install specific skills to all agents
open-skills add owner/repo --agent '*' --skill frontend-design
```

### Installation Scope

| Scope       | Flag      | Location            | Use Case                                      |
| ----------- | --------- | ------------------- | --------------------------------------------- |
| **Project** | (default) | `./<agent>/skills/` | Committed with your project, shared with team |
| **Global**  | `-g`      | `~/<agent>/skills/` | Available across all projects                 |

### Installation Methods

When installing interactively, you can choose:

| Method                    | Description                                                                                 |
| ------------------------- | ------------------------------------------------------------------------------------------- |
| **Symlink** (Recommended) | Creates symlinks from each agent to a canonical copy. Single source of truth, easy updates. |
| **Copy**                  | Creates independent copies for each agent. Use when symlinks aren't supported.              |

## Other Commands

| Command                        | Description                                       |
| ------------------------------ | ------------------------------------------------- |
| `open-skills use <source>`      | Use one skill without installing                  |
| `open-skills list`              | List installed skills (alias: `ls`)               |
| `open-skills find [query]`      | Show decentralized-discovery migration guidance   |
| `open-skills remove [skills]`   | Remove installed skills from agents               |
| `open-skills update [skills]`   | Update installed skills to latest versions        |
| `open-skills init [name]`       | Create a new SKILL.md template                    |

### `open-skills list`

List all installed skills. Similar to `npm ls`.

```bash
# List all installed skills (project and global)
open-skills list

# List only global skills
open-skills ls -g

# Filter by specific agents
open-skills ls -a claude-code -a cursor
```

### `open-skills find`

Hosted skill search has been removed. The legacy `find`, `search`, `f`, and `s` commands print offline migration guidance and exit with status 1, including when given an old query or `--owner` option.

Discover skills by searching GitHub and the web for relevant `SKILL.md` files and inspecting their contents. Install a discovered skill directly from its source:

```bash
open-skills add <owner>/<repo>@<skill>
```

### `open-skills update`

```bash
# Update all skills (interactive scope prompt)
open-skills update

# Update a single skill by name
open-skills update my-skill

# Update multiple specific skills
open-skills update frontend-design web-design-guidelines

# Update only global or project skills
open-skills update -g
open-skills update -p

# Non-interactive (auto-detects scope: project if in a project, else global)
open-skills update -y
```

| Option          | Description                                                               |
| --------------- | ------------------------------------------------------------------------- |
| `-g, --global`  | Only update global skills                                                 |
| `-p, --project` | Only update project skills                                                |
| `-y, --yes`     | Skip scope prompt (auto-detect: project if in a project dir, else global) |
| `[skills...]`   | Update specific skills by name instead of all                             |

### `open-skills init`

```bash
# Create SKILL.md in current directory
open-skills init

# Create a new skill in a subdirectory
open-skills init my-skill
```

### `open-skills remove`

Remove installed skills from agents.

```bash
# Remove interactively (select from installed skills)
open-skills remove

# Remove specific skill by name
open-skills remove web-design-guidelines

# Remove multiple skills
open-skills remove frontend-design web-design-guidelines

# Remove from global scope
open-skills remove --global web-design-guidelines

# Remove from specific agents only
open-skills remove --agent claude-code cursor my-skill

# Remove all installed skills without confirmation
open-skills remove --all

# Remove all skills from a specific agent
open-skills remove --skill '*' -a cursor

# Remove a specific skill from all agents
open-skills remove my-skill --agent '*'

# Use 'rm' alias
open-skills rm my-skill
```

| Option         | Description                                      |
| -------------- | ------------------------------------------------ |
| `-g, --global` | Remove from global scope (~/) instead of project |
| `-a, --agent`  | Remove from specific agents (use `'*'` for all)  |
| `-s, --skill`  | Specify skills to remove (use `'*'` for all)     |
| `-y, --yes`    | Skip confirmation prompts                        |
| `--all`        | Shorthand for `--skill '*' --agent '*' -y`       |

## What are Agent Skills?

Agent skills are reusable instruction sets that extend your coding agent's capabilities. They're defined in `SKILL.md`
files with YAML frontmatter containing a `name` and `description`.

Skills let agents perform specialized tasks like:

- Generating release notes from git history
- Creating PRs following your team's conventions
- Integrating with external tools (Linear, Notion, etc.)

Discover skills by searching GitHub and the web for relevant `SKILL.md` files and inspecting their contents before installation.

## Supported Agents

Skills can be installed to any of these agents:

<!-- supported-agents:start -->
| Agent | `--agent` | Project Path | Global Path |
|-------|-----------|--------------|-------------|
| AiderDesk | `aider-desk` | `.aider-desk/skills/` | `~/.aider-desk/skills/` |
| Amp, Replit, Universal | `amp`, `replit`, `universal` | `.agents/skills/` | `~/.config/agents/skills/` |
| Antigravity | `antigravity` | `.agents/skills/` | `~/.gemini/antigravity/skills/` |
| Antigravity CLI | `antigravity-cli` | `.agents/skills/` | `~/.gemini/antigravity-cli/skills/` |
| AstrBot | `astrbot` | `data/skills/` | `~/.astrbot/data/skills/` |
| Autohand Code CLI | `autohand-code` | `.autohand/skills/` | `~/.autohand/skills/` |
| Augment | `augment` | `.augment/skills/` | `~/.augment/skills/` |
| IBM Bob | `bob` | `.bob/skills/` | `~/.bob/skills/` |
| Claude Code | `claude-code` | `.claude/skills/` | `~/.claude/skills/` |
| OpenClaw | `openclaw` | `skills/` | `~/.openclaw/skills/` |
| Cline, Dexto, Kimi Code CLI, Loaf, Warp, Zed | `cline`, `dexto`, `kimi-code-cli`, `loaf`, `warp`, `zed` | `.agents/skills/` | `~/.agents/skills/` |
| CodeArts Agent | `codearts-agent` | `.codeartsdoer/skills/` | `~/.codeartsdoer/skills/` |
| CodeBuddy | `codebuddy` | `.codebuddy/skills/` | `~/.codebuddy/skills/` |
| Codemaker | `codemaker` | `.codemaker/skills/` | `~/.codemaker/skills/` |
| Code Studio | `codestudio` | `.codestudio/skills/` | `~/.codestudio/skills/` |
| Codex | `codex` | `.agents/skills/` | `~/.codex/skills/` |
| Command Code | `command-code` | `.commandcode/skills/` | `~/.commandcode/skills/` |
| Continue | `continue` | `.continue/skills/` | `~/.continue/skills/` |
| Cortex Code | `cortex` | `.cortex/skills/` | `~/.snowflake/cortex/skills/` |
| Crush | `crush` | `.crush/skills/` | `~/.config/crush/skills/` |
| Cursor | `cursor` | `.agents/skills/` | `~/.cursor/skills/` |
| Deep Agents | `deepagents` | `.agents/skills/` | `~/.deepagents/agent/skills/` |
| Devin for Terminal | `devin` | `.devin/skills/` | `~/.config/devin/skills/` |
| Droid | `droid` | `.factory/skills/` | `~/.factory/skills/` |
| Eve | `eve` | `agent/skills/` | N/A (project-only) |
| Firebender | `firebender` | `.agents/skills/` | `~/.firebender/skills/` |
| ForgeCode | `forgecode` | `.forge/skills/` | `~/.forge/skills/` |
| Gemini CLI | `gemini-cli` | `.agents/skills/` | `~/.gemini/skills/` |
| GitHub Copilot | `github-copilot` | `.agents/skills/` | `~/.copilot/skills/` |
| Goose | `goose` | `.goose/skills/` | `~/.config/goose/skills/` |
| Grok Build | `grok` | `.grok/skills/` | `~/.grok/skills/` |
| Hermes Agent | `hermes-agent` | `.hermes/skills/` | `~/.hermes/skills/` |
| inference.sh | `inference-sh` | `.inferencesh/skills/` | `~/.inferencesh/skills/` |
| Jazz | `jazz` | `.jazz/skills/` | `~/.jazz/skills/` |
| Junie | `junie` | `.junie/skills/` | `~/.junie/skills/` |
| iFlow CLI | `iflow-cli` | `.iflow/skills/` | `~/.iflow/skills/` |
| Kilo Code | `kilo` | `.kilocode/skills/` | `~/.kilocode/skills/` |
| Kimchi | `kimchi` | `.kimchi/skills/` | `~/.config/kimchi/harness/skills/` |
| Kiro CLI | `kiro-cli` | `.kiro/skills/` | `~/.kiro/skills/` |
| Kode | `kode` | `.kode/skills/` | `~/.kode/skills/` |
| Lingma | `lingma` | `.lingma/skills/` | `~/.lingma/skills/` |
| MCPJam | `mcpjam` | `.mcpjam/skills/` | `~/.mcpjam/skills/` |
| Mistral Vibe | `mistral-vibe` | `.vibe/skills/` | `~/.vibe/skills/` |
| Moxby | `moxby` | `.moxby/skills/` | `~/.moxby/skills/` |
| Mux | `mux` | `.mux/skills/` | `~/.mux/skills/` |
| OpenCode | `opencode` | `.agents/skills/` | `~/.config/opencode/skills/` |
| OpenHands | `openhands` | `.openhands/skills/` | `~/.openhands/skills/` |
| Ona | `ona` | `.ona/skills/` | `~/.ona/skills/` |
| Pi | `pi` | `.agents/skills/` | `~/.pi/agent/skills/` |
| Qoder | `qoder` | `.qoder/skills/` | `~/.qoder/skills/` |
| Qoder CN | `qoder-cn` | `.qoder/skills/` | `~/.qoder-cn/skills/` |
| Qwen Code | `qwen-code` | `.qwen/skills/` | `~/.qwen/skills/` |
| Reasonix | `reasonix` | `.reasonix/skills/` | `~/.reasonix/skills/` |
| Rovo Dev | `rovodev` | `.rovodev/skills/` | `~/.rovodev/skills/` |
| Roo Code | `roo` | `.roo/skills/` | `~/.roo/skills/` |
| Tabnine CLI | `tabnine-cli` | `.tabnine/agent/skills/` | `~/.tabnine/agent/skills/` |
| Terramind | `terramind` | `.terramind/skills/` | `~/.terramind/skills/` |
| Tinycloud | `tinycloud` | `.tinycloud/skills/` | `~/.tinycloud/skills/` |
| Trae | `trae` | `.trae/skills/` | `~/.trae/skills/` |
| Trae CN | `trae-cn` | `.trae/skills/` | `~/.trae-cn/skills/` |
| Windsurf | `windsurf` | `.windsurf/skills/` | `~/.codeium/windsurf/skills/` |
| ZCode | `zcode` | `.zcode/skills/` | `~/.zcode/skills/` |
| Zencoder, Zenflow | `zencoder`, `zenflow` | `.zencoder/skills/` | `~/.zencoder/skills/` |
| Neovate | `neovate` | `.neovate/skills/` | `~/.neovate/skills/` |
| Pochi | `pochi` | `.pochi/skills/` | `~/.pochi/skills/` |
| PromptScript | `promptscript` | `.agents/skills/` | N/A (project-only) |
| AdaL | `adal` | `.adal/skills/` | `~/.adal/skills/` |
<!-- supported-agents:end -->

> [!NOTE]
> **Kiro CLI users:** The default agent automatically loads skills from `.kiro/skills/` and `~/.kiro/skills/` — no
> configuration needed. If you use a **custom agent**, add skills to its `resources` in `.kiro/agents/<agent>.json`:
>
> ```json
> {
>   "resources": ["skill://.kiro/skills/**/SKILL.md"]
> }
> ```

The CLI automatically detects which coding agents you have installed. If none are detected, you'll be prompted to select
which agents to install to.

## Creating Skills

Skills are directories containing a `SKILL.md` file with YAML frontmatter:

```markdown
---
name: my-skill
description: What this skill does and when to use it
---

# My Skill

Instructions for the agent to follow when this skill is activated.

## When to Use

Describe the scenarios where this skill should be used.

## Steps

1. First, do this
2. Then, do that
```

### Required Fields

- `name`: Unique identifier (lowercase, hyphens allowed)
- `description`: Brief explanation of what the skill does

### Optional Fields

- `metadata.internal`: Set to `true` to hide the skill from normal discovery. In the native release, internal skills
  are visible when `OPEN_SKILLS_INSTALL_INTERNAL_SKILLS=1` is set; the npm 0.1.x baseline and native 1.x compatibility
  also accept `INSTALL_INTERNAL_SKILLS`. Useful for work-in-progress skills or skills meant only for internal tooling.

```markdown
---
name: my-internal-skill
description: An internal skill not shown by default
metadata:
  internal: true
---
```

### Skill Discovery

The CLI searches for skills in these locations within a repository. Each
skill container directory is walked one level deep for the common flat
layout (`skills/<name>/SKILL.md`) and one extra level deep for catalog
layouts (`skills/<category>/<name>/SKILL.md`). A `SKILL.md` discovered at
the shallower level shadows anything nested below it. Use `--full-depth`
to also discover `SKILL.md` files outside these container directories
(e.g. under `examples/` or `tests/`).

<!-- skill-discovery:start -->
- Root directory (if it contains `SKILL.md`)
- `skills/`
- `skills/.curated/`
- `skills/.experimental/`
- `skills/.system/`
- `.aider-desk/skills/`
- `.agents/skills/`
- `data/skills/`
- `.autohand/skills/`
- `.augment/skills/`
- `.bob/skills/`
- `.claude/skills/`
- `.codeartsdoer/skills/`
- `.codebuddy/skills/`
- `.codemaker/skills/`
- `.codestudio/skills/`
- `.commandcode/skills/`
- `.continue/skills/`
- `.cortex/skills/`
- `.crush/skills/`
- `.devin/skills/`
- `.factory/skills/`
- `agent/skills/`
- `.forge/skills/`
- `.goose/skills/`
- `.grok/skills/`
- `.hermes/skills/`
- `.inferencesh/skills/`
- `.jazz/skills/`
- `.junie/skills/`
- `.iflow/skills/`
- `.kilocode/skills/`
- `.kimchi/skills/`
- `.kiro/skills/`
- `.kode/skills/`
- `.lingma/skills/`
- `.mcpjam/skills/`
- `.vibe/skills/`
- `.moxby/skills/`
- `.mux/skills/`
- `.openhands/skills/`
- `.ona/skills/`
- `.pi/skills/`
- `.qoder/skills/`
- `.qwen/skills/`
- `.reasonix/skills/`
- `.rovodev/skills/`
- `.roo/skills/`
- `.tabnine/agent/skills/`
- `.terramind/skills/`
- `.tinycloud/skills/`
- `.trae/skills/`
- `.windsurf/skills/`
- `.zcode/skills/`
- `.zencoder/skills/`
- `.neovate/skills/`
- `.pochi/skills/`
- `.adal/skills/`
<!-- skill-discovery:end -->

### Plugin Manifest Discovery

If `.claude-plugin/marketplace.json` or `.claude-plugin/plugin.json` exists, skills declared in those files are also discovered:

```json
// .claude-plugin/marketplace.json
{
  "metadata": { "pluginRoot": "./plugins" },
  "plugins": [
    {
      "name": "my-plugin",
      "source": "my-plugin",
      "skills": ["./skills/review", "./skills/test"]
    }
  ]
}
```

This enables compatibility with the [Claude Code plugin marketplace](https://code.claude.com/docs/en/plugin-marketplaces) ecosystem. Skill paths declared in a manifest are searched at their declared depth and are not subject to the depth-2 catalog walk described above.

If no skills are found in standard locations, a recursive search is performed.

## Compatibility

Skills are generally compatible across agents since they follow a
shared [Agent Skills specification](https://agentskills.io). However, some features may be agent-specific:

| Feature         | OpenCode | OpenHands | Claude Code | Cline | CodeBuddy | Codex | Command Code | Kiro CLI | Cursor | Antigravity | Roo Code | Github Copilot | Amp | OpenClaw | Neovate | Pi  | Qoder | Zencoder |
| --------------- | -------- | --------- | ----------- | ----- | --------- | ----- | ------------ | -------- | ------ | ----------- | -------- | -------------- | --- | -------- | ------- | --- | ----- | -------- |
| Basic skills    | Yes      | Yes       | Yes         | Yes   | Yes       | Yes   | Yes          | Yes      | Yes    | Yes         | Yes      | Yes            | Yes | Yes      | Yes     | Yes | Yes   | Yes      |
| `allowed-tools` | Yes      | Yes       | Yes         | Yes   | Yes       | Yes   | Yes          | No       | Yes    | Yes         | Yes      | Yes            | Yes | Yes      | Yes     | Yes | Yes   | No       |
| `context: fork` | No       | No        | Yes         | No    | No        | No    | No           | No       | No     | No          | No       | No             | No  | No       | No      | No  | No    | No       |
| Hooks           | No       | No        | Yes         | Yes   | No        | No    | No           | Yes      | No     | No          | No       | No             | No  | No       | No      | No  | No    | No       |

## Troubleshooting

### "No skills found"

Ensure the repository contains valid `SKILL.md` files with both `name` and `description` in the frontmatter.

### Skill not loading in agent

- Verify the skill was installed to the correct path
- Check the agent's documentation for skill loading requirements
- Ensure the `SKILL.md` frontmatter is valid YAML

### Permission errors

Ensure you have write access to the target directory.

## Environment Variables

The native release uses canonical `OPEN_SKILLS_` names for Open Skills-owned
configuration. Legacy names remain supported through native 1.x and emit a
migration warning on stderr; a present canonical value always wins a conflict.
The npm 0.1.x implementation continues to use the legacy names shown below.

| Native canonical variable | Description | Legacy name through 1.x |
| --- | --- | --- |
| `OPEN_SKILLS_CLONE_TIMEOUT_MS` | Positive Git timeout in milliseconds (default `300000`) | `SKILLS_CLONE_TIMEOUT_MS` |
| `OPEN_SKILLS_INSTALL_INTERNAL_SKILLS` | Set to exactly `1` or `true` to show and install `metadata.internal: true` skills | `INSTALL_INTERNAL_SKILLS` |
| `OPEN_SKILLS_LOCK_TIMEOUT_MS` | Non-negative advisory-lock wait in milliseconds (default `10000`) | None |
| `OPEN_SKILLS_SUPPRESS_LEGACY_ENV_WARNINGS` | Set to exactly `1` or `true` to suppress only legacy-name migration warnings | None |

```bash
# Native release: include internal skills
OPEN_SKILLS_INSTALL_INTERNAL_SKILLS=1 open-skills add owner/repo --list
```

Standard ecosystem and third-party variables such as `NO_COLOR`, `XDG_*`,
`GH_HOST`, Git credentials/configuration, and agent-owned home variables retain
their established names. See the [native D12 migration notes](docs/native-migration.md#d12-namespaced-configuration-and-exact-authorization)
for precedence, deprecation, and exact authorization semantics.

## Related Links

- [Agent Skills Specification](https://agentskills.io)
- [Amp Skills Documentation](https://ampcode.com/manual#agent-skills)
- [Antigravity Skills Documentation](https://antigravity.google/docs/skills)
- [Factory AI / Droid Skills Documentation](https://docs.factory.ai/cli/configuration/skills)
- [Claude Code Skills Documentation](https://code.claude.com/docs/en/skills)
- [OpenClaw Skills Documentation](https://docs.openclaw.ai/tools/skills)
- [Cline Skills Documentation](https://docs.cline.bot/features/skills)
- [CodeBuddy Skills Documentation](https://www.codebuddy.ai/docs/ide/Features/Skills)
- [Codex Skills Documentation](https://developers.openai.com/codex/skills)
- [Command Code Skills Documentation](https://commandcode.ai/docs/skills)
- [Crush Skills Documentation](https://github.com/charmbracelet/crush?tab=readme-ov-file#agent-skills)
- [Cursor Skills Documentation](https://cursor.com/docs/context/skills)
- [Firebender Skills Documentation](https://docs.firebender.com/multi-agent/skills)
- [Gemini CLI Skills Documentation](https://geminicli.com/docs/cli/skills/)
- [GitHub Copilot Agent Skills](https://docs.github.com/en/copilot/concepts/agents/about-agent-skills)
- [iFlow CLI Skills Documentation](https://platform.iflow.cn/en/cli/examples/skill)
- [Kimi Code CLI Skills Documentation](https://moonshotai.github.io/kimi-code/en/customization/skills)
- [Kiro CLI Skills Documentation](https://kiro.dev/docs/cli/custom-agents/configuration-reference/#skill-resources)
- [Kode Skills Documentation](https://github.com/shareAI-lab/kode/blob/main/docs/skills.md)
- [OpenCode Skills Documentation](https://opencode.ai/docs/skills)
- [Qwen Code Skills Documentation](https://qwenlm.github.io/qwen-code-docs/en/users/features/skills/)
- [OpenHands Skills Documentation](https://docs.openhands.ai/modules/usage/how-to/using-skills)
- [Pi Skills Documentation](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/skills.md)
- [Qoder Skills Documentation](https://docs.qoder.com/cli/Skills)
- [Replit Skills Documentation](https://docs.replit.com/replitai/skills)
- [Roo Code Skills Documentation](https://docs.roocode.com/features/skills)
- [Trae Skills Documentation](https://docs.trae.ai/ide/skills)

## Development

The active source tree is Go-only. Build, format, vet, and test it with the Go toolchain declared by `go.mod`:

```sh
gofmt -w cmd internal
go vet ./...
go test ./... -count=1
CGO_ENABLED=0 go build -trimpath -o build/open-skills ./cmd/open-skills
```

Ordinary pull-request and `main` push CI intentionally has one Ubuntu Go validation job. It fails closed on formatting, vetting, and the complete test suite. As part of that suite, the native release test builds every CGO-disabled release target once and smoke-tests the production Linux executable; only the security-sensitive compatibility subset is rerun inside a network namespace. Native macOS ARM64 Homebrew and experimental Windows Scoop checks run only against release artifacts in the release workflows; ordinary CI does not repeat the full suite across operating systems or Go versions.

See [native development](docs/native-development.md) for the compatibility corpus and signed native release process. The protected [`v0.1.2`](https://github.com/EngBlock/open-skills/tree/v0.1.2) tag and checked-in compatibility manifests preserve the retired implementation for historical inspection; they are not active development dependencies.

## Origin

Open Skills originated as a fork of [vercel-labs/skills](https://github.com/vercel-labs/skills).
Existing MIT notices and upstream attribution are preserved.

## License

MIT
