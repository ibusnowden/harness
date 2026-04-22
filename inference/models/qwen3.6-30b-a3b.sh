MODEL_ID="Qwen/Qwen3.6-35B-A3B"
SERVED_NAME="qwen3.6-30b-a3b"
PORT=8000
TENSOR_PARALLEL=1
MAX_MODEL_LEN=262144
TOOL_CALL_PARSER="qwen3_xml"
REASONING_PARSER="qwen3"
# fp8 cuts weights from ~70GB to ~35GB on H100 — no speed penalty, native support
# First cold start on 1×H100: 5-15 min (JIT compile + CUDA graph capture for ~50 batch sizes).
# Subsequent starts are faster once vLLM's compile cache is warm (~/.cache/vllm).
# --gdn-prefill-backend triton avoids the FlashInfer JIT path, shaves ~1-2 min off cold start.
# To scale to 4×H100: set TENSOR_PARALLEL=4 — each GPU loads ~9GB instead of 35GB, ~2-3× faster startup.
EXTRA_ARGS="--quantization fp8 --gpu-memory-utilization 0.90 --gdn-prefill-backend triton"
