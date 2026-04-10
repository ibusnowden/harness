# Ascaris Usage

## Build

```bash
go build -o ./bin/ascaris ./cmd/ascaris
```

## Install

```bash
./install.sh
```

For a stripped local release build:

```bash
./install.sh --release
```

## First Commands

```bash
./bin/ascaris doctor
./bin/ascaris status
./bin/ascaris login
./bin/ascaris prompt "summarize this repository"
```

## Sessions And State

- Default config root: `.ascaris/`
- Managed session namespace: `.ascaris/sessions/<workspace-hash>/`
- Worker state: `.ascaris/worker-state.json`
- Team state: `.ascaris/teams.json`
- Cron state: `.ascaris/crons.json`
- Override config root with `ASCARIS_CONFIG_HOME`

Examples:

```bash
ASCARIS_CONFIG_HOME=/tmp/ascaris ./bin/ascaris session list
./bin/ascaris team create reviewers task_1 task_2
./bin/ascaris cron add "@daily" "summarize the repo"
./bin/ascaris worker create .
```

## Tools And Traceability

```bash
./bin/ascaris commands --query session --limit 10
./bin/ascaris tools --query Worker --limit 10
./bin/ascaris parity-audit
./bin/ascaris manifest
```

## JSON Output

```bash
./bin/ascaris status --json
./bin/ascaris doctor --json
./bin/ascaris team list --json
./bin/ascaris worker list --json
```

## Test

```bash
mkdir -p .cache/go-build
GOCACHE="$(pwd)/.cache/go-build" go test ./...
```
