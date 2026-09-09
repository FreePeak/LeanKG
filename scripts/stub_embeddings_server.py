#!/usr/bin/env python3
"""Stub OpenAI-compatible /v1/embeddings server for LeanKG contract testing.

Runs entirely locally (stdlib only, no model, no cost). Validates that LeanKG's
OpenAiCompatibleProvider sends the right request shape (bearer auth, model name,
batch of inputs) and handles the response (index ordering, dim validation).

Response vectors are deterministic pseudo-random floats derived from each input
string — stable across calls, garbage semantically. Dim: STUB_DIM env (default 1024).
Usage: python3 stub_embeddings_server.py [port]   (default 8787)
"""
import hashlib
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8787
DIM = int(os.environ.get("STUB_DIM", "1024"))

EXPECTED_KEY = "stub-secret-key"
LOG = "/tmp/opencode/stub_embeddings_requests.jsonl"


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path in ("/health", "/v1/health"):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        if self.path not in ("/v1/embeddings", "/embeddings"):
            self.send_response(404)
            self.end_headers()
            return
        auth = self.headers.get("Authorization", "")
        if auth != f"Bearer {EXPECTED_KEY}":
            self.send_response(401)
            self.end_headers()
            self.wfile.write(b'{"error":"bad bearer token"}')
            return
        length = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(length))
        model = body.get("model")
        inputs = body.get("input")
        if isinstance(inputs, str):
            inputs = [inputs]
        with open(LOG, "a") as f:
            f.write(json.dumps({"model": model, "n_inputs": len(inputs),
                                "first_60_chars": [t[:60] for t in inputs[:3]]}) + "\n")
        data = []
        for i, text in enumerate(inputs):
            seed = hashlib.sha256(text.encode()).digest()
            vec = []
            counter = 0
            while len(vec) < DIM:
                h = hashlib.sha256(seed + counter.to_bytes(4, "little")).digest()
                for j in range(0, 32, 4):
                    if len(vec) >= DIM:
                        break
                    chunk = int.from_bytes(h[j:j+4], "little")
                    vec.append((chunk / 0xFFFFFFFF) * 2.0 - 1.0)
                counter += 1
            data.append({"index": i, "embedding": vec})
        resp = json.dumps({"object": "list", "data": data,
                           "model": model, "usage": {"prompt_tokens": 0, "total_tokens": 0}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(resp)))
        self.end_headers()
        self.wfile.write(resp)


if __name__ == "__main__":
    print(f"stub embeddings server on :{PORT} dim={DIM}", flush=True)
    HTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
