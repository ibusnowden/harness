# Ascaris Roadmap

## Current State

- Go is the active implementation.
- The active CLI surface includes prompt runtime, OAuth, sessions, plugins, MCP, skills, agents, worker state, team state, and cron state.

## Near-Term Priorities

1. Release packaging polish for standalone binaries and future `curl | sh` installation.
2. Broader end-to-end contract coverage for provider fallback paths and multi-tool runtime behavior.
3. Continued CI and release automation hardening around the Go-only runtime.

## Maintenance Rules

- Keep `.ascaris` as the only active config and state namespace.
- Keep migration support explicit and opt-in rather than part of steady-state runtime behavior.
