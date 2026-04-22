# Qwen vLLM Smoke Test

Use this runbook to validate the local `qwen3.6-30b-a3b` serve path and the Ascaris OpenAI-compatible harness path.

## Serve

Start the vLLM server from the repo root:

```bash
cd /project/inniang/harness
sbatch inference/serve.sh qwen3.6-30b-a3b
```

The Qwen preset is configured for vLLM 0.19 with:

- `MAX_MODEL_LEN=262144`
- `TOOL_CALL_PARSER="qwen3_xml"`
- `REASONING_PARSER="qwen3"`

`inference/serve.sh` requests one H100 80GB GPU and 128 GB memory for this
long-context profile. The Slurm job name is `serve-vllm-h100`; helper scripts
only discover that H100 job and ignore older generic `serve-vllm` allocations.

Inspect the serve logs:

```bash
tail -n 50 inference/logs/slurm-serve-<jobid>.out
tail -n 50 inference/logs/slurm-serve-<jobid>.err
```

The `.err` log should include vLLM startup output showing max model length
`262144`. If it reports `16384`, the running job is an old allocation and must
be replaced.

The serve job only starts after Slurm assigns an H100 node. To print the active
direct API URL:

```bash
./inference/endpoint.sh qwen3.6-30b-a3b
```

For a stable harness URL that does not change when Slurm chooses a different node, tunnel the active node to localhost:

```bash
./inference/connect.sh qwen3.6-30b-a3b
```

Keep that tunnel open and use `http://127.0.0.1:8000/v1` as the OpenAI base URL.

## Harness Smoke

Build the CLI:

```bash
mkdir -p .cache/go-build
GOCACHE="$(pwd)/.cache/go-build" go build -o /tmp/ascaris-qwen ./cmd/ascaris
```

Run a plain prompt:

```bash
OPENAI_API_KEY=dummy \
OPENAI_BASE_URL=http://127.0.0.1:8000/v1 \
ASCARIS_CONFIG_HOME=/tmp/ascaris-qwen \
/tmp/ascaris-qwen --provider openai --model qwen3.6-30b-a3b --output-format json prompt 'Reply with exactly ok'
```

Expected result:

- JSON response contains `"provider":"openai"`
- final message is `ok`
- reasoning, when present, is preserved as a `thinking` block

Run a tool-use smoke:

```bash
OPENAI_API_KEY=dummy \
OPENAI_BASE_URL=http://127.0.0.1:8000/v1 \
ASCARIS_CONFIG_HOME=/tmp/ascaris-qwen \
/tmp/ascaris-qwen --provider openai --model qwen3.6-30b-a3b --allowedTools read_file --output-format json prompt 'Use the read_file tool to read README.md and return the first Markdown heading only.'
```

Expected result:

- `tool_uses` contains a `read_file` call
- `tool_results` contains the matching tool output
- final message is non-empty

## Raw SSE Check

If tool-use fails, inspect the raw vLLM stream. Auto tool choice is only healthy when streamed tool calls are populated:

```bash
curl -N http://127.0.0.1:8000/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen3.6-30b-a3b","stream":true,"max_tokens":128,"messages":[{"role":"system","content":"Use tools when needed."},{"role":"user","content":"Use the read_file tool to read README.md and tell me the first Markdown heading only."}],"tools":[{"type":"function","function":{"name":"read_file","description":"Read a file from the current workspace","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}}}],"tool_choice":"auto"}'
```

Healthy output includes:

- reasoning deltas under `delta.reasoning`
- one or more populated `delta.tool_calls` items
- `finish_reason: "tool_calls"` only when actual tool calls were streamed
