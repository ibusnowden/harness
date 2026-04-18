# GLM Cluster Deployment

This checklist is the minimum cluster-side deployment path for running Ascaris against a fine-tuned `GLM-4.7` model on an `8xH100` box with vLLM serving an OpenAI-compatible API.

## Goal

Run the full coding harness locally on your own infrastructure:
- fine-tuned `GLM-4.7` weights on the cluster
- vLLM serving `POST /v1/chat/completions`
- Ascaris pointed at that endpoint with `--provider openai`
- no dependency on Anthropic, OpenAI, or OpenRouter for inference

## Architecture

Recommended production layout:
- one GPU node runs vLLM with the fine-tuned weights
- Ascaris runs on the same node or a nearby CPU/login node with low-latency network access
- the code workspace is mounted locally where Ascaris runs
- the harness points to a cluster-local vLLM endpoint

Development layout:
- run Ascaris locally on your workstation
- SSH tunnel to the cluster-hosted vLLM endpoint
- use this only for bring-up and smoke testing

## Prerequisites

Before deployment:
- confirm the fine-tuned weights load correctly in vLLM
- confirm your tokenizer and chat template are the ones intended for the fine-tune
- confirm the model can emit reliable tool calls if you plan to use agent tools heavily
- build Ascaris from source or ship a known-good binary

## vLLM Launch Template

Example launch command:

```bash
vllm serve /path/to/glm47-checkpoint \
  --host 0.0.0.0 \
  --port 8000 \
  --api-key local-dev \
  --served-model-name GLM-4.7 \
  --tensor-parallel-size 8 \
  --max-model-len 32768 \
  --generation-config vllm
```

Tune these for your workload:
- `--tensor-parallel-size 8` for `8xH100`
- `--max-model-len` based on your prompt and repo sizes
- any quantization or memory flags appropriate for your checkpoint

## Harness Configuration

On the machine running Ascaris:

```bash
export OPENAI_API_KEY=local-dev
export OPENAI_BASE_URL=http://127.0.0.1:8000/v1
```

Run Ascaris against the local endpoint:

```bash
./bin/ascaris --provider openai --model GLM-4.7 prompt "Reply with exactly: ok"
```

Important:
- use `--provider openai` explicitly for GLM model names
- do not rely on model-name auto-routing

## Bring-Up Order

Use this order. Do not skip ahead.

1. Start vLLM and confirm the service is listening.
2. Run a raw `curl` chat completion probe.
3. Run a forced tool-call probe with one function and `parallel_tool_calls=false`.
4. Run `scripts/check_tool_call_response.py` on the raw tool-call response.
5. Run `scripts/smoke_glm_ascaris.sh`.
6. Run the interactive approval-path prompt manually.

## Validation Checklist

The deployment is not ready until all of these are true:
- raw chat completions succeed
- raw tool-call responses contain one valid function call with parseable JSON arguments
- Ascaris prompt mode works in text mode
- JSON mode is clean
- `read_file`, `grep_search`, and `write_file` work
- approval-gated `bash` works and remains visually clean in an interactive terminal

## Highest-Risk Area

The biggest technical risk is tool calling, not base prompting.

Typical failure modes:
- the model emits invalid `tool_calls`
- the model emits prose instead of a function call
- the arguments field is not valid JSON
- the model emits multiple calls when the harness expects a single next action
- the chat template is not aligned with tool use

If raw tool calling is unstable at the vLLM layer, fix that before blaming the harness.

## Recommended Ops Layout

For initial bring-up:
- use `tmux`
- keep one pane for vLLM logs
- one pane for `nvidia-smi`
- one pane for Ascaris smoke tests

After the smoke tests are clean:
- move vLLM to a `systemd` service
- keep Ascaris as a user-invoked CLI unless you have a concrete reason to daemonize it

## Operational Notes

- keep model serving and the code workspace close to each other to reduce latency
- do not start with a large tool set; begin with `read_file` and `grep_search`
- keep the model name stable in serving and in Ascaris invocations
- preserve exact command transcripts when debugging tool-call failures

## Files Added For This Workflow

- `scripts/smoke_glm_ascaris.sh`
- `scripts/check_tool_call_response.py`
- `docs/prompt-e2e-stress-test.md`

Use those as the bring-up path before broader cluster automation.
