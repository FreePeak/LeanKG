#!/usr/bin/env python3
"""Mock LSP server for end-to-end testing of the LeanKG LSP bridge.

Reads JSON-RPC frames from stdin and writes responses to stdout
using the standard `Content-Length` framing. Replies to:
  - initialize -> { capabilities: { ... } }
  - initialized -> no-op
  - textDocument/didOpen -> no-op
  - textDocument/definition -> returns a fake Location pointing
    to /tmp/lsp_mock_target.go:10:5-15

Used by `cargo test --release --lib lsp::e2e::spawns_mock_server`.
"""
import json
import sys


def read_frame():
    """Read one JSON-RPC frame from stdin. Returns the parsed body."""
    headers = {}
    while True:
        line = sys.stdin.readline()
        if not line:
            return None
        line = line.rstrip("\r\n")
        if line == "":
            break
        if ":" in line:
            k, v = line.split(":", 1)
            headers[k.strip().lower()] = v.strip()
    length = int(headers.get("content-length", "0"))
    if length == 0:
        return None
    body = sys.stdin.read(length)
    return json.loads(body)


def write_frame(msg):
    body = json.dumps(msg)
    sys.stdout.write(f"Content-Length: {len(body)}\r\n\r\n{body}")
    sys.stdout.flush()


def main():
    while True:
        msg = read_frame()
        if msg is None:
            return
        method = msg.get("method")
        msg_id = msg.get("id")
        if method == "initialize":
            write_frame({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "capabilities": {
                        "definitionProvider": True,
                        "referencesProvider": True,
                    }
                },
            })
        elif method == "textDocument/definition":
            write_frame({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": {
                    "uri": "file:///tmp/lsp_mock_target.go",
                    "range": {
                        "start": {"line": 9, "character": 4},
                        "end": {"line": 14, "character": 15},
                    },
                },
            })
        elif method == "textDocument/references":
            write_frame({
                "jsonrpc": "2.0",
                "id": msg_id,
                "result": [
                    {
                        "uri": "file:///tmp/lsp_mock_caller.go",
                        "range": {
                            "start": {"line": 0, "character": 0},
                            "end": {"line": 0, "character": 1},
                        },
                    }
                ],
            })
        elif method == "textDocument/didOpen":
            pass  # notification, no reply
        elif method == "exit":
            return
        else:
            if msg_id is not None:
                write_frame({
                    "jsonrpc": "2.0",
                    "id": msg_id,
                    "error": {"code": -32601, "message": f"unknown method {method}"},
                })


if __name__ == "__main__":
    try:
        main()
    except BrokenPipeError:
        pass
