# Go development and native releases

The active project is a Go module with two pinned dependencies: `golang.org/x/text` for Unicode normalization and case folding, and `golang.org/x/sys` for pure-Go Unix and Windows advisory-lock primitives. It has no JavaScript or TypeScript build, test, formatting, packaging, or release toolchain. System Git is the CLI's only runtime dependency. The supported seam is the built `open-skills` process; packages under `internal/` are implementation details and do not promise a public Go interface.

Build and smoke-test the standalone executable:

```sh
CGO_ENABLED=0 go build -trimpath -o build/open-skills ./cmd/open-skills
env PATH= ./build/open-skills
```

Run the active formatting and verification checks with:

```sh
gofmt -w cmd internal
go vet ./...
go test ./... -count=1
```

`internal/repositorypolicy` guards the cutover: active tracked JavaScript/TypeScript and Node package metadata are rejected, active workflows may not set up or invoke Node/npm/pnpm, and the reviewed historical manifests must remain identity-linked.

## Homebrew native releases

The checked-in [`Formula/open-skills.rb`](../Formula/open-skills.rb) is the tap formula for the supported macOS ARM64 target. It installs only the `open-skills` payload from the canonical `darwin_arm64.tar.gz` GitHub Release asset; it does not build from source or download through npm. Because the tap lives in this repository rather than a separately named `homebrew-*` repository, users add its explicit Git URL before installing it.

Before creating a signed native release tag, regenerate the archives and formula with the Go version declared by `go.mod`:

```sh
GOTOOLCHAIN=go1.24.0 go run ./internal/release/cmd/native-preview \
  --version 0.2.0 \
  --output native-dist \
  --homebrew-formula Formula/open-skills.rb \
  --scoop-manifest bucket/open-skills.json \
  --skip-linux-smoke
```

Commit the resulting formula and manifest in the release candidate before tagging. The formula generator selects the exact Darwin ARM64 digest from `checksums.txt` and embeds both the immutable release URL and SHA-256. Release builds disable mutable Go VCS metadata so a clean signed-tag build reproduces the formula digest. Follow the protected production sequence in the [native 0.2.0 production gate](native-production-gate.md): the signed tag workflow validates the candidate before approval, publishes the immutable release, and only then may the production metadata become available from the default branch.

The release workflow rebuilds the artifacts, refuses to proceed unless its generated formula exactly matches the checked-in formula, and stages the verified bundle for `scripts/homebrew-smoke.sh` on macOS ARM64. That pre-publication check installs an older test keg, upgrades it to the exact archive pending publication, verifies the reported version and help output, confirms the keg exposes only `open-skills`, runs the formula test, and performs a clean install. Production publication also waits for protected `native-production` maintainer approval after Homebrew and Scoop checks pass. Maintainers can exercise the same seam locally:

```sh
OPEN_SKILLS_HOMEBREW_ARTIFACT="$PWD/native-dist/open-skills_0.2.0_darwin_arm64.tar.gz" \
  scripts/homebrew-smoke.sh Formula/open-skills.rb
```

## Scoop native releases

The checked-in [`bucket/open-skills.json`](../bucket/open-skills.json) installs only `open-skills.exe` from the canonical checksummed `windows_amd64.zip` GitHub Release asset and never downloads through npm. The Windows x86-64 target remains experimental rather than fully supported. Users add this repository as a Scoop bucket, so the manifest must remain under `bucket/`.

Regenerate it with the same release-generation command shown above by passing `--scoop-manifest bucket/open-skills.json`. The generated manifest embeds the current archive SHA-256 and canonical immutable release URL. Production `checkver` metadata selects non-draft stable releases; prerelease manifests continue to select non-draft previews. `autoupdate` derives the next canonical archive URL and extracts its digest from that release's `checksums.txt`.

Before publication, the release workflow regenerates and compares the checked manifest, then runs `scripts/scoop-smoke.ps1` on Windows x86-64 with a commit-pinned Scoop core. The smoke check validates the manifest against Scoop's schema, exercises its upgrade metadata against the pending checksum, installs the pending ZIP through Scoop's cache, verifies version and help output, and confirms that the package exposes only `open-skills.exe`. The release job cannot run unless this experimental Windows check succeeds.

Concurrent mutation waits are bounded to 10 seconds by default. Set
`OPEN_SKILLS_LOCK_TIMEOUT_MS` to a non-negative decimal millisecond value when
a development or CI scenario needs a different bound. Invalid or negative
values fail closed before managed state is touched. Git acquisition defaults to
five minutes and reads `OPEN_SKILLS_CLONE_TIMEOUT_MS`; internal-skill discovery
uses `OPEN_SKILLS_INSTALL_INTERNAL_SKILLS`. The complete canonical and legacy
environment contract is documented in the [D12 migration notes](native-migration.md#d12-namespaced-configuration-and-exact-authorization).

`internal/compatibility` contains the process-level harness and the frozen historical records. Each target gets a fresh home, project, temporary directory, local Git repositories, HTTP server, stdin, environment, and PATH command fixtures. The harness records raw process streams and status, filesystem and lock state, HTTP requests, and child-command invocations before applying only documented presentation normalization.

Verify the live historical artifact, annotated tag chain, and tag-protection rules without the retired toolchain:

```sh
go run ./internal/compatibility/cmd/verify-native-baseline
```

The verifier uses the exact identities in `compatibility/npm-0.1.2/oracle.json`; it never resolves a dist-tag or invokes npm.

### Optional historical oracle maintenance

`PrepareNPMOracle`, the differential tests, and the corpus recorder are retained only for historical inspection and deliberate corpus maintenance. They are not part of normal development or CI. When explicitly enabled, they read `compatibility/npm-0.1.2/oracle.json` plus `runtime-dependencies.json`, verify the exact retired CLI and pinned `yaml@2.9.0` tarball bytes, enforce recorded file counts and unpacked sizes, and safely extract the historical entrypoint with its runtime dependency for an explicitly supplied Node executable. They never resolve a dist-tag or invoke npm/npx.

Scenario processes inherit no credentials, proxies, agent markers, runtime injection variables, or host Git configuration. Their `PATH` is empty unless declared command fixtures are installed, in which case it contains only those fixtures. This fail-closed setup makes subprocess behavior explicit while local repository fixtures are initialized separately with isolated deterministic Git configuration. Golden Git scenarios may opt into the recording wrapper around the absolute system Git executable; arbitrary host-command passthrough is rejected.

## Reviewed compatibility corpus

[`internal/compatibility/testdata/golden/v1`](../internal/compatibility/testdata/golden/v1) is the reviewed, versioned npm 0.1.2/native-divergence corpus. Its manifest is a closed inventory of commands, aliases, source families, scopes, installation topologies, agent launches, exact non-interactive decisions, lock fixtures, and D01–D13 expectations. Normal CI runs the built native process against every checked-in outcome and fails if the process invokes Node, npm, or npx.

Run the native-only suite with one build:

```sh
mkdir -p build
CGO_ENABLED=0 go build -trimpath -o "$PWD/build/open-skills" ./cmd/open-skills
OPEN_SKILLS_NATIVE_UNDER_TEST="$PWD/build/open-skills" \
  go test ./internal/compatibility -run '^TestNativeGoldenCorpus$' -count=1
```

The opt-in recorder is not an active development or CI path. It uses the integrity-pinned historical oracle only for scenarios that retain npm behavior and native for scenarios carrying named divergence IDs. It requires an output directory outside the reviewed corpus and never overwrites v1; see the corpus README for the deliberate recording and review protocol.
