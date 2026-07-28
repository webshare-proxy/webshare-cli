# Contributing

## Setup

Go 1.23+ is the only requirement.

```sh
git clone https://github.com/webshare-proxy/webshare-cli
cd webshare-cli
go build -o webshare .
```

## Layout

```
main.go              entrypoint (signal handling, exit code)
internal/cmd/        one file per command group; root.go has global flags,
                     the client factory and error rendering
internal/output/     tables, TSV, CSV, JSON; terminal/pipe detection, colors
```

The CLI is a thin layer over the
[webshare-go](https://github.com/webshare-proxy/webshare-go) SDK — API
types and requests live there, not here. Bump the SDK with
`go get github.com/webshare-proxy/webshare-go@main && go mod tidy`.

## Running against an environment

```sh
export WEBSHARE_API_KEY=your-key
./webshare whoami                        # production

export WEBSHARE_BASE_URL=https://proxy.dev.webshare.io
./webshare whoami --insecure             # dev (self-signed TLS cert)
```

`--insecure` is a hidden flag that skips TLS verification — test
environments only.

## Checks

CI runs exactly these; run them before pushing:

```sh
gofmt -l .            # must print nothing
go vet ./...
staticcheck ./...     # go install honnef.co/go/tools/cmd/staticcheck@2026.1
go test -race ./...
```

Tests never hit the network: command tests run the CLI in-process against
an `httptest` server (see `internal/cmd/cmd_test.go` for the pattern).

## Command conventions

- Every command supports `--json`; data goes to stdout, status messages to
  stderr.
- Tables render only on a terminal; in a pipe the same data is
  tab-separated with no header. Don't print decoration to stdout.
- Exit codes: `0` success, `1` API/network error, `2` usage error (return
  `usagef(...)` for the latter).
- Destructive commands call `confirm(...)`: interactive prompt on a
  terminal, `--yes` required in scripts.
- Plan-scoped commands take `--plan` via `addPlanFlag` and fall back to the
  account's active plan (`resolvePlanID`).
- New commands: create `internal/cmd/<group>.go`, register it in
  `newRootCmd`, add a routing test.

## Releases

Tag and push — the Release workflow does the rest:

```sh
git tag v0.2.0
git push origin v0.2.0
```

GoReleaser (`.goreleaser.yaml`) builds static binaries for
linux/darwin/windows on amd64/arm64, packages them with `checksums.txt`,
stamps the tag into `webshare --version`, publishes a GitHub release with
a changelog from the commit log, and pushes an updated Homebrew cask to
[webshare-proxy/homebrew-tap](https://github.com/webshare-proxy/homebrew-tap)
(`brew install webshare-proxy/tap/webshare`). Versions follow semver; the
config can be checked locally with `goreleaser check`.

The tap push authenticates with the `HOMEBREW_TAP_GITHUB_TOKEN` repo
secret — a fine-grained PAT with contents read/write on the tap repo only.
If a release fails at the Homebrew step, check that this secret exists and
has not expired; re-run the workflow after fixing it.
