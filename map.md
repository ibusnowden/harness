# AI Stack Map

A bottom-up engineering reference for the full stack: GPU silicon → model weights → vLLM serving → inference deployment → agentic harness.

---

## Table of Contents

1. [Layer 0 — Silicon: The GPU](#layer-0--silicon-the-gpu)
2. [Layer 1 — Model: Qwen/Qwen3.6-35B-A3B](#layer-1--model-qwenqwen36-35b-a3b)
3. [Layer 2 — Serving: vLLM](#layer-2--serving-vllm)
4. [Layer 3 — Deployment: `inference/`](#layer-3--deployment-inference)
5. [Layer 4 — Harness: ascaris](#layer-4--harness-ascaris)
6. [End-to-End Data Flow](#end-to-end-data-flow)

---

## Layer 0 — Silicon: The GPU

### Compute Model

An NVIDIA GPU (H100 in this deployment) is organized into **Streaming Multiprocessors (SMs)**. The H100 SXM5 has 132 SMs; each SM contains:

- **CUDA cores** — scalar FP32/INT32 ALUs; one warp (32 threads) executes together
- **Tensor Cores** — 4th-gen matrix-multiply units supporting TF32, BF16, FP16, FP8, INT8; deliver the bulk of throughput for matrix operations (GEMM)
- **Registers** — 256 KB per SM; fastest storage, private per thread
- **L1/shared memory** — 228 KB configurable per SM; on-chip, shared within a thread block

The kernel launch hierarchy is: **grid → thread blocks → warps → threads**. All threads in a warp execute the same instruction simultaneously (SIMT). Divergent branches serialize within a warp.

### Memory Hierarchy

```
Registers      (256 KB/SM)     ~1 cycle
L1/shared mem  (228 KB/SM)     ~30 cycles
L2 cache       (50 MB)         ~200 cycles
HBM3           (80 GB)         ~400 cycles   ← 3.35 TB/s bandwidth (H100 SXM5)
```

**HBM3 (High Bandwidth Memory)** is the critical bottleneck for inference: the decode phase reads model weights and KV cache from HBM on every token generation step. Bandwidth — not compute — sets the throughput ceiling.

### Key CUDA Libraries

| Library | Role |
|---------|------|
| **cuBLAS** | Batched GEMM; used for all weight-matrix multiplications in attention and MLP layers |
| **cuDNN** | Fused attention primitives; convolution kernels; LayerNorm; GELU |
| **NCCL** | Collective communications (all-reduce, all-gather, broadcast) across GPUs for tensor parallelism |
| **Thrust** | Device-side sorting and reduction (used in top-k sampling) |

### Interconnects

- **NVLink 4.0** — 900 GB/s bidirectional between GPUs in same node; required for tensor-parallel inference without bandwidth bottleneck
- **PCIe 5.0** — 128 GB/s (64 GB/s each direction) CPU↔GPU; also used between GPUs without NVLink
- **NVSwitch** (DGX H100) — flat all-to-all NVLink across all 8 GPUs at full speed

### FP8 on H100

H100 Tensor Cores natively accelerate FP8 (E4M3 and E5M2 formats), delivering **~2× the throughput** of BF16 GEMM at the same power. This is why the Qwen model is served with `--quantization fp8`: weights are stored and multiplied in FP8, then accumulated in FP32/BF16 to preserve range.

---

## Layer 1 — Model: Qwen/Qwen3.6-35B-A3B

**Model ID**: `Qwen/Qwen3.6-35B-A3B`  
**Total parameters**: ~35B  
**Active parameters per token**: ~3B (A3B = 3B active)  
**Architecture**: Transformer decoder, Mixture of Experts (MoE)  
**Weights on disk**: ~35 GB (FP8) / ~70 GB (BF16)  
**HF cache location**: `/project/inniang/hf-cache`

### 1.1 Mixture of Experts (MoE)

Standard transformers apply every weight to every token. MoE replaces the feed-forward network (FFN) in each transformer layer with a bank of experts and a gating router:

```
Input token representation
        ↓
  [Gate Network]  ← learnable linear projection
        ↓
  top-k selection (k=8 out of 128 experts)
        ↓
  [Expert 0] [Expert 1] ... [Expert 7]   ← only 8 run
        ↓
  weighted sum of expert outputs
        ↓
  Output token representation
```

- **128 experts** per MoE layer; each expert is a small FFN
- **Top-8 routing**: the gate produces a softmax probability over all 128 experts; the 8 highest are activated
- **Compute cost**: only 8/128 = 6.25% of FFN parameters are used per token → 35B total but 3B active
- **Load balancing loss**: an auxiliary loss (scaled by hyperparameter α) is added during training to prevent all tokens routing to the same few experts (expert collapse)

### 1.2 Grouped Query Attention (GQA)

Standard Multi-Head Attention (MHA) creates one Key and one Value tensor per query head. For a model with 32 query heads that means 32 KV pairs — a large KV cache footprint.

**Grouped Query Attention** shares KV heads across groups of query heads:

```
MHA:  Q0  Q1  Q2  Q3  ...  Q31
      K0  K1  K2  K3  ...  K31   (32 KV pairs)

GQA:  Q0  Q1  Q2  Q3  Q4  Q5  Q6  Q7 | Q8 ... Q15 | Q16...Q23 | Q24...Q31
      K0                              | K1          | K2        | K3
                                      (4 KV pairs)
```

- **Query heads**: 32
- **KV heads**: 4
- **Group size**: 8 (8 query heads share 1 KV head)
- **Memory savings**: 8× reduction in KV cache vs MHA — critical for long-context serving

### 1.3 Rotary Position Embeddings (RoPE)

RoPE encodes position information by rotating the query and key vectors in complex space before computing dot-product attention. Unlike learned absolute position embeddings, RoPE:

- Applies a rotation matrix to each (Q, K) pair proportional to token position
- Encodes *relative* position through the interaction of Q and K rotations
- Generalizes better to sequence lengths beyond training length

**YaRN (Yet Another RoPE extensioN)** extends context further by scaling the rotation frequencies at inference time. The model natively supports 131K context; YaRN extends this to 262K.

### 1.4 Tokenizer

- **Algorithm**: Byte-Pair Encoding (BPE) on raw UTF-8 bytes via **tiktoken**
- **Vocabulary**: ~150K tokens
- **Special tokens**:
  - `<|endoftext|>` — sequence terminator
  - `<|im_start|>` / `<|im_end|>` — instruction/message boundaries (ChatML format)
  - `<think>` / `</think>` — thinking mode reasoning delimiters
- BPE on bytes (not Unicode codepoints) ensures every possible input is representable; avoids unknown token issues for rare scripts

### 1.5 Training Pipeline

```
Stage 1 — General pre-training
  ├── Massive multilingual corpus (web, books, code, math)
  ├── Objective: next-token prediction
  └── Learns: language, world knowledge, basic reasoning

Stage 2 — Specialized reasoning
  ├── High-quality STEM (math, physics, chemistry)
  ├── Programming code across many languages
  └── Learns: step-by-step reasoning, code generation

Stage 3 — Long-context adaptation
  ├── Training sequences up to 32,768 tokens
  ├── RoPE frequency adjustment for extended context
  └── Learns: document comprehension, long-form synthesis

Post-training — Alignment
  ├── SFT (Supervised Fine-Tuning) on curated instruction data
  └── DPO (Direct Preference Optimization)
       ├── Offline: learns from static preference pairs
       ├── Online: reward model generates pairs dynamically
       └── Replaces RLHF; no separate reward model needed
```

### 1.6 Thinking Mode

The model supports internal chain-of-thought reasoning via a distinct token region:

```
User: "What is 17 × 23?"

Model output stream:
<think>
  17 × 23 = 17 × 20 + 17 × 3 = 340 + 51 = 391
</think>
17 × 23 = 391.
```

- Reasoning tokens within `<think>…</think>` are consumed by the model but separated from the final answer
- Up to **8,192 reasoning tokens** budget
- Controlled via chat template flag `enable_thinking` (true/false)
- vLLM's reasoning parser extracts these into a separate `reasoning_content` field in the API response

### 1.7 CUDA Kernels

**FlashAttention-2 / FlashAttention-3**

Standard attention materializes the full N×N attention matrix in HBM — for a 4K-token sequence that's 16M float32 values per head, a huge memory bandwidth sink. FlashAttention avoids this:

```
Tiled attention algorithm:
  For each tile of Q:
    Load tile of Q from HBM → SRAM
    For each tile of K, V:
      Load tiles from HBM → SRAM
      Compute local QK^T scores in SRAM
      Apply online softmax (numerically stable, no full N×N needed)
      Accumulate output in SRAM
    Write output tile to HBM
```

- **Never materializes the full N×N matrix** → memory complexity O(N) instead of O(N²)
- **FlashAttention-3** fuses QKV projection + softmax + dropout + output projection into a single kernel: 1.5–2× faster than FA-2; FP8 support with 2.6× lower numerical error

**Fused RoPE**

Applying RoPE as a separate kernel requires reading Q and K from HBM, rotating, writing back — pure memory bandwidth waste. The fused RoPE kernel applies the rotation during the GEMM that computes Q and K projections: 1.6–3.7× higher bandwidth utilization.

**Fused MoE Dispatch**

Expert selection (gate softmax + top-k) and routing of tokens to expert FFNs is fused into a single kernel, avoiding intermediate materialization of routing indices in HBM.

### 1.8 Weight Storage (safetensors)

Model weights are stored as **safetensors** — a flat binary format with JSON metadata header. Unlike Python pickle, safetensors is:
- Type-safe (explicit dtype + shape in header)
- Memory-mappable (zero-copy from disk to GPU with `mmap`)
- Partially loadable (load individual tensors by offset)

For tensor parallelism, weights are sharded across files:
- **Column-wise split**: Q, K, V projections and first MLP layer — each GPU gets a column slice of the weight matrix
- **Row-wise split**: attention output projection and second MLP layer — each GPU gets a row slice

---

## Layer 2 — Serving: vLLM

vLLM is a high-throughput LLM inference engine that wraps PyTorch + CUDA kernels with a scheduler, memory manager, and OpenAI-compatible HTTP server.

### 2.1 Prefill vs. Decode

Every generation request passes through two distinct computational phases:

**Prefill**
- The entire prompt (N tokens) is processed in a single parallel forward pass
- All N tokens attend to each other simultaneously via the full attention mask
- Outputs: hidden states for all N positions, fully populated KV cache entries
- **Compute-bound**: high arithmetic intensity (large GEMM); tensor cores are the bottleneck
- Latency grows with prompt length: O(N²) for attention computation

**Decode**
- One new token is generated per forward pass
- The new token attends to all previous tokens via their cached K/V vectors
- Outputs: logits for the next token; one new KV entry appended to cache
- **Memory-bandwidth-bound**: low arithmetic intensity; the bottleneck is reading model weights and KV cache from HBM on every step
- Time-per-output-token (TPOT) is roughly: `(model_weight_bytes + kv_cache_bytes) / HBM_bandwidth`

**Practical implication**: prefill is fast per-token but uses GPU compute; decode is slow per-token (limited by HBM bandwidth) and runs for every token generated. Scheduling must balance both.

### 2.2 PagedAttention: Virtual Memory for KV Cache

Traditional serving allocates a contiguous memory block per request sized for the maximum possible sequence length. If a request that could use 4096 tokens finishes at 200, the remaining 3896 token slots sit empty — internal fragmentation of 95%.

**PagedAttention** applies OS virtual memory paging to the KV cache:

```
Physical KV cache memory pool
┌──────────────────────────────────────────────────────┐
│ Block 0  │ Block 1  │ Block 2  │ Block 3  │ Block 4  │
│ 16 toks  │ 16 toks  │ 16 toks  │ 16 toks  │ 16 toks  │
└──────────────────────────────────────────────────────┘
     ↑             ↑         ↑
     Req A         Req B     Req A      ← non-contiguous allocation

Block table for Req A: [Block 0, Block 2, Block 4]
Block table for Req B: [Block 1]
```

- Fixed-size **blocks** (16 tokens/block by default)
- Each request has a **block table** mapping logical sequence positions to physical blocks
- **Fragmentation**: at most `block_size - 1` wasted tokens per request → <4% waste in practice vs 60-80% traditional
- **Prefix sharing**: requests with the same prompt prefix share physical blocks (copy-on-write when sequences diverge)
- The attention kernel loops over block tables instead of indexing a contiguous tensor

### 2.3 Continuous Batching

Static batching (wait for a full batch, run, discard) leaves the GPU idle while fast requests finish. Continuous batching runs a tight scheduler loop:

```
Loop forever:
  1. SCHEDULE
     - Select queued requests to prefill (compute-bound; fills tensor cores)
     - Select in-progress requests to decode (bandwidth-bound; fills HBM bandwidth)
     - Optionally: chunked prefill — split long prompts, interleave with decode

  2. EXECUTE
     - Single forward pass on the mixed batch
     - Prefill tokens contribute to full attention; decode tokens read KV cache

  3. POSTPROCESS
     - Sample next tokens (top-p, top-k, temperature)
     - Detokenize, check stop conditions
     - Free blocks of finished requests; admit new requests
```

**Chunked prefill** mode: instead of doing all prefill before any decode, long prompts are chunked (e.g., 512 tokens at a time) and interleaved with decode steps. This keeps both compute (tensor cores) and memory bandwidth (HBM) saturated simultaneously.

### 2.4 Tensor Parallelism

For a model too large for one GPU, tensor parallelism splits weight matrices across devices:

```
GPU 0                    GPU 1
Q_weight[:, :half]       Q_weight[:, half:]
K_weight[:, :half]       K_weight[:, half:]
V_weight[:, :half]       V_weight[:, half:]
     ↓                         ↓
partial attention         partial attention
     └──────── all-reduce ──────┘
          full attention result
```

- **Column-wise split** (Q, K, V, first MLP): each GPU computes a slice of the output dimension → requires all-reduce to combine
- **Row-wise split** (output proj, second MLP): each GPU holds a row slice → no sync needed post-multiply; all-reduce before next layer

In this deployment: `--tensor-parallel-size 1` (single GPU). The FP8 quantization compensates — it halves the weight memory, allowing a 35B model to fit in one H100 with room for KV cache.

### 2.5 FP8 Quantization

The model is served with `--quantization fp8`:

```
BF16 weights: 35B params × 2 bytes = 70 GB
FP8 weights:  35B params × 1 byte  = 35 GB
```

- **FP8 format**: 8-bit floating point (E4M3: 4 exponent bits, 3 mantissa bits)
- H100 Tensor Cores have **native FP8 GEMM support** — no speed penalty vs. BF16 GEMM; effectively free compression
- Activations remain in BF16/FP32; only weights are quantized
- Accuracy impact: negligible for most tasks; vLLM applies per-tensor scaling factors to compensate for FP8's limited dynamic range

**Other quantization schemes** (not active here but available):
- **AWQ** (Activation-aware Weight Quantization): 4-bit weights; activation-guided scaling protects important weights; Marlin kernel gives 10.9× speedup over naive INT4
- **GPTQ**: Hessian-based layer-wise quantization; 4-bit; Marlin kernel gives 2.6× speedup
- **QAT** (Quantization-Aware Training): quantization baked into training; highest quality; not post-hoc

### 2.6 Tool Call Parsing — Qwen3 XML Protocol

When the model wants to call a function, it emits XML-tagged JSON in its response stream:

```
<tool_call>
{"name": "read_file", "arguments": {"path": "internal/api/types.go"}}
</tool_call>
```

vLLM's Qwen3 XML parser (`--tool-call-parser qwen3_xml`) intercepts this in the response stream and converts it to the OpenAI tool_calls format before returning it to the caller:

```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": null,
      "tool_calls": [{
        "id": "call_abc123",
        "type": "function",
        "function": {
          "name": "read_file",
          "arguments": "{\"path\": \"internal/api/types.go\"}"
        }
      }]
    },
    "finish_reason": "tool_calls"
  }]
}
```

The harness then parses this OpenAI format to dispatch to the actual tool implementation.

### 2.7 Reasoning Parser — Qwen3 Format

The `--reasoning-parser qwen3` flag configures vLLM to split the model's output at `<think>…</think>` boundaries:

```
Raw model output:
"<think>Step 1: read the file...\nStep 2: check types...</think>
The read_file tool takes a path argument."

Parsed API response delta:
{
  "delta": {
    "reasoning_content": "Step 1: read the file...\nStep 2: check types...",
    "content": "The read_file tool takes a path argument."
  }
}
```

- **Streaming-safe**: the parser maintains state across SSE chunks, tracking whether it is inside or outside a `<think>` block
- **Token-ID-based tracking**: uses token IDs rather than string matching for performance
- The harness currently surfaces only `content` to the user; `reasoning_content` is present in the raw API response

### 2.8 OpenAI-Compatible API

vLLM exposes the same API surface as OpenAI:

```
Base URL: http://localhost:8000/v1

GET  /v1/models               → list available models
POST /v1/chat/completions     → generate (main endpoint)
POST /v1/completions          → raw completion (legacy)
```

**Chat completions request** (what the harness sends):
```json
{
  "model": "qwen3.6-30b-a3b",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."},
    {"role": "assistant", "content": "...", "tool_calls": [...]},
    {"role": "tool", "tool_call_id": "...", "content": "..."}
  ],
  "tools": [{"type": "function", "function": {"name": "...", "parameters": {...}}}],
  "tool_choice": "auto",
  "stream": true,
  "max_tokens": 4096
}
```

**Streaming**: Server-Sent Events (SSE), each chunk is a `data: {...}` JSON line with `choices[0].delta`. The harness accumulates these deltas to reconstruct tool_calls.

### 2.9 GPU Memory Layout

```
H100 SXM5 — 80 GB HBM3
├── Model weights (FP8):  ~35 GB  (44%)
│   ├── Token embeddings
│   ├── 48× transformer blocks
│   │   ├── Attention: Q, K, V, O projections
│   │   ├── MoE: gate + 128 expert FFNs
│   │   └── LayerNorm parameters
│   └── Output projection (lm_head)
│
├── KV Cache pool:        ~34 GB  (42%)  ← with --gpu-memory-utilization 0.90
│   ├── Physical block pool (16 tokens/block)
│   ├── Block tables (per-request logical→physical mapping)
│   └── Slot mapping (token position → memory address)
│
└── Framework overhead:   ~3 GB   (4%)
    ├── PyTorch activations (prefill forward pass)
    ├── Sampling buffers (top-k, temperature)
    └── Scheduler state
```

**KV cache size per token** (approximate for 35B MoE with 48 layers, 4 KV heads, head_dim 128):
```
2 (K+V) × 48 (layers) × 4 (KV heads) × 128 (head_dim) × 1 byte (FP8) ≈ 49 KB/token
```

With 34 GB KV cache: supports roughly **700K active tokens** across all concurrent requests.

---

## Layer 3 — Deployment: `inference/`

This layer bridges the raw GPU cluster (SLURM + NVIDIA) to the serving API that the harness consumes.

### File Layout

```
inference/
├── serve.sh              ← SLURM job submission script
├── connect.sh            ← SSH tunnel for remote access
├── endpoint.sh           ← print active OpenAI-compatible endpoint
├── docker-compose.yml    ← alternative Docker-based deployment
├── requirements.txt      ← vllm>=0.8.5, huggingface_hub>=0.23.0
├── logs/                 ← SLURM stdout/stderr per job
└── models/
    ├── qwen3.6-30b-a3b.sh ← model configuration
    └── glm-4.7-flash.sh  ← alternative model
```

### serve.sh — SLURM Submission

```bash
sbatch inference/serve.sh qwen3.6-30b-a3b
```

**SLURM directives** (embedded as `#SBATCH` comments):
```
--partition=bigTiger       # GPU partition
--gres=gpu:h100_80gb:1     # 1 H100 80GB GPU
--cpus-per-task=16         # 16 CPU cores for data loading / tokenization
--mem=128G                 # 128 GB RAM for HF model loading + serving
```

**What it runs**:
1. Sources `models/qwen3.6-30b-a3b.sh` to load `MODEL_ID`, `PORT`, `EXTRA_ARGS`, etc.
2. Verifies that Slurm exposed an H100 before writing endpoint metadata
3. Sets `HF_HOME=/project/inniang/hf-cache` (shared HuggingFace model cache)
4. Activates Python venv at `/project/inniang/.venv`
5. Launches `python -m vllm.entrypoints.openai.api_server` with all flags

### models/qwen3.6-30b-a3b.sh — Model Configuration

```bash
MODEL_ID="Qwen/Qwen3.6-35B-A3B"        # HuggingFace model ID
SERVED_NAME="qwen3.6-30b-a3b"           # name exposed in /v1/models
PORT=8000
TENSOR_PARALLEL=1                        # single GPU
MAX_MODEL_LEN=262144                     # long-context profile
TOOL_CALL_PARSER="qwen3_xml"
REASONING_PARSER="qwen3"
EXTRA_ARGS="--quantization fp8 --gpu-memory-utilization 0.90 --gdn-prefill-backend triton"
```

Note: the H100-only Slurm request and the runtime GPU check keep this profile
from silently falling back to RTX nodes.

### connect.sh — SSH Tunnel

If the machine running the harness cannot reach the active H100 Slurm node
directly (e.g., behind a login node firewall):

```bash
./inference/connect.sh qwen3.6-30b-a3b
# → ssh -NL 8000:<h100-node>:8000 <h100-node>
```

This forwards `localhost:8000` to the active H100 Slurm node, allowing the
harness to talk to `http://localhost:8000/v1` while the actual vLLM process
runs on the GPU node.

### HuggingFace Model Download

First access to `Qwen/Qwen3.6-35B-A3B` triggers an automatic download via `huggingface_hub`:

```
/project/inniang/hf-cache/
└── hub/
    └── models--Qwen--Qwen3.6-35B-A3B/
        ├── blobs/              ← actual weight files (safetensors shards)
        │   ├── <sha256>        ← ~35 GB total (FP8)
        │   └── ...
        ├── refs/main           ← pointer to current commit hash
        └── snapshots/
            └── <commit>/       ← symlinks to blobs
                ├── config.json
                ├── tokenizer.json
                ├── model-00001-of-00008.safetensors → ../../blobs/<sha>
                └── ...
```

**Download size**: ~35 GB (FP8 weights). In BF16 this would be ~70 GB.

---

## Layer 4 — Harness: ascaris

The harness is a Go CLI (`cmd/ascaris`) that runs an agentic loop: it sends messages to the LLM, receives tool calls, executes them, and feeds results back — repeating until the model returns a final text response.

### Architecture Overview

```
User (terminal)
     ↓
CLI (internal/cli/cli.go)
     ↓
Config (internal/config/config.go)
     ↓
Provider Factory (internal/api/provider.go)
     ↓
OpenAI Adapter (internal/api/openai.go) ──HTTP──▶ vLLM :8000
     ↓
Live Harness loop (internal/runtime/live.go)
     ├── Tool definitions (internal/tools/builtins.go + plugins + MCP)
     ├── Tool dispatch (internal/runtime/live_runtime.go)
     ├── Hooks (internal/hooks/hooks.go)
     ├── Sessions (internal/sessions/store.go)
     └── Recovery (internal/recovery/recovery.go)
```

### 4.1 Entrypoint and CLI

**`cmd/ascaris/main.go`**: bootstraps the process, captures CWD, delegates to `cli.Run()`.

**`internal/cli/cli.go`**:
- Parses global flags: `--model`, `--provider`, `--permission-mode`, `--allowed-tools`, `--max-iterations`
- Slash commands (handled in REPL): `/model`, `/provider`, `/tools`, `/session`, `/plan`, `/commit`, `/memory`, `/security-review`, `/skills`, `/cron`
- Two modes:
  - **REPL** (`runInteractiveREPL`): TTY with prompt, streaming output
  - **One-shot** (`runPrompt`): `ascaris -p "prompt"` for scripted use

### 4.2 Configuration

**`internal/config/config.go`**: deep-merges config from four sources (later overrides earlier):

```
~/.ascaris/settings.json          (user defaults)
.ascaris/settings.json            (project settings, committed)
.ascaris/settings.local.json      (local overrides, gitignored)
ASCARIS_CONFIG_HOME env var       (override config home location)
```

**Relevant config keys for this deployment**:
```json
{
  "model": "qwen3.6-30b-a3b",
  "provider": {
    "kind": "openai",
    "openai_base_url": "http://localhost:8000/v1",
    "openai_api_key": "token-abc123"
  },
  "permission_mode": "workspace_write",
  "max_iterations": 32
}
```

### 4.3 Provider Abstraction

**`internal/api/provider.go`**:

```go
type MessageClient interface {
    ProviderKind() ProviderKind
    StreamMessage(ctx context.Context, request MessageRequest) (MessageResponse, error)
}
```

`NewProviderClient(model, cfg)` returns:
- `*Client` — Anthropic native client (uses `/v1/messages`, `x-api-key` header)
- `*OpenAICompatClient` — for OpenAI / OpenRouter / xAI / local vLLM (uses `/v1/chat/completions`)

Auto-detection: if `model` contains `claude` → Anthropic; otherwise → OpenAI-compatible. Config can override with explicit `provider.kind`.

**`internal/api/openai.go`** (`OpenAICompatClient`):
- Translates `MessageRequest` (harness internal format) → OpenAI wire format
- Converts Anthropic-style `InputContentBlock` messages to OpenAI `role`/`content`/`tool_calls` structure
- Streams SSE response, accumulates deltas, converts back to `MessageResponse`
- Handles: streaming tool_call reconstruction (JSON chunks arrive incrementally), reasoning_content field, stop reasons

### 4.4 API Types

**`internal/api/types.go`** — the shared message protocol:

```go
// Outbound (harness → model)
type MessageRequest struct {
    Model      string
    MaxTokens  int
    Messages   []InputMessage
    System     string
    Tools      []ToolDefinition
    ToolChoice string
    Stream     bool
}

type InputMessage struct {
    Role    string                // "user" | "assistant" | "tool"
    Content []InputContentBlock
}

type InputContentBlock struct {
    Type      string   // "text" | "tool_use" | "tool_result"
    Text      string
    ID        string   // tool_use ID
    Name      string   // tool name
    Input     any      // tool arguments
    ToolUseID string   // for tool_result blocks
    IsError   bool
}

// Inbound (model → harness)
type MessageResponse struct {
    Content    []OutputContentBlock
    StopReason string   // "tool_use" | "end_turn" | "max_tokens"
    Usage      Usage
}

type OutputContentBlock struct {
    Type  string   // "text" | "tool_use" | "thinking"
    Text  string
    ID    string   // tool_use ID
    Name  string   // tool name
    Input any      // parsed JSON arguments
}

type ToolDefinition struct {
    Name        string
    Description string
    InputSchema  any   // JSON Schema object
}
```

### 4.5 Agentic Iteration Loop

**`internal/runtime/live.go`** — `LiveHarness.RunPrompt()`:

```
Initialize:
  Load/create session (.ascaris/sessions/<id>.jsonl)
  Build ProviderConfig from merged config
  Create MessageClient (OpenAICompatClient → vLLM)
  Initialize liveRuntime (plugins, MCP, hooks, worker state)
  Collect tool definitions (built-ins + plugins + MCP tools)

Iteration loop (up to MaxIterations = 32):

  ┌─────────────────────────────────────────────────┐
  │ 1. SEND                                         │
  │    Build MessageRequest:                        │
  │      - system prompt (injected by harness)      │
  │      - full session message history             │
  │      - all tool definitions (JSON Schema)       │
  │    client.StreamMessage() → SSE chunks → resp   │
  └──────────────────────┬──────────────────────────┘
                         ↓
  ┌─────────────────────────────────────────────────┐
  │ 2. PARSE                                        │
  │    Scan resp.Content for OutputContentBlock     │
  │    where Type == "tool_use"                     │
  │    If none → return resp.FinalText() to user    │
  └──────────────────────┬──────────────────────────┘
                         ↓
  ┌─────────────────────────────────────────────────┐
  │ 3. EXECUTE TOOLS (parallel where safe)          │
  │    For each tool_call:                          │
  │      a) hookRunner.RunPreToolUse()              │
  │         → can deny (block execution)            │
  │         → can fail (return error to model)      │
  │         → can override permission level         │
  │      b) liveRuntime.ExecuteTool()               │
  │         → try built-in tools first              │
  │         → fall back to plugin tools             │
  │         → fall back to MCP server tools         │
  │      c) hookRunner.RunPostToolUse()             │
  │         → append extra messages if needed       │
  │      d) On failure: attemptRecovery()           │
  └──────────────────────┬──────────────────────────┘
                         ↓
  ┌─────────────────────────────────────────────────┐
  │ 4. FEED BACK                                    │
  │    Append assistant message (with tool_calls)   │
  │    Append tool result message (ToolResultEnvelope)│
  │    Save session to disk                         │
  │    Continue iteration                           │
  └─────────────────────────────────────────────────┘

Finalize:
  Save session
  Return PromptSummary (message, usage, cost, tool_uses, iterations)
```

### 4.6 Tool System

**`internal/tools/builtins.go`** — built-in tools, their definitions and execution:

| Tool | Description | Permission required |
|------|-------------|---------------------|
| `read_file` | Read file contents with line numbers | read_only |
| `write_file` | Create or overwrite a file | workspace_write |
| `edit_file` | Replace a substring in a file | workspace_write |
| `glob_search` | Find files matching a glob pattern | read_only |
| `grep_search` | Regex search across files | read_only |
| `bash` | Execute a shell command | danger_full_access |
| `task_create` | Create a tracked task | read_only |
| `task_update` | Update task status (open/in_progress/done) | read_only |
| `task_list` | List all tasks | read_only |
| `request_plan_approval` | Show plan to user for Y/N gate | read_only |

**Path safety**: every file path is resolved through `resolveWorkspacePath()` which ensures all file operations stay within the workspace root. Any path that would escape (via `../..`) is rejected.

**Permission modes**:
```
read_only          → read_file, glob, grep, tasks only
workspace_write    → + write_file, edit_file
danger_full_access → + bash (unrestricted shell)
```

### 4.7 Permissions and Hooks

**`internal/permissions/permissions.go`**: deny-list at the tool-name level:
```go
type ToolPermissionContext struct {
    DenyNames    map[string]struct{}   // exact tool names to block
    DenyPrefixes []string              // prefix patterns to block
}
```

**`internal/hooks/hooks.go`**: hooks run shell commands at tool boundaries, configured in `.ascaris/settings.json`:
```json
{
  "hooks": {
    "pre_tool_use": [{"command": "my-policy-check --tool $TOOL_NAME"}],
    "post_tool_use": [{"command": "my-logger --tool $TOOL_NAME"}]
  }
}
```

Pre-tool hooks return a structured response that can:
- **Allow** (default)
- **Deny** with reason (model sees the denial, adjusts plan)
- **Fail** (treat as tool execution error)
- **Override permission** (elevate or restrict per-call)

### 4.8 Session Persistence

**`internal/sessions/store.go`**: sessions are stored as append-only JSONL:

```
.ascaris/sessions/
└── <session-id>.jsonl    ← one JSON object per line
    ├── {"type":"meta","session_id":"...","model":"...","created_at":...}
    ├── {"role":"user","content":[{"type":"text","text":"..."}]}
    ├── {"role":"assistant","content":[{"type":"tool_use","id":"...","name":"read_file",...}]}
    └── {"role":"user","content":[{"type":"tool_result","tool_use_id":"...","content":"..."}]}
```

- **Resume**: `ascaris --resume <session-id>` replays the full history into the next request
- **Fork**: creates a new session with a `parent_session_id` reference (useful for branching experiments)
- **Compaction**: when session grows too large, a summary is generated and old messages are truncated

### 4.9 Plugins and MCP

**Plugins** (`internal/plugins/plugins.go`): extend the tool set with external processes. A plugin is a directory containing `.ascaris-plugin/plugin.json`:

```json
{
  "name": "my-plugin",
  "tools": [{
    "name": "my_tool",
    "description": "...",
    "input_schema": {...},
    "command": "./my_tool_handler",
    "required_permission": "workspace_write"
  }],
  "lifecycle": {
    "init": ["./setup.sh"],
    "shutdown": ["./teardown.sh"]
  },
  "hooks": {
    "pre_tool_use": ["./pre-check.sh"],
    "post_tool_use": ["./post-log.sh"]
  }
}
```

**MCP (Model Context Protocol)** (`internal/mcp/mcp.go`): standardized protocol for connecting model clients to external tool servers. The harness discovers MCP servers from config, probes them with `Registry.Discover()`, and adds their tools to the tool definitions list. This enables integration with any MCP-compatible server (filesystem, web search, databases, etc.).

### 4.10 Recovery

**`internal/recovery/recovery.go`**: when the agentic loop hits a failure (provider timeout, MCP disconnection, plugin crash), recovery scenarios are tried before surfacing to the user:

| Scenario | Recovery steps |
|----------|---------------|
| Provider API failure | Retry request → restart worker state |
| MCP handshake failure | Re-probe MCP server → retry |
| Plugin startup failure | Restart plugin process → re-init |
| Git conflicts | Rebase branch → clean build probe |

Recovery state is persisted in the worker registry so a restarted harness process can resume mid-recovery.

---

## End-to-End Data Flow

```
User types: "refactor internal/api/types.go to add a Timeout field"
                         │
                         ▼
          ┌──────────────────────────┐
          │  CLI (cli.go)            │
          │  Parse global flags      │
          │  Load config             │
          └──────────┬───────────────┘
                     │
                     ▼
          ┌──────────────────────────┐
          │  Config (config.go)      │
          │  model: qwen3.6-30b-a3b  │
          │  base_url: :8000/v1      │
          └──────────┬───────────────┘
                     │
                     ▼
          ┌──────────────────────────┐
          │  Provider Factory        │
          │  → OpenAICompatClient    │
          └──────────┬───────────────┘
                     │
                     ▼
          ┌──────────────────────────────────────────────┐
          │  Live Harness (live.go)  — Iteration 1       │
          │                                              │
          │  POST /v1/chat/completions                   │
          │  { model: "qwen3.6-30b-a3b",                 │
          │    messages: [system, user],                  │
          │    tools: [read_file, write_file, ...] }     │
          └──────────────┬───────────────────────────────┘
                         │  HTTP SSE stream
                         ▼
          ┌──────────────────────────────────────────────┐
          │  vLLM :8000 (active H100 node, via tunnel)   │
          │                                              │
          │  Prefill: tokenize + forward pass on prompt  │
          │  Decode:  generate tool call tokens          │
          │  Parser:  qwen3_xml → extract tool_call JSON │
          │                                              │
          │  SSE response:                               │
          │  {"choices":[{"delta":{"tool_calls":[{       │
          │    "function":{"name":"read_file",           │
          │    "arguments":"{\"path\":\"internal/...\"}" │
          │  }}]}}]}                                     │
          └──────────────┬───────────────────────────────┘
                         │
                         ▼
          ┌──────────────────────────────────────────────┐
          │  Tool Dispatch (live_runtime.go)             │
          │                                              │
          │  pre_hook: allow                             │
          │  built-in read_file:                         │
          │    resolveWorkspacePath("internal/api/types.go")
          │    read file from disk                       │
          │    return contents as string                 │
          │  post_hook: log                              │
          └──────────────┬───────────────────────────────┘
                         │
                         ▼
          ┌──────────────────────────────────────────────┐
          │  Live Harness — Iteration 2                  │
          │                                              │
          │  Append to messages:                         │
          │    assistant: {tool_calls: [read_file]}      │
          │    tool: {tool_result: "<file contents>"}    │
          │                                              │
          │  POST /v1/chat/completions (with history)    │
          └──────────────┬───────────────────────────────┘
                         │  (model now reads the file, decides to edit it)
                         ▼
          ┌──────────────────────────────────────────────┐
          │  vLLM generates edit_file tool call          │
          └──────────────┬───────────────────────────────┘
                         │
                         ▼
          ┌──────────────────────────────────────────────┐
          │  Tool Dispatch — edit_file                   │
          │    permission check: workspace_write ✓       │
          │    apply string replacement to file          │
          └──────────────┬───────────────────────────────┘
                         │
                         ▼
          ┌──────────────────────────────────────────────┐
          │  Live Harness — Iteration 3                  │
          │  model returns final text (no tool_calls)    │
          │  → "Done. Added Timeout field to types.go."  │
          └──────────────┬───────────────────────────────┘
                         │
                         ▼
          ┌──────────────────────────────────────────────┐
          │  Session saved to .ascaris/sessions/<id>.jsonl│
          │  PromptSummary: 3 iterations, 2 tool_uses,  │
          │  ~2400 tokens, estimated cost printed        │
          └──────────────────────────────────────────────┘
                         │
                         ▼
                   User sees result
```

---

## Quick Reference: Layer Summary

| Layer | Technology | Key abstraction | Where |
|-------|-----------|----------------|-------|
| Silicon | NVIDIA H100 | SM / Tensor Core / HBM3 | Hardware |
| Model | Qwen3.6-35B-A3B | MoE + GQA + RoPE + FlashAttn | `/project/inniang/hf-cache` |
| Serving | vLLM | PagedAttention + continuous batching | active H100 node |
| Deployment | SLURM + Python venv | serve.sh + model config | `inference/` |
| Harness | ascaris (Go) | MessageClient + tool loop | `internal/` |

| Serving flag | Purpose |
|---|---|
| `--model Qwen/Qwen3.6-35B-A3B` | HF model ID |
| `--served-model-name qwen3.6-30b-a3b` | Name in API |
| `--quantization fp8` | FP8 weights: 35 GB vs 70 GB BF16 |
| `--gpu-memory-utilization 0.90` | Reserve 90% VRAM for model+KV cache |
| `--max-model-len 262144` | Long-context profile |
| `--enable-auto-tool-choice` | Model can emit tool calls |
| `--tool-call-parser qwen3_xml` | Parse `<tool_call>` XML → OpenAI JSON |
| `--reasoning-parser qwen3` | Split `<think>` reasoning from answer |
