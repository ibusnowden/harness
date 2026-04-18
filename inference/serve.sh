#!/usr/bin/env bash
# Usage: sbatch inference/serve.sh <model-alias>
# Example: sbatch inference/serve.sh qwen3.6-30b-a3b

#SBATCH --job-name=serve-vllm
#SBATCH --partition=bigTiger
#SBATCH --nodelist=itiger01
#SBATCH --nodes=1
#SBATCH --ntasks-per-node=1
#SBATCH --gres=gpu:1
#SBATCH --cpus-per-task=16
#SBATCH --mem=64G
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

export HF_HOME="${HF_HOME:-/project/inniang/hf-cache}"
export VLLM_WORKER_MULTIPROC_METHOD=spawn

source "/project/inniang/.venv/bin/activate"

printf '[%s] Starting vLLM for %s on port %s\n' "$(date -Iseconds)" "${SERVED_NAME}" "${PORT}"

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
