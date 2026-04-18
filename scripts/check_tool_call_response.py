#!/usr/bin/env python3
import argparse
import json
import sys


def fail(message: str) -> None:
    print(f"error: {message}", file=sys.stderr)
    raise SystemExit(1)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Validate an OpenAI-compatible chat completion tool-call response."
    )
    parser.add_argument(
        "path",
        nargs="?",
        help="Optional JSON file path. Reads stdin when omitted.",
    )
    parser.add_argument(
        "--expect-tool",
        help="Require exactly one tool call with this function name.",
    )
    parser.add_argument(
        "--expect-arg",
        action="append",
        default=[],
        help="Require the parsed tool arguments object to include this key. Repeatable.",
    )
    parser.add_argument(
        "--allow-no-tool",
        action="store_true",
        help="Allow a normal assistant response with no tool call.",
    )
    args = parser.parse_args()

    if args.path:
        with open(args.path, "r", encoding="utf-8") as handle:
            payload = json.load(handle)
    else:
        payload = json.load(sys.stdin)

    choices = payload.get("choices")
    if not isinstance(choices, list) or not choices:
        fail("missing choices array")

    message = choices[0].get("message")
    if not isinstance(message, dict):
        fail("missing choices[0].message object")

    tool_calls = message.get("tool_calls") or []
    if not tool_calls:
        if args.allow_no_tool:
            content = message.get("content")
            if isinstance(content, str) and content.strip():
                print("ok: no tool call present and assistant content is non-empty")
                return
            fail("no tool call present and assistant content is empty")
        fail("no tool_calls found")

    if not isinstance(tool_calls, list):
        fail("tool_calls is not a list")
    if len(tool_calls) != 1:
        fail(f"expected exactly one tool call, got {len(tool_calls)}")

    tool_call = tool_calls[0]
    function = tool_call.get("function")
    if not isinstance(function, dict):
        fail("missing tool_calls[0].function object")

    name = function.get("name")
    if not isinstance(name, str) or not name.strip():
        fail("tool call function name is missing")
    if args.expect_tool and name != args.expect_tool:
        fail(f"expected tool {args.expect_tool!r}, got {name!r}")

    arguments = function.get("arguments")
    if not isinstance(arguments, str) or not arguments.strip():
        fail("tool call arguments string is missing")

    try:
        parsed_arguments = json.loads(arguments)
    except json.JSONDecodeError as exc:
        fail(f"tool call arguments are not valid JSON: {exc}")

    if not isinstance(parsed_arguments, dict):
        fail(f"tool call arguments must decode to an object, got {type(parsed_arguments).__name__}")

    for key in args.expect_arg:
        if key not in parsed_arguments:
            fail(f"missing expected argument key {key!r}")

    finish_reason = choices[0].get("finish_reason")
    print(
        "ok:",
        json.dumps(
            {
                "tool": name,
                "arguments": parsed_arguments,
                "finish_reason": finish_reason,
            },
            sort_keys=True,
        ),
    )


if __name__ == "__main__":
    main()
