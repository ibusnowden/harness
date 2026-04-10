# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Detected stack
- Languages: Go.
- Frameworks: none detected from the supported starter markers.

## Verification
- Run Go verification from the repo root: `mkdir -p .cache/go-build && GOCACHE="$(pwd)/.cache/go-build" go test ./...`
- Use `./install.sh` for a local build plus smoke checks.

## Repository shape
- `cmd/ascaris` contains the executable entrypoint.
- `internal/` contains the active Go implementation.

## Working agreement
- Prefer small, reviewable changes and keep generated bootstrap files aligned with actual repo workflows.
- Keep runtime/config examples aligned with `.ascaris`, `.ascaris.json`, and `ASCARIS_CONFIG_HOME`.
- Do not overwrite existing `CLAUDE.md` content automatically; update it intentionally when repo workflows change.
