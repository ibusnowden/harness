# Ascaris
[![CI](https://github.com/ibusnowden/harness/actions/workflows/go-ci.yml/badge.svg)](https://github.com/ibusnowden/harness/actions/workflows/go-ci.yml)
[![version](https://img.shields.io/badge/version-0.1.0-blue)](https://github.com/ibusnowden/harness/releases)

Ascaris is a Go coding harness built for local CLI use, standalone binaries, and a future `curl | sh` install flow.

The active product is the Go CLI under [`cmd/ascaris`](./cmd/ascaris).

## Active Repo Shape

- `cmd/ascaris` — executable entrypoint
- `internal/` — Go packages for CLI, runtime, sessions, tools, plugins, MCP, OAuth, recovery, and state
- `testdata/parity/traceability.json` — traceability fixture for Go audit coverage
- `testdata/contracts` — scenario and contract fixtures for the Go test suite

## Quick Start

```bash
go build -o ./bin/ascaris ./cmd/ascaris
./bin/ascaris doctor
./bin/ascaris status
./bin/ascaris
./bin/ascaris prompt "summarize this repository"
echo "summarize this repository" | ./bin/ascaris
```

`./bin/ascaris` now starts an interactive terminal chat when run from a TTY. Explicit `prompt` and piped stdin continue to work as one-shot prompt modes.

Run the Go test suite:

```bash
mkdir -p .cache/go-build
GOCACHE="$(pwd)/.cache/go-build" go test ./...
```

## Useful Commands

```bash
./bin/ascaris login
./bin/ascaris session list
./bin/ascaris team list
./bin/ascaris cron list
./bin/ascaris worker list
./bin/ascaris tools --query Worker --limit 8
./bin/ascaris parity-audit
```

Ascaris stores config and runtime state under `.ascaris/` by default. Override the config root with `ASCARIS_CONFIG_HOME`.

## Documentation Map

- [`USAGE.md`](./USAGE.md) — build/install and CLI usage
- [`docs/prompt-e2e-stress-test.md`](./docs/prompt-e2e-stress-test.md) — prompt-first physical E2E stress-test runbook
- [`docs/qwen-vllm-smoke-test.md`](./docs/qwen-vllm-smoke-test.md) — local Qwen + vLLM + Ascaris smoke-test runbook
- [`PARITY.md`](./PARITY.md) — current Go traceability and contract notes
- [`ROADMAP.md`](./ROADMAP.md) — current product direction
- [`PHILOSOPHY.md`](./PHILOSOPHY.md) — product intent and operating model
- [`docs/container.md`](./docs/container.md) — container-based Go workflows

## Disclaimer

- This repository does not claim ownership of the original Claude Code source material.
- This repository is not affiliated with, endorsed by, or maintained by Anthropic.
