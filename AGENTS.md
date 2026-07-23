# AGENTS.md

This file provides guidance to coding agents working on the `open-skills` CLI.

## Project overview

`open-skills` is a standalone Go CLI for the open agent skills ecosystem. The active project has no JavaScript or TypeScript implementation and no Node.js, npm, or pnpm development dependency. System Git is the CLI's only runtime dependency.

Native distributions expose only the `open-skills` executable. The retired npm 0.1.2 implementation remains available through the protected `v0.1.2` tag and the reviewed compatibility records; it is not an active implementation.

## Architecture

```text
cmd/open-skills/          Process entry point
internal/application/    Command routing, acquisition, install/update/remove/use flows
internal/state/          Lock schemas, agent registry, paths, and installed-state inspection
internal/trust/          Exact remote-instruction trust store
internal/compatibility/  Process harness, immutable oracle tooling, and golden corpus
internal/release/        Native archive, Homebrew, Scoop, and release verification
compatibility/npm-0.1.2/ Immutable npm 0.1.2 artifact/source manifests
Formula/                 Homebrew package metadata
bucket/                  Scoop package metadata
scripts/                 Native package smoke and signed-tag verification scripts
```

The supported API seam is the built `open-skills` process. Packages under `internal/` are implementation details.

`internal/state/agents.go` is the active authority for agent identifiers, display names, detection, and installation paths. Update the README agent table in the same change and add or update Go tests when the registry changes.

## Development

Use the Go version declared by `go.mod`.

```sh
# Format

gofmt -w cmd internal

# Static checks and complete test suite

go vet ./...
go test ./... -count=1

# Build and smoke-test the standalone executable

CGO_ENABLED=0 go build -trimpath -o build/open-skills ./cmd/open-skills
env PATH= ./build/open-skills

# Run one package or test

go test ./internal/application -run '^TestName$' -count=1
go test ./internal/compatibility -run '^TestNativeGoldenCorpus$' -count=1
```

Run `gofmt` before committing. CI rejects formatting drift, runs vet and the complete Go suite on Linux, runs the frozen golden corpus in a network namespace, and runs the Go suite plus a native build on Windows.

## Compatibility history

The reviewed corpus under `internal/compatibility/testdata/golden/v1` is immutable compatibility history. Normal CI executes only the native binary against its checked-in outcomes and rejects Node, npm, or npx subprocesses.

The opt-in oracle recorder and differential tests under `internal/compatibility` are historical maintenance tools, not normal development or CI requirements. They may materialize the integrity-pinned npm 0.1.2 oracle with an explicitly supplied Node executable. Never point them at a mutable dist-tag or overwrite the reviewed v1 corpus.

Verify the live immutable artifact, source chain, and tag protection with the Go verifier:

```sh
go run ./internal/compatibility/cmd/verify-native-baseline
```

This online historical check requires GitHub authentication through `GITHUB_TOKEN`, `GH_TOKEN`, or `gh auth login`; it does not invoke npm.

## Native packaging and release

Generate the reviewed native archives and package metadata through Go:

```sh
GOTOOLCHAIN=go1.24.0 go run ./internal/release/cmd/native-preview \
  --version 0.2.0 \
  --output native-dist \
  --homebrew-formula Formula/open-skills.rb \
  --scoop-manifest bucket/open-skills.json \
  --skip-linux-smoke
```

Release builds are CGO-disabled archives containing only `open-skills` (`open-skills.exe` on Windows). The signed-tag workflow verifies checksums, keyless signatures, provenance, Homebrew, and Scoop before protected publication. Follow `docs/native-development.md` and `docs/native-production-gate.md`; do not publish or restore an npm workflow.
