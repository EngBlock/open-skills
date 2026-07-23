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

Concurrent mutation waits are bounded to 10 seconds by default. Set
`OPEN_SKILLS_LOCK_TIMEOUT_MS` to a non-negative decimal millisecond value when
a development or CI scenario needs a different bound. Invalid or negative
values fail closed before managed state is touched.

`internal/compatibility` contains the process-level differential harness. Each target gets a fresh home, project, temporary directory, local Git repositories, HTTP server, stdin, environment, and PATH command fixtures. The harness records raw process streams and status, filesystem and lock state, HTTP requests, and child-command invocations before applying only documented presentation normalization.

The npm side must be prepared with `PrepareNPMOracle`. It reads `compatibility/npm-0.1.2/oracle.json` plus `runtime-dependencies.json`, verifies the exact CLI and pinned `yaml@2.9.0` tarball bytes, enforces their recorded file counts and unpacked sizes, and safely extracts `package/bin/cli.mjs` with its runtime dependency for an explicitly supplied Node executable. It never resolves a dist-tag or invokes npm/npx.

Scenario processes inherit no credentials, proxies, agent markers, runtime injection variables, or host Git configuration. Their `PATH` is empty unless declared command fixtures are installed, in which case it contains only those fixtures. This fail-closed setup makes subprocess behavior explicit while local repository fixtures are initialized separately with isolated deterministic Git configuration.
