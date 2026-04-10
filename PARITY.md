# Ascaris Parity Status

## Summary

- The active runtime is the Go `ascaris` CLI.
- Go-side traceability and contract checks are fixture-backed through `testdata/parity/traceability.json` and `testdata/contracts/`.

## Live Go Surface

- Multi-provider prompt runtime with managed JSONL sessions
- Loopback OAuth login and persisted credentials
- Built-in file, grep, glob, bash, worker, team, and cron tools
- Plugin, MCP, skills, agents, hooks, worker recovery, and stale-branch policy wiring
- Public CLI and slash-command support for session, team, cron, worker, plugins, MCP, agents, skills, login, logout, doctor, status, and prompt flows

## Audit And Fixtures

- `./bin/ascaris parity-audit` checks mapped root files and directories against the active Go tree and reports live registry counts.
- `testdata/contracts/mock_parity_scenarios.json` keeps the scripted scenario manifest used by the Go-side harness tests.
