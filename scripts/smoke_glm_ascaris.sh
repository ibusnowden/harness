#!/usr/bin/env bash
set -euo pipefail

if ! command -v curl >/dev/null 2>&1; then
  echo "error: curl is required" >&2
  exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "error: python3 is required" >&2
  exit 1
fi

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
ASCARIS_BIN="${ASCARIS_BIN:-$REPO_ROOT/bin/ascaris}"
MODEL="${MODEL:-GLM-4.7}"
OPENAI_BASE_URL="${OPENAI_BASE_URL:-http://127.0.0.1:8000/v1}"
OPENAI_API_KEY="${OPENAI_API_KEY:-local-dev}"
FIXTURE_PATH="${FIXTURE_PATH:-$REPO_ROOT/fixture.txt}"
CHECKER="$REPO_ROOT/scripts/check_tool_call_response.py"

export OPENAI_BASE_URL
export OPENAI_API_KEY

if [[ ! -x "$ASCARIS_BIN" ]]; then
  echo "error: ascaris binary not found or not executable at $ASCARIS_BIN" >&2
  echo "hint: go build -o ./bin/ascaris ./cmd/ascaris" >&2
  exit 1
fi

if [[ ! -f "$CHECKER" ]]; then
  echo "error: tool-call checker not found at $CHECKER" >&2
  exit 1
fi

echo "==> repo root: $REPO_ROOT"
echo "==> base url : $OPENAI_BASE_URL"
echo "==> model    : $MODEL"

run_json_chat() {
  local payload="$1"
  curl -fsS "$OPENAI_BASE_URL/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $OPENAI_API_KEY" \
    -d "$payload"
}

echo
echo "==> raw chat completion probe"
CHAT_RESPONSE="$(run_json_chat "{
  \"model\": \"$MODEL\",
  \"messages\": [
    {\"role\": \"user\", \"content\": \"Reply with exactly: ok\"}
  ],
  \"max_tokens\": 32,
  \"stream\": false
}")"
printf '%s\n' "$CHAT_RESPONSE" | python3 - <<'PY'
import json, sys
payload = json.load(sys.stdin)
choices = payload.get("choices") or []
if not choices:
    raise SystemExit("missing choices in chat completion response")
message = choices[0].get("message") or {}
content = message.get("content")
if not isinstance(content, str) or not content.strip():
    raise SystemExit(f"unexpected message content: {content!r}")
print("chat completion ok:", content.strip())
PY

echo
echo "==> forced tool-call probe"
TOOL_RESPONSE="$(run_json_chat "{
  \"model\": \"$MODEL\",
  \"messages\": [
    {\"role\": \"user\", \"content\": \"Use the read_file tool for fixture.txt\"}
  ],
  \"tools\": [
    {
      \"type\": \"function\",
      \"function\": {
        \"name\": \"read_file\",
        \"description\": \"Read a file\",
        \"parameters\": {
          \"type\": \"object\",
          \"properties\": {
            \"path\": {\"type\": \"string\"}
          },
          \"required\": [\"path\"],
          \"additionalProperties\": false
        }
      }
    }
  ],
  \"tool_choice\": {
    \"type\": \"function\",
    \"function\": {\"name\": \"read_file\"}
  },
  \"parallel_tool_calls\": false,
  \"max_tokens\": 128,
  \"stream\": false
}")"
printf '%s\n' "$TOOL_RESPONSE" | python3 "$CHECKER" --expect-tool read_file --expect-arg path

echo
echo "==> preparing fixture"
mkdir -p "$(dirname "$FIXTURE_PATH")" "$REPO_ROOT/generated"
printf 'alpha parity line\nbeta line\ngamma parity line\n' > "$FIXTURE_PATH"

run_ascaris() {
  local description="$1"
  shift
  echo
  echo "==> $description"
  "$ASCARIS_BIN" --provider openai --model "$MODEL" "$@"
}

run_ascaris "basic prompt" prompt "Reply with exactly: ok"
run_ascaris "json prompt" --output-format=json prompt "Reply with exactly: json ok"
run_ascaris "read_file tool smoke" --permission-mode workspace-write prompt "Read fixture.txt and print the first line."
run_ascaris "grep_search tool smoke" --permission-mode workspace-write prompt "Count how many times the word parity appears in fixture.txt."
run_ascaris "write_file tool smoke" --permission-mode workspace-write prompt "Write generated/output.txt containing exactly: hello"

cat <<'EOF'

==> manual approval smoke
Run this interactively to verify the approval prompt and spinner behavior:

./bin/ascaris --provider openai --model "$MODEL" --permission-mode workspace-write prompt "Use the bash tool to run: printf 'bash ok'"
EOF
