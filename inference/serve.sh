#!/usr/bin/env bash
# Usage: sbatch inference/serve.sh <model-alias>
# Example: sbatch inference/serve.sh qwen3.6-30b-a3b

#SBATCH --job-name=serve-vllm-h100
#SBATCH --partition=bigTiger
#SBATCH --nodes=1
#SBATCH --ntasks-per-node=1
#SBATCH --gres=gpu:h100_80gb:1
#SBATCH --cpus-per-task=16
#SBATCH --mem=128G
#SBATCH --output=/project/inniang/harness/inference/logs/slurm-serve-%j.out
#SBATCH --error=/project/inniang/harness/inference/logs/slurm-serve-%j.err
#SBATCH --export=ALL

set -euo pipefail

REPO_ROOT="${SLURM_SUBMIT_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
SCRIPT_DIR="${REPO_ROOT}/inference"

if [ "$#" -lt 1 ]; then
    printf 'Usage: sbatch %s <model-alias>\n' "$0" >&2
    printf 'Available models:\n' >&2
    for f in "${SCRIPT_DIR}/models/"*.sh; do
        printf '  %s\n' "$(basename "${f%.sh}")" >&2
    done
    exit 1
fi

ALIAS="$1"
MODEL_FILE="${SCRIPT_DIR}/models/${ALIAS}.sh"
if [ ! -f "${MODEL_FILE}" ]; then
    printf 'Unknown model alias: %s\n' "${ALIAS}" >&2
    printf 'Add a config to %s/models/%s.sh first.\n' "${SCRIPT_DIR}" "${ALIAS}" >&2
    exit 1
fi

# shellcheck source=/dev/null
source "${MODEL_FILE}"

mkdir -p "${SCRIPT_DIR}/logs"

REQUIRED_GPU_NAME="${REQUIRED_GPU_NAME:-H100}"
NODE_NAME="${SLURMD_NODENAME:-$(hostname -s)}"
JOB_ID="${SLURM_JOB_ID:-manual}"
DIRECT_BASE_URL="http://${NODE_NAME}:${PORT}/v1"
LOCAL_BASE_URL="http://127.0.0.1:${PORT}/v1"
ENDPOINT_FILE="${SCRIPT_DIR}/logs/current-${ALIAS}-h100.env"

if ! command -v nvidia-smi >/dev/null 2>&1; then
    printf 'Refusing to start: nvidia-smi is not available, so H100 allocation cannot be verified.\n' >&2
    exit 2
fi

GPU_NAMES="$(nvidia-smi --query-gpu=name --format=csv,noheader)"
case "${GPU_NAMES}" in
    *"${REQUIRED_GPU_NAME}"*)
        ;;
    *)
        printf 'Refusing to start: expected a GPU matching "%s", but Slurm exposed:\n%s\n' "${REQUIRED_GPU_NAME}" "${GPU_NAMES}" >&2
        exit 2
        ;;
esac

{
    printf 'VLLM_ALIAS=%q\n' "${ALIAS}"
    printf 'VLLM_JOB_ID=%q\n' "${JOB_ID}"
    printf 'VLLM_JOB_NAME=%q\n' "serve-vllm-h100"
    printf 'VLLM_GPU_REQUIREMENT=%q\n' "${REQUIRED_GPU_NAME}"
    printf 'VLLM_HOST=%q\n' "${NODE_NAME}"
    printf 'VLLM_PORT=%q\n' "${PORT}"
    printf 'OPENAI_BASE_URL=%q\n' "${DIRECT_BASE_URL}"
    printf 'LOCAL_OPENAI_BASE_URL=%q\n' "${LOCAL_BASE_URL}"
} > "${ENDPOINT_FILE}"

export HF_HOME="${HF_HOME:-/project/inniang/hf-cache}"
export VLLM_WORKER_MULTIPROC_METHOD=spawn

source "/project/inniang/.venv/bin/activate"

printf '[%s] Starting vLLM for %s on %s:%s\n' "$(date -Iseconds)" "${SERVED_NAME}" "${NODE_NAME}" "${PORT}"
printf '[%s] Verified GPU allocation: %s\n' "$(date -Iseconds)" "${GPU_NAMES}"
printf '[%s] Direct OpenAI base URL: %s\n' "$(date -Iseconds)" "${DIRECT_BASE_URL}"
printf '[%s] Tunnel OpenAI base URL: %s (run ./inference/connect.sh %s)\n' "$(date -Iseconds)" "${LOCAL_BASE_URL}" "${ALIAS}"
printf '[%s] Wrote endpoint metadata: %s\n' "$(date -Iseconds)" "${ENDPOINT_FILE}"

exec python -m vllm.entrypoints.openai.api_server \
    --model "${MODEL_ID}" \
    --served-model-name "${SERVED_NAME}" \
    --tensor-parallel-size "${TENSOR_PARALLEL}" \
    --max-model-len "${MAX_MODEL_LEN}" \
    --enable-auto-tool-choice \
    --tool-call-parser "${TOOL_CALL_PARSER}" \
    --reasoning-parser "${REASONING_PARSER}" \
    --host 0.0.0.0 \
    --port "${PORT}" \
    ${EXTRA_ARGS}
