# Building and testing

## Layout

| Path | Content |
|---|---|
| `cmd/imageadvisor` | `main` |
| `internal/app` | flags, HTTP endpoints, signals, main loop |
| `internal/core` | reconcile, registry checks, comparison, metrics |
| `internal/config` | config file |
| `internal/docker` | read-only Docker API client (Unix socket, Windows named pipe) |
| `internal/metrics` | Prometheus text format |
| `internal/ref` | image reference parsing and normalization |
| `internal/state` | `state.json` |
| `tests/integration` | integration tests |

Unit tests sit next to the code (`*_test.go`). The only dependency is `go.yaml.in/yaml/v3`.

## Build

Needs the Go version in `go.mod`.

```bash
go build ./cmd/imageadvisor
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/imageadvisor
docker build -t imageadvisor:dev .
```

The Linux image is `FROM scratch` with one static binary. `--build-arg VERSION=... REVISION=...
BUILD_TIME=...` set what `imageadvisor_build_info` reports.

The Windows image only copies a binary built before onto nanoserver (no `RUN`), so it builds on Windows
and with buildx on Linux alike:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o dist/imageadvisor.exe ./cmd/imageadvisor
docker build -f Dockerfile.windows --build-arg OSVERSION=ltsc2022 -t imageadvisor:dev .
```

## Unit tests

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

CI runs them on Linux and on Windows (without `-race`), so the Windows code is covered too.

## Integration tests

`tests/integration/run.sh IMAGE COVERDIR STORE` starts a `docker:dind` engine with the `classic` or the
`containerd` image store and runs everything inside it: a local `registry:2`, a fake registry answering
429, test images copied from Docker Hub's `busybox`, a Swarm stack, and the image under test. It checks
the comparison end to end (newer image pushed, pulled, container recreated), pinned and local
containers, excludes, a registry mirror, Swarm task updates, the rate limit backoff, a restart with the
state, the endpoints and a failed reload. It needs Docker with `--privileged` and access to Docker Hub:

```bash
docker build --build-arg COVER=1 -t imageadvisor:test .
tests/integration/run.sh imageadvisor:test "$PWD/cover-integration" classic
tests/integration/run.sh imageadvisor:test "$PWD/cover-integration" containerd
```

`tests/integration/run-windows.ps1 IMAGE COVERDIR LTSC` runs the Windows image on a Windows Server host
with the pipe mounted and checks that a `mcr.microsoft.com/windows/nanoserver` container is reported as
`checked`.

## Coverage

CI merges the unit and the integration coverage of Linux and Windows:

```bash
go test -covermode=atomic -coverpkg=./... -cover ./... -args -test.gocoverdir="$PWD/cover-unit"
go tool covdata percent -i=cover-unit,cover-integration
go tool covdata textfmt -i=cover-unit,cover-integration -o cover.txt
go tool cover -html=cover.txt -o coverage.html
```

The image built with `COVER=1` (Linux) or from a binary built with `go build -cover` (Windows) writes
its counters to `GOCOVERDIR` on exit. Below 99% the `Coverage` check fails.

## CI

`.github/workflows/build.yml` runs on every PR: unit tests on Linux and Windows, the integration test
on `amd64` and `arm64` runners with both image stores, the Windows integration test on `windows-2022`
and `windows-2025`, and the coverage report; on `master` it also releases. See
[RELEASING.md](../RELEASING.md).
