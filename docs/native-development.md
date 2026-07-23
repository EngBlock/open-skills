# Native development

The native preview is a Go module with two pinned dependencies: `golang.org/x/text` for Unicode normalization and case folding, and `golang.org/x/sys` for pure-Go Unix and Windows advisory-lock primitives. Its supported seam is the built `open-skills` process; packages under `internal/` are implementation details and do not promise a public Go interface.

Build and smoke-test the standalone executable without a Node runtime:

```sh
CGO_ENABLED=0 go build -trimpath -o build/open-skills ./cmd/open-skills
env PATH= ./build/open-skills
```

Run native checks with:

```sh
gofmt -w cmd internal
go vet ./...
go test ./...
```

## Homebrew preview releases

The checked-in [`Formula/open-skills.rb`](../Formula/open-skills.rb) is the tap formula for the supported macOS ARM64 target. It installs only the `open-skills` payload from the canonical `darwin_arm64.tar.gz` GitHub Release asset; it does not build from source or download through npm. Because the tap lives in this repository rather than a separately named `homebrew-*` repository, users add its explicit Git URL before installing it.

Before creating a signed native preview tag, regenerate the archives and formula with the Go version declared by `go.mod`:

```sh
GOTOOLCHAIN=go1.24.0 go run ./internal/release/cmd/native-preview \
  --version 0.2.0-preview.2 \
  --output native-dist \
  --homebrew-formula Formula/open-skills.rb \
  --skip-linux-smoke
```

Replace the example version with the release version and commit the resulting formula before tagging. The formula generator selects the exact Darwin ARM64 digest from `checksums.txt` and embeds both the immutable release URL and SHA-256. Release builds disable mutable Go VCS metadata so a clean signed-tag build reproduces the formula digest. Push the signed tag, wait for the release workflow to publish the immutable release successfully, and only then push the formula commit to the default branch so the tap never advertises an unavailable archive.

The release workflow rebuilds the artifacts, refuses to proceed unless its generated formula exactly matches the checked-in formula, and stages the verified bundle for `scripts/homebrew-smoke.sh` on macOS ARM64. That pre-publication check installs an older test keg, upgrades it to the exact archive pending publication, verifies the reported version and help output, confirms the keg exposes only `open-skills`, runs the formula test, and performs a clean install. Only after it succeeds does the workflow publish the immutable release. Maintainers can exercise the same seam locally:

```sh
OPEN_SKILLS_HOMEBREW_ARTIFACT="$PWD/native-dist/open-skills_0.2.0-preview.2_darwin_arm64.tar.gz" \
  scripts/homebrew-smoke.sh Formula/open-skills.rb
```

Concurrent mutation waits are bounded to 10 seconds by default. Set
`OPEN_SKILLS_LOCK_TIMEOUT_MS` to a non-negative decimal millisecond value when
a development or CI scenario needs a different bound. Invalid or negative
values fail closed before managed state is touched. Git acquisition defaults to
five minutes and reads `OPEN_SKILLS_CLONE_TIMEOUT_MS`; internal-skill discovery
uses `OPEN_SKILLS_INSTALL_INTERNAL_SKILLS`. The complete canonical and legacy
environment contract is documented in the [D12 migration notes](native-migration.md#d12-namespaced-configuration-and-exact-authorization).

`internal/compatibility` contains the process-level differential harness. Each target gets a fresh home, project, temporary directory, local Git repositories, HTTP server, stdin, environment, and PATH command fixtures. The harness records raw process streams and status, filesystem and lock state, HTTP requests, and child-command invocations before applying only documented presentation normalization.

The npm side must be prepared with `PrepareNPMOracle`. It reads `compatibility/npm-0.1.2/oracle.json` plus `runtime-dependencies.json`, verifies the exact CLI and pinned `yaml@2.9.0` tarball bytes, enforces their recorded file counts and unpacked sizes, and safely extracts `package/bin/cli.mjs` with its runtime dependency for an explicitly supplied Node executable. It never resolves a dist-tag or invokes npm/npx.

Scenario processes inherit no credentials, proxies, agent markers, runtime injection variables, or host Git configuration. Their `PATH` is empty unless declared command fixtures are installed, in which case it contains only those fixtures. This fail-closed setup makes subprocess behavior explicit while local repository fixtures are initialized separately with isolated deterministic Git configuration.
