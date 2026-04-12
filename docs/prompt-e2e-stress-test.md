# Prompt E2E Stress Test

This runbook is the first physical end-to-end pass for Ascaris. It starts from the known prompt UX issues in [`HANDOFF.md`](../HANDOFF.md) and turns them into a repeatable stress-test checklist for another terminal.

## Goal

Validate the real `./bin/ascaris prompt` flow in an interactive terminal before expanding into the broader CLI surface.

This pass is focused on:
- spinner cleanliness
- approval prompt readability
- final output separation
- JSON output cleanliness
- session persistence after real prompt runs

This pass is not focused on:
- provider matrix coverage
- plugins, MCP, agents, team, cron, worker, or state commands beyond prompt/session follow-up
- matching Claude, Gemini CLI, or any external frontend styling

## Preconditions

Run from the repo root.

Build a fresh binary:

```bash
go build -o ./bin/ascaris ./cmd/ascaris
```

Use a real interactive terminal session, not a buffered log runner.

Before provider-backed testing:
- rotate any credentials that were previously pasted into chat
- export the provider environment variables you intend to use
- confirm you are invoking the freshly built `./bin/ascaris`

Recommended local test cache:

```bash
mkdir -p .cache/go-build
export GOCACHE="$(pwd)/.cache/go-build"
```

## Automated Preflight

Run these before any manual retest:

```bash
go test ./internal/outputstyles ./internal/cli ./internal/runtime
```

These cover the current prompt UX invariants:
- spinner starts only for interactive text mode
- spinner pauses around approvals
- spinner stops before final output and error output
- spinner renders one phrase per task
- live runtime sends the current system prompt

## Environment Setup

Set the provider values you want to use for physical prompt runs:

```bash
export MODEL="<provider-model>"
export ASCARIS_CONFIG_HOME="$(pwd)/.ascaris-e2e"
```

If you are using OpenRouter, also set the relevant API key and base URL in your shell before continuing.

To avoid stale session state from earlier runs:

```bash
rm -rf "$ASCARIS_CONFIG_HOME"
mkdir -p "$ASCARIS_CONFIG_HOME"
```

## Pass Or Fail Criteria

Every prompt run in this document should satisfy all of the following:
- the spinner stays on its own terminal line
- only one spinner verb is visible at a time
- the approval prompt is readable and not interleaved with spinner output
- final assistant output starts on a clean line
- error output starts on a clean line
- JSON mode emits only JSON, with no spinner or approval artifacts mixed in

Log a failure with:
- exact command
- terminal app and shell
- expected behavior
- observed behavior
- whether it is a spinner, approval prompt, final output, JSON contamination, or session issue

## Phase 1: Core Interactive Retest

Start with the two commands from `HANDOFF.md`.

Text-mode basic prompt:

```bash
./bin/ascaris --provider openrouter --model "$MODEL" prompt "Reply with exactly: openrouter ok"
```

Text-mode approval prompt:

```bash
./bin/ascaris --provider openrouter --model "$MODEL" --permission-mode workspace-write prompt "Use the bash tool to run: printf 'bash ok'"
```

Expected behavior:
- spinner appears during thinking
- spinner stops before final assistant text
- approval prompt appears on a clean line
- after approval, spinner can resume, but it must remain visually clean

Repeat each command three times in the same terminal. Repetition matters because line-clearing bugs often appear only after prior prompt output has already dirtied the terminal state.

For the approval command, test both outcomes:
- answer `y`
- rerun and answer `n`

## Phase 2: JSON Cleanliness

Run a JSON-mode prompt immediately after interactive text-mode runs:

```bash
./bin/ascaris --provider openrouter --model "$MODEL" --output-format=json prompt "Reply with exactly: openrouter json ok"
```

Expected behavior:
- stdout is valid JSON only
- no spinner text appears
- no approval or progress text leaks into the JSON payload

## Phase 3: Tool Roundtrip Prompts

Use these prompts to exercise the common tool paths that already have parity coverage in `internal/cli/live_prompt_test.go`.

Create a fixture:

```bash
printf 'alpha parity line\nbeta line\ngamma parity line\n' > fixture.txt
```

Read file:

```bash
./bin/ascaris --provider openrouter --model "$MODEL" --permission-mode workspace-write prompt "Read fixture.txt and quote the first line."
```

Grep flow:

```bash
./bin/ascaris --provider openrouter --model "$MODEL" --permission-mode workspace-write prompt "Count how many times the word parity appears in fixture.txt."
```

Write file:

```bash
./bin/ascaris --provider openrouter --model "$MODEL" --permission-mode workspace-write prompt "Write generated/output.txt containing exactly: created by prompt e2e"
```

Bash approval flow:

```bash
./bin/ascaris --provider openrouter --model "$MODEL" --permission-mode workspace-write prompt "Use the bash tool to run: printf 'bash ok'"
```

Expected behavior:
- tool-triggering prompts keep the terminal clean before and after tool execution
- approval prompts remain readable
- generated files match the request

## Phase 4: Session Follow-Up

After at least one successful real prompt run, verify session behavior:

```bash
./bin/ascaris session list
./bin/ascaris session show latest
./bin/ascaris session export latest
./bin/ascaris session clear
```

Expected behavior:
- the latest session is visible after prompt runs
- `session show latest` points to the most recent managed session
- export succeeds on the session created by the physical prompt pass
- clearing the latest alias does not corrupt prior session data

## Known Good Follow-On Surface

Only continue here after the prompt/session pass is clean.

The next manual sweep from `HANDOFF.md` is:
- `commands`
- `tools`
- `sandbox`
- `agents`
- `skills`
- `team`
- `cron`
- `worker`
- `plugins`
- `mcp`
- `state`
- `security-review`
- `fuzz`
- `crash-triage`

Keep that work as a second pass. Do not mix those results into the first prompt UX report.

## Suggested Report Template

Use one block per defect:

```text
Command:
Terminal:
Expected:
Observed:
Category:
Reproducible:
Notes:
```

If the full prompt/session pass is clean, record that explicitly and include:
- terminal app
- shell
- provider
- model
- whether both approval outcomes were tested
- whether JSON mode remained clean after interactive runs
