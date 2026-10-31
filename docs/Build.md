# Building and testing

## Layout

| Path | Content |
|---|---|
| `cmd/imageadvisor` | `main` |
| `internal/app` | flags, HTTP endpoints, signals, main loop |
| `internal/core` | checks and metrics |
| `internal/config` | config file |
| `internal/docker` | read-only Docker API client |
| `internal/metrics` | Prometheus text format |
| `tests/integration` | integration test |

Unit tests sit next to the code (`*_test.go`). The only dependency is `go.yaml.in/yaml/v3`.

## Build

Needs the Go version in `go.mod`.

```bash
go build ./cmd/imageadvisor
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/imageadvisor
docker build -t imageadvisor:dev .
```

The image is `FROM scratch` with one static binary. `--build-arg VERSION=... REVISION=... BUILD_TIME=...`
set what `imageadvisor_build_info` reports.

## Unit tests

```bash
gofmt -l .
go vet ./...
go test -race ./...
```

## Integration test

`tests/integration/run.sh IMAGE COVERDIR` runs the image against the host's Docker daemon and checks the
endpoints, the metrics and a failed reload. It needs root and Docker and starts a container, so run it
in CI or a throw-away VM:

```bash
docker build --build-arg COVER=1 -t imageadvisor:test .
sudo tests/integration/run.sh imageadvisor:test "$PWD/cover-integration"
```

## Coverage

CI merges the unit and the integration coverage:

```bash
go test -covermode=atomic -coverpkg=./... -cover ./... -args -test.gocoverdir="$PWD/cover-unit"
go tool covdata percent -i=cover-unit,cover-integration
go tool covdata textfmt -i=cover-unit,cover-integration -o cover.txt
go tool cover -html=cover.txt -o coverage.html
```

The image built with `COVER=1` writes its counters to `GOCOVERDIR` on exit.

## CI

`.github/workflows/build.yml` runs unit tests, the integration test on `amd64` and `arm64` runners and
the coverage report on every PR; on `master` it also releases. See [RELEASING.md](../RELEASING.md).
