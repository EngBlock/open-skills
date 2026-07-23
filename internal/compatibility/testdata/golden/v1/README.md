# Reviewed native compatibility corpus v1

This directory freezes the process-level outcomes approved for issue #40.
`manifest.json` contains the complete scenario inputs, coverage tags, the exact
`@engblock/open-skills@0.1.2` oracle-manifest digest, and the D01–D13 divergence
index. `outcomes/` contains one normalized full process observation per stable
scenario ID. `locks/` freezes valid, malformed, older, newer, and unknown-field
project and global state.

Normal CI runs `TestNativeGoldenCorpus`. It builds or accepts one native
`open-skills` executable, executes only that binary, and fails if Node, npm, or
npx is observed. The Linux CI invocation also runs inside a network namespace;
only loopback HTTP fixtures and deterministic local Git repositories are used.

The normalizer is closed and versioned. It only removes ANSI from presentation
streams, converts CRLF in presentation/lock text, tokens exact sandbox paths and
the fixture host, canonicalizes separators in path fields, tokens generated
workspace/transaction identifiers, preserves file kinds plus portable Unix
permission bits (symlink permission bits are OS metadata), records whether link
targets are relative or absolute plus their resolved sandbox destination, and
tokens the named JSON timestamp fields `installedAt`, `updatedAt`, and
`approvedAt`. Repeated
horizontal layout spacing is normalized only for scenarios that declare that
terminal-layout rule; JSON scenarios cannot opt into it. Exit status, stream roles,
semantic text, lock fields, file bytes, portable permissions/kinds, links, HTTP
requests, and child argv remain observable.

## Recording candidates

Recording is deliberately separate from the test and cannot overwrite this
reviewed directory. Compatibility scenarios execute the integrity-pinned npm
0.1.2 oracle; scenarios carrying approved divergence IDs execute native.
Record to a temporary directory, inspect every change, then copy only accepted
outcomes in a separate reviewed edit:

```sh
OPEN_SKILLS_RECORD_GOLDEN_DIR=/tmp/open-skills-golden-v1 \
  go test ./internal/compatibility -run '^TestRecordGoldenCorpus$' -count=1
```

Do not revise v1 to adopt a different baseline. A semantic baseline change
requires an explicit decision and a new corpus version.
