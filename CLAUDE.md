# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Detected stack
- Languages: Go (harness), Bash (inference scripts), C/CUDA (mmllm training).
- Go version: 1.25. No frameworks — pure stdlib + Charm bracelet TUI ecosystem.

## Verification
```bash
mkdir -p .cache/go-build && GOCACHE="$(pwd)/.cache/go-build" go test ./...
```
Single package: `GOCACHE=$(pwd)/.cache/go-build go test ./internal/<pkg>`
Local build + smoke checks: `./install.sh` (add `--release` for optimized binary, `--no-verify` to skip smoke tests).

## Repository shape
- `cmd/ascaris/` — executable entrypoint.
- `internal/` — all Go implementation (~50 packages): `cli`, `repl`, `runtime`, `agents`, `api`, `config`, `mcp`, `plugins`, `hooks`, `pools`, `oauth`, `security`.
- `inference/` — SLURM + vLLM serving infrastructure (see below).
- `mmllm/` — pure C/CUDA/NCCL training codebase targeting multi-node H100s (45 MFU with FSDP on 4×8×H100).
- `.ascaris/` — runtime config/state root (override with `ASCARIS_CONFIG_HOME`).

## Running ascaris
```bash
export OPENAI_API_KEY=dummy
TERM=screen-256color ./bin/ascaris          # interactive TUI (TTY required)
./bin/ascaris prompt "query"               # one-shot
./bin/ascaris doctor                       # health check
./bin/ascaris status --json
```
Active model/endpoint lives in `.ascaris/settings.local.json`. Copy from inference:
```bash
cp inference/.ascaris/settings.json .ascaris/settings.local.json
```

## Inference (vLLM on SLURM)

**Launch Qwen model server (1 H100, 262k context):**
```bash
sbatch inference/serve.sh qwen3.6-30b-a3b
```
Logs: `inference/logs/slurm-serve-<jobid>.{out,err}`

The Qwen preset targets long-context H100 serving with `MAX_MODEL_LEN=262144`.
`serve.sh` requests `gpu:h100_80gb:1` and `128G` memory by default. Use a
separate lower-context preset if serving on RTX-class nodes.

**Scale to 4 H100s** — edit `inference/models/qwen3.6-30b-a3b.sh`:
```bash
TENSOR_PARALLEL=4
```
Then update the SLURM header in `serve.sh`:
```
#SBATCH --gres=gpu:h100_80gb:4
#SBATCH --mem=128G
```
With `TENSOR_PARALLEL=4` each H100 loads ~9 GB instead of ~35 GB; startup drops from 5–15 min to ~2–3 min.

**Connection refused (`http://<node>:8000`):**
- Check job is running: `squeue -u $USER`
- Print the active direct endpoint:
  ```bash
  ./inference/endpoint.sh qwen3.6-30b-a3b
  ```
- For a stable endpoint that works no matter which node Slurm chose, open an SSH tunnel:
  ```bash
  ./inference/connect.sh qwen3.6-30b-a3b   # keeps tunnel open until Ctrl-C
  ```
  Then set `openaiBaseURL` to `http://127.0.0.1:8000/v1` in settings.
- Cold-start takes 5–15 min on first launch (JIT + CUDA graph capture). Watch `.err` log.

**Add a new model** — create `inference/models/<alias>.sh` exporting:
`MODEL_ID`, `SERVED_NAME`, `PORT`, `TENSOR_PARALLEL`, `MAX_MODEL_LEN`, `TOOL_CALL_PARSER`, `REASONING_PARSER`, `EXTRA_ARGS`.

**Docker Compose alternative** (single node):
```bash
docker compose --profile qwen up   # Qwen on :8000
docker compose --profile glm up    # GLM on :8001
```

## Working agreement
- Prefer small, reviewable changes and keep generated bootstrap files aligned with actual repo workflows.
- Keep runtime/config examples aligned with `.ascaris`, `.ascaris.json`, and `ASCARIS_CONFIG_HOME`.
- Do not overwrite existing `CLAUDE.md` content automatically; update it intentionally when repo workflows change.
- Audit traceability contracts: `./bin/ascaris parity-audit` (fixture source: `testdata/parity/traceability.json`).
