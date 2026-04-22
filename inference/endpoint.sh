#!/usr/bin/env bash
# Print the active OpenAI-compatible base URL for a served model.
#
# Usage:
#   ./inference/endpoint.sh <model-alias>
#   ./inference/endpoint.sh --local <model-alias>
#   ./inference/endpoint.sh --export <model-alias>
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MODE="direct"

case "${1:-}" in
    --local)
        MODE="local"
        shift
        ;;
    --export)
        MODE="export"
        shift
        ;;
    -h|--help)
        printf 'Usage: %s [--local|--export] <model-alias>\n' "$0"
        exit 0
        ;;
esac

if [ "$#" -lt 1 ]; then
    printf 'Usage: %s [--local|--export] <model-alias>\n' "$0" >&2
    exit 1
fi

ALIAS="$1"
MODEL_FILE="${SCRIPT_DIR}/models/${ALIAS}.sh"
if [ ! -f "${MODEL_FILE}" ]; then
    printf 'Unknown model alias: %s\n' "${ALIAS}" >&2
    exit 1
fi

# shellcheck source=/dev/null
source "${MODEL_FILE}"

SLURM_JOB_NAME="${VLLM_SLURM_JOB_NAME:-serve-vllm-h100}"
METADATA_FILE="${SCRIPT_DIR}/logs/current-${ALIAS}-h100.env"

find_slurm_host() {
    if ! command -v squeue >/dev/null 2>&1; then
        return 0
    fi

    local node_spec=""
    if [ -n "${VLLM_JOB_ID:-}" ]; then
        node_spec="$(squeue -h -j "${VLLM_JOB_ID}" -t RUNNING -o "%N" 2>/dev/null | awk 'NF { node = $1 } END { print node }')"
    else
        local user_name="${USER:-$(id -un)}"
        node_spec="$(squeue -h -u "${user_name}" -n "${SLURM_JOB_NAME}" -t RUNNING -o "%i %N" 2>/dev/null | sort -n | awk 'NF { node = $2 } END { print node }')"
    fi

    if [ -z "${node_spec}" ] || [ "${node_spec}" = "(null)" ]; then
        return 0
    fi

    if command -v scontrol >/dev/null 2>&1; then
        scontrol show hostnames "${node_spec}" 2>/dev/null | awk 'NF { print $1; exit }'
    else
        printf '%s\n' "${node_spec}"
    fi
}

host_from_metadata() {
    local file="$1"
    if [ ! -f "${file}" ]; then
        return 0
    fi

    local meta_host=""
    local meta_job=""
    meta_host="$(awk -F= '$1 == "VLLM_HOST" { print $2; exit }' "${file}")"
    meta_job="$(awk -F= '$1 == "VLLM_JOB_ID" { print $2; exit }' "${file}")"
    if [ -z "${meta_host}" ]; then
        return 0
    fi

    if command -v squeue >/dev/null 2>&1 && [ -n "${meta_job}" ] && [ "${meta_job}" != "manual" ]; then
        local node_spec=""
        node_spec="$(squeue -h -j "${meta_job}" -t RUNNING -o "%N" 2>/dev/null | awk 'NF { node = $1 } END { print node }')"
        if [ -z "${node_spec}" ] || [ "${node_spec}" = "(null)" ]; then
            return 0
        fi
        if command -v scontrol >/dev/null 2>&1; then
            scontrol show hostnames "${node_spec}" 2>/dev/null | awk 'NF { print $1; exit }'
        else
            printf '%s\n' "${node_spec}"
        fi
        return 0
    fi

    printf '%s\n' "${meta_host}"
}

HOST="${VLLM_HOST:-}"
if [ -z "${HOST}" ]; then
    HOST="$(find_slurm_host)"
fi
if [ -z "${HOST}" ] && [ -f "${METADATA_FILE}" ]; then
    HOST="$(host_from_metadata "${METADATA_FILE}")"
fi
if [ -z "${HOST}" ]; then
    printf 'No running %s Slurm job found. Set VLLM_HOST=<node> or VLLM_JOB_ID=<jobid>.\n' "${SLURM_JOB_NAME}" >&2
    exit 1
fi

DIRECT_BASE_URL="http://${HOST}:${PORT}/v1"
LOCAL_BASE_URL="http://127.0.0.1:${PORT}/v1"

case "${MODE}" in
    local)
        printf '%s\n' "${LOCAL_BASE_URL}"
        ;;
    export)
        printf 'export OPENAI_API_KEY=%q\n' "${OPENAI_API_KEY:-dummy}"
        printf 'export OPENAI_BASE_URL=%q\n' "${DIRECT_BASE_URL}"
        ;;
    *)
        printf '%s\n' "${DIRECT_BASE_URL}"
        ;;
esac
