#!/usr/bin/env python3
"""leankg runner for grep_vs_leankg/questions.json → leankg.json"""
import json, subprocess, time, sys, re

QUESTIONS = json.load(open("benchmark/grep_vs_leankg/questions.json"))
TOP_K = QUESTIONS["top_k"]
PORT = "9799"
URL = f"http://localhost:{PORT}/mcp?project=/Users/linh.doan/work/harvey/freepeak/leankg"

def mcp_call(payload, timeout=90):
    proc = subprocess.run(
        ["curl", "-s", "--max-time", str(timeout), "-X", "POST", URL,
         "-H", "Content-Type: application/json",
         "-H", "Accept: application/json, text/event-stream",
         "-d", json.dumps(payload)],
        capture_output=True, text=True)
    raw = proc.stdout
    for line in raw.splitlines():
        if line.startswith("data:"):
            raw = line[5:].strip()
            break
    return json.loads(raw)

def extract_qns(text, n):
    """Pull qualified_name strings from the TOON payload (they are the quoted
    strings containing '::' or full paths; ranked in document order)."""
    qns = re.findall(r'"(/[^"]+::[^"]*)"', text)
    seen, out = set(), []
    for q in qns:
        if q not in seen:
            seen.add(q)
            out.append(q)
    return out[:n]

results = []
for q in QUESTIONS["questions"]:
    if q["leankg_mode"] == "router":
        payload = {"jsonrpc": "2.0", "id": 1, "method": "tools/call",
                   "params": {"name": "get", "arguments": {"query": q["question"], "limit": TOP_K}}}
    t0 = time.perf_counter()
    try:
        resp = mcp_call(payload)
        dt = time.perf_counter() - t0
        res = resp.get("result", {})
        err = resp.get("error", {}).get("message")
        text = res.get("content", [{}])[0].get("text", "") if "result" in resp else ""
        hit = "status: ok" in text
        qns = extract_qns(text, TOP_K)
        rung = re.findall(r"rung: (\w+)", text)
        method = re.findall(r"method: ([^\n]+)", text)
        results.append({
            "id": q["id"], "category": q["category"], "question": q["question"],
            "tool": "leankg", "elapsed_s": round(dt, 3),
            "ok": hit, "error": err,
            "rung": rung[0] if rung else None,
            "method": method[0][:60] if method else None,
            "returned_qns": qns,
            "ground_truth": q["ground_truth"],
        })
        print(f"{q['id']:24s} {dt*1000:8.0f}ms  rung={rung[0] if rung else '-':9s}  {len(qns)} qns")
    except Exception as e:
        print(f"{q['id']:24s} EXCEPTION {e}")
        results.append({"id": q["id"], "tool": "leankg", "ok": False, "error": str(e)})

json.dump({"tool": "leankg", "top_k": TOP_K, "results": results},
          open("benchmark/grep_vs_leankg/leankg.json", "w"), indent=1)
print("leankg.json written")
