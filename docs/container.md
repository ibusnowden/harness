# Container-First Ascaris Workflows

The checked-in [`Containerfile`](../Containerfile) is for running the Go `ascaris` CLI and test suite in a clean container.

## Build The Image

### Docker

```bash
docker build -t ascaris-dev -f Containerfile .
```

### Podman

```bash
podman build -t ascaris-dev -f Containerfile .
```

## Run The Go Test Suite In The Container

### Docker

```bash
docker run --rm -it \
  -v "$PWD":/workspace \
  -e GOCACHE=/tmp/go-build \
  -w /workspace \
  ascaris-dev \
  go test ./...
```

### Podman

```bash
podman run --rm -it \
  -v "$PWD":/workspace:Z \
  -e GOCACHE=/tmp/go-build \
  -w /workspace \
  ascaris-dev \
  go test ./...
```

## Build The CLI In The Container

```bash
docker run --rm -it \
  -v "$PWD":/workspace \
  -e GOCACHE=/tmp/go-build \
  -w /workspace \
  ascaris-dev \
  ./install.sh --release
```

## Open A Shell

### Docker

```bash
docker run --rm -it \
  -v "$PWD":/workspace \
  -e GOCACHE=/tmp/go-build \
  -w /workspace \
  ascaris-dev
```

### Podman

```bash
podman run --rm -it \
  -v "$PWD":/workspace:Z \
  -e GOCACHE=/tmp/go-build \
  -w /workspace \
  ascaris-dev
```

Inside the container:

```bash
go build -o ./bin/ascaris ./cmd/ascaris
go test ./...
./bin/ascaris doctor
./bin/ascaris status
```

## Notes

- Docker and Podman use the same `Containerfile`.
- The `:Z` suffix in the Podman examples is for SELinux relabeling.
- `GOCACHE=/tmp/go-build` keeps build artifacts out of the mounted checkout.
