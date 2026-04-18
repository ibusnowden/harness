#!/usr/bin/env bash
# connect.sh — create an SSH tunnel from localhost to a vLLM server on itiger01
# Only needed if the login node cannot reach itiger01 directly.
# If http://itiger01:PORT is already reachable, skip this script.
#
# Usage: ./inference/connect.sh <model-alias>
# Example: ./inference/connect.sh qwen3.6-30b-a3b
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

if [ "$#" -lt 1 ]; then
    printf 'Usage: %s <model-alias>\n' "$0" >&2
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

printf 'Opening SSH tunnel: localhost:%s -> itiger01:%s\n' "${PORT}" "${PORT}"
printf 'Press Ctrl-C to close the tunnel.\n'
ssh -NL "${PORT}:itiger01:${PORT}" itiger01
