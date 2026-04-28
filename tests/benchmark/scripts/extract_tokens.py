#!/usr/bin/env python3
"""
Extract token usage from Kilo session export.
Usage: python3 extract_tokens.py <session_export.json>
"""

import json
import sys
from pathlib import Path


def extract_tokens(session_path: str) -> dict:
    """Extract token usage and tool invocations from session export."""
    with open(session_path, "r") as f:
        session = json.load(f)

    info = session.get("info", {})
    messages = session.get("messages", [])

    total_messages = len(messages)
    tool_calls = 0
    leankg_calls = []

    for msg in messages:
        msg_type = msg.get("type", "")
        if msg_type == "tool_call" or "tool_calls" in msg:
            tool_calls += 1
            if "tool_calls" in msg:
                for tc in msg["tool_calls"]:
                    tool_name = tc.get("name", "")
                    if "leankg" in tool_name.lower() or "mcp" in tool_name.lower():
                        leankg_calls.append(tool_name)

    return {
        "session_id": info.get("id", "unknown"),
        "title": info.get("title", "unknown"),
        "total_messages": total_messages,
        "tool_calls": tool_calls,
        "leankg_calls": leankg_calls,
        "directory": info.get("directory", ""),
    }


def main():
    if len(sys.argv) < 2:
        print("Usage: extract_tokens.py <session_export.json>")
        sys.exit(1)

    result = extract_tokens(sys.argv[1])
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
