# Native JSON automation contract

The native preview exposes schema-versioned JSON for the core management commands and offline trust inspection:

```sh
open-skills --json list
open-skills check --json
open-skills add <source> --json [selection options]
open-skills remove <skill> --json --yes
open-skills update --json [scope options]
open-skills --json trust list
```

`--json` may appear before the command or anywhere in its arguments. Aliases use the same schema as their canonical command. JSON mode is intentionally unsupported for commands such as `use`: launched agents retain direct terminal I/O and their exact exit status.

## Process contract

- stdout contains exactly one newline-terminated JSON document on success or ordinary failure.
- Human diagnostics, lock-wait notices, transport warnings, and subprocess notices are written only to stderr.
- JSON mode emits no logo, color, spinner, progress line, or prompt. It never reads a decision from stdin.
- JSON mode does not imply `--yes`, `--force`, `--replace`, `--trust`, or `--allow-insecure-transport`. A required selection or confirmation fails symbolically before mutation.
- Normal success remains exit status `0`; normal validation, inspection, or mutation failure remains `1`. JSON does not change launched-agent exit passthrough because `use` does not accept JSON mode.
- Object fields follow the order shown below. Skills and results are sorted by scope and canonical skill name; agent IDs use registry order; trust approvals are sorted by source and commit. Empty result arrays are `[]`, not `null`.
- Additive fields may be introduced in a schema version. Removing a field or changing its meaning requires a new version.

## Management failure schema

List, check, add, remove, and update failures use:

```json
{
  "schemaVersion": 1,
  "error": {
    "code": "invalid_arguments",
    "message": "Unknown option: --example"
  }
}
```

`error.code` is the stable automation value. `message` is human-readable and may improve without a schema bump. State-inspection errors may also include `path`.

Stable version 1 codes are:

| Code | Meaning |
| --- | --- |
| `invalid_arguments` | An option or required argument is invalid. |
| `invalid_agent` | An agent ID or agent/scope combination is invalid. |
| `selection_required` | Explicit `--skill`, `--skill-path`, `--agent`, or `--all` input is required because JSON mode cannot prompt. |
| `selection_failed` | Explicit selectors do not identify an installable/removable skill. |
| `confirmation_required` | The operation requires an explicit `--yes` authorization. |
| `replacement_requires_authorization` | An existing skill has different provenance and requires explicit `--replace`. |
| `state_malformed` | A managed state document is malformed. |
| `state_version_older` | A managed state document uses an unsupported older version. |
| `state_version_newer` | A managed state document uses an unknown newer version. |
| `state_unreadable` | Managed state could not be read safely. |
| `partial_failure` | Check or update inspected multiple entries and at least one failed. |
| `operation_failed` | Acquisition, locking, inspection, or mutation failed; stderr contains the bounded diagnostic. |
| `result_unavailable` | A covered command completed without producing its required schema. |
| `json_not_supported` | JSON was requested for a command or mode outside this contract. |

## List schema version 1

```json
{
  "schemaVersion": 1,
  "scope": "project",
  "skills": [
    {
      "name": "example",
      "path": "/absolute/project/.agents/skills/example",
      "scope": "project",
      "agents": ["Claude Code"],
      "source": "owner/repository",
      "sourceUrl": "https://github.com/owner/repository",
      "sourceType": "github"
    }
  ]
}
```

`scope` is `project` or `global`. Provenance fields are `null` when no value is recorded. Agent values retain the established display-name contract.

## Add schema version 1

A successful installation returns a deterministic installed destination path, registry agent IDs, and credential-free provenance:

```json
{
  "schemaVersion": 1,
  "scope": "project",
  "installed": [
    {
      "name": "example",
      "path": "/absolute/project/.agents/skills/example",
      "agents": ["universal"],
      "source": "/absolute/source",
      "sourceType": "local",
      "revision": null
    }
  ]
}
```

Remote Git results set `revision` to the immutable checked commit. `add --list --json` is read-only and uses the alternate success field:

```json
{
  "schemaVersion": 1,
  "scope": "project",
  "available": [
    { "name": "example", "path": "skills/example" }
  ]
}
```

## Remove schema version 1

Removal requires explicit skill selectors and `--yes` unless `--all` already supplies that authorization:

```json
{
  "schemaVersion": 1,
  "scope": "project",
  "removed": [
    { "name": "example", "agents": ["universal"] }
  ]
}
```

A successful no-op returns an empty `removed` array.

## Check and update schema version 1

Check and update share a result schema. `scope` is `project`, `global`, or `both`.

```json
{
  "schemaVersion": 1,
  "scope": "project",
  "results": [
    {
      "name": "example",
      "scope": "project",
      "status": "update_available",
      "revision": "0123456789abcdef"
    }
  ],
  "summary": {
    "checked": 1,
    "updated": 0,
    "failed": 0
  }
}
```

Per-skill status is one of `unchanged`, `update_available`, `updated`, `removed`, `missing_upstream`, or `failed`. `revision` is present when the remote source supplies an immutable Git commit. A partial failure retains all deterministic results, adds a top-level `error` with code `partial_failure`, and exits `1`.

## Trust inspection schema version 1

`trust list` retains its established `version` field:

```json
{
  "version": 1,
  "approvals": [
    {
      "source": "owner/repository",
      "commit": "0123456789abcdef",
      "approvedAt": "2026-01-02T03:04:05Z"
    }
  ]
}
```

Trust-store and option failures use the same stable error object while retaining the trust schema's version field:

```json
{
  "version": 1,
  "error": {
    "code": "invalid_arguments",
    "message": "Unknown option: --example"
  }
}
```

Trust inspection is offline. Trust mutation commands are not part of the version 1 JSON automation contract.
