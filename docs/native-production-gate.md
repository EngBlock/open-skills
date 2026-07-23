# Native 0.2.0 production gate

Native `open-skills` 0.2.0 was published through the protected release path below. This checklist remains the historical release contract; the final evidence and maintainer approval are recorded on [issue #41](https://github.com/EngBlock/open-skills/issues/41). Active development and release automation are now Go-only following the completed cutover in [issue #43](https://github.com/EngBlock/open-skills/issues/43).

## Technical gate

- **Reviewed compatibility corpus:** `TestNativeGoldenCorpus` must pass against every reviewed v1 outcome. D01-D13 remain explicit in `internal/compatibility/testdata/golden/v1/manifest.json` and `docs/native-migration.md`; no security divergence may be normalized away.
- **Security and state safety:** `go test ./... -count=1` must pass the provenance, modification detection, transaction recovery, concurrency, trust, transport, state-safety, JSON, and exact-authorization suites.
- **macOS ARM64 maintainer validation:** the supported Homebrew install, upgrade, formula test, executable-name, version, and help checks must pass on an ARM64 macOS host using `scripts/homebrew-smoke.sh`.
- **Linux x86-64 built-binary smoke:** canonical packaging must execute the CGO-disabled Linux x86-64 binary and verify its release version in CI.
- **Experimental artifacts:** macOS x86-64, Linux ARM64, and Windows x86-64 must build successfully. Release notes and package metadata must continue to call these targets experimental; the Windows artifact must also pass `scripts/scoop-smoke.ps1`.
- **Signed canonical artifacts:** every canonical archive and `checksums.txt` must have a verified keyless signature, every archive must have GitHub build provenance, and all checksums and workflow identities must be verified before publication.
- **Homebrew availability:** `Formula/open-skills.rb` must match the generated macOS ARM64 artifact exactly and the pending production archive must pass the Homebrew smoke before approval. The public tap install is checked after publication before #41 closes.
- **Scoop availability:** `bucket/open-skills.json` must match the generated experimental Windows x86-64 artifact exactly and the pending production archive must pass the Scoop smoke before approval. The public bucket install is checked after publication before #41 closes.
- **Migration guidance:** `docs/native-migration.md` describes every intentional divergence, existing-state recognition, forward-only native metadata, legacy environment behavior, authorization boundaries, and platform limitations. Issues #42 and #43 subsequently retired npm distribution and removed the JavaScript/TypeScript toolchain from active development without changing this release evidence.

## Candidate evidence

On 2026-07-23, the issue #41 candidate passed the complete Go suite, frozen corpus, and then-current differential compatibility checks. A maintainer-controlled macOS ARM64 host ran the pending `0.2.0` archive through `scripts/homebrew-smoke.sh`, including install, upgrade, formula test, canonical executable, version, help, and clean-install checks. An exact Go 1.24.0 Linux x86-64 container built every supported and experimental target and executed the required Linux built-binary smoke; its checksums reproduced the macOS-generated checked metadata. The signed-tag workflow reran these checks and the Windows Scoop smoke from the immutable candidate before approval.

## Human approval and publication

1. Commit the generated production Homebrew formula and Scoop manifest with the complete candidate. Keep that metadata off the default branch until its canonical archives exist; the signed-tag workflow reruns the complete candidate checks before approval.
2. Protect exact tag `v0.2.0` against updates and deletion with no bypass actor.
3. A maintainer creates and pushes the signed `v0.2.0` tag. The tag workflow rebuilds from that immutable commit and reruns the technical gate.
4. The `production-approval` job starts only after the build, macOS ARM64 Homebrew, and experimental Windows Scoop jobs pass. Publication then waits at the protected `native-production` GitHub environment for explicit maintainer approval.
5. After approval, the workflow publishes one non-prerelease GitHub Release. It refuses to replace an existing release or retarget the signed tag.
6. Verify the public release, signatures, provenance, Homebrew availability, and Scoop availability. Record the exact commands, outcomes, workflow URL, release URL, approver, and platform evidence on issue #41 before closing it.

Creating the signed production tag and approving `native-production` are separate deliberate maintainer actions. Preview tags never enter the protected production approval environment.
