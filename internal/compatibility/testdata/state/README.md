# Existing state compatibility fixtures

These fixtures model state already written by the two approved native migration baselines:

- `upstream-v1.5.20` follows the project/global schemas and locations in `vercel-labs/skills` at commit `c042b919` (release v1.5.20).
- `npm-0.1.2` follows the schemas and locations in the integrity-pinned `@engblock/open-skills@0.1.2` artifact and tag recorded in `compatibility/npm-0.1.2/oracle.json`.

Project fixtures use `skills-lock.json` beside the canonical `.agents/skills` tree. Global fixtures cover both the XDG state location and the legacy `~/.agents/.skill-lock.json` location. Hashes and timestamps are stable test values; field shapes and installation paths match the corresponding writer.
