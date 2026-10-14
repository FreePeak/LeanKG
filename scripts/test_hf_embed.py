"""Tests for scripts/hf-embed.sh, run against stub hf and leankg-embed executables.

Nothing real runs: the stubs log their argv to calls.log and write fixed files.

Run from the repository root:

    python3 -m unittest scripts/test_hf_embed.py -v

Standard library only.
"""

from __future__ import annotations

import os
import re
import tempfile
import unittest
from pathlib import Path
import subprocess

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "hf-embed.sh"
BUCKET = "<your-org>/<your-bucket>"
BUCKET_URI = f"hf://buckets/{BUCKET}"
VECTOR_LINE = '{"qualified_name":"pkg.F1","content_hash":"c1","vec":[0.5]}'

# Stub hf: logs argv; uploads copy the local file aside; downloads write a fixed vector line.
HF_STUB = r"""#!/usr/bin/env bash
set -eu
echo "hf $*" >> "$STUB_DIR/calls.log"
case "$1 $2" in
  "buckets cp")
    case "$3" in
      hf://*) printf '%s\n' '__VECTOR_LINE__' > "$4" ;;
      *) cp "$3" "$STUB_DIR/uploaded.ndjson" ;;
    esac ;;
  "jobs uv") echo "job-stub-1" ;;
  "jobs wait") [ -z "${HF_STUB_WAIT_FAIL:-}" ] ;;
  "jobs logs") echo "stub log line" ;;
  "jobs inspect") echo "{}" ;;
  "buckets rm") ;;
  *) echo "hf stub: unexpected $*" >&2; exit 2 ;;
esac
""".replace("__VECTOR_LINE__", VECTOR_LINE)

# Stub leankg-embed: export writes STUB_EXPORT_LINES lines to --out; import copies --in aside.
EMBED_STUB = r"""#!/usr/bin/env bash
set -eu
echo "leankg-embed $*" >> "$STUB_DIR/calls.log"
case "$1" in
  export)
    out=""; prev=""
    for a in "$@"; do
      if [ "$prev" = "--out" ]; then out="$a"; fi
      prev="$a"
    done
    : > "$out"
    i=1
    while [ "$i" -le "${STUB_EXPORT_LINES:-50}" ]; do
      printf '%s\n' "{\"qualified_name\":\"pkg.F$i\",\"content_hash\":\"c$i\",\"text\":\"passage: f$i\"}" >> "$out"
      i=$((i + 1))
    done ;;
  import)
    in=""; prev=""
    for a in "$@"; do
      if [ "$prev" = "--in" ]; then in="$a"; fi
      prev="$a"
    done
    cp "$in" "$STUB_DIR/imported.ndjson"
    echo '{"imported":1}' ;;
  version) echo "leankg-embed stub" ;;
  *) echo "leankg-embed stub: unexpected $*" >&2; exit 2 ;;
esac
"""


class HfEmbedTests(unittest.TestCase):
    def setUp(self) -> None:
        self._tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self._tmp.name)
        self.project = self.dir / "project"
        self.project.mkdir()
        self.hf = self._stub("hf", HF_STUB)
        self.embed = self._stub("leankg-embed", EMBED_STUB)

    def tearDown(self) -> None:
        self._tmp.cleanup()

    def _stub(self, name: str, body: str) -> Path:
        path = self.dir / name
        path.write_text(body)
        path.chmod(0o755)
        return path

    def run_script(self, *args: str, env: dict | None = None, drop: tuple = (), lines: int = 50):
        full = {
            k: v
            for k, v in os.environ.items()
            if not k.startswith(("LEANKG_HF_", "LEANKG_EMBED_")) and k != "HF_TOKEN"
        }
        full.update({
            "HF_BIN": str(self.hf),
            "LEANKG_EMBED_BIN": str(self.embed),
            "LEANKG_EMBED_MODEL": "embeddinggemma-2",
            "LEANKG_HF_BUCKET": BUCKET,
            "HF_TOKEN": "test-token-not-real-123",
            "STUB_DIR": str(self.dir),
            "STUB_EXPORT_LINES": str(lines),
        })
        full.update(env or {})
        for key in drop:
            full.pop(key, None)
        return subprocess.run(
            [str(SCRIPT), *args, str(self.project)],
            env=full,
            capture_output=True,
            text=True,
            timeout=120,
            cwd=str(self.dir),
        )

    def calls(self) -> list[str]:
        log = self.dir / "calls.log"
        return log.read_text().splitlines() if log.exists() else []

    def hf_calls(self) -> list[str]:
        return [c for c in self.calls() if c.startswith("hf ")]

    def assert_in_order(self, lines: list[str], patterns: list[str]) -> None:
        pos = 0
        for pattern in patterns:
            hit = next(
                (i for i in range(pos, len(lines)) if re.search(pattern, lines[i])),
                None,
            )
            self.assertIsNotNone(hit, f"no call matching {pattern!r} after position {pos}: {lines}")
            pos = hit + 1

    def test_missing_bucket_refused_before_any_call(self) -> None:
        r = self.run_script("--yes", drop=("LEANKG_HF_BUCKET",))
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("LEANKG_HF_BUCKET", r.stderr)
        self.assertEqual(self.calls(), [])

    def test_no_yes_prints_estimate_and_refuses_without_upload(self) -> None:
        r = self.run_script()
        out = r.stdout + r.stderr
        self.assertNotEqual(r.returncode, 0)
        self.assertRegex(out, r"lines to embed:\s+50\b")
        self.assertRegex(out, r"estimate:\s+[0-9.]+ min, \$[0-9.]+")
        self.assertRegex(out, r"worst case:\s+\$0\.40\b")
        self.assertIn("re-run with --yes", out)
        self.assertEqual(self.hf_calls(), [])

    def test_dry_run_prints_plan_and_calls_no_hf(self) -> None:
        r = self.run_script("--dry-run")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.hf_calls(), [])
        for needle in (
            "--flavor l4x1",
            "--timeout 30m",
            f"-v {BUCKET_URI}:/data",
            "hf_embed_job.py",
            "--model-dir /data ",
            "--in /data/leankg/",
            "--out /data/leankg/",
        ):
            self.assertIn(needle, r.stdout)

    def test_dry_run_is_refused_when_worst_case_over_budget(self) -> None:
        r = self.run_script("--dry-run", env={"LEANKG_HF_JOB_TIMEOUT": "2h"})
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("LEANKG_HF_MAX_USD", r.stderr)
        self.assertEqual(self.hf_calls(), [])

    def test_worst_case_over_budget_refused_even_with_yes(self) -> None:
        # 2h at l4x1 ($0.80/h) is a $1.60 worst case against the $0.50 default budget.
        r = self.run_script("--yes", env={"LEANKG_HF_JOB_TIMEOUT": "2h"})
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("LEANKG_HF_MAX_USD", r.stderr)
        self.assertEqual(self.hf_calls(), [])
        self.assertFalse((self.dir / "uploaded.ndjson").exists())

    def test_estimate_over_timeout_refused_even_with_yes(self) -> None:
        # 50 texts at 0.01 texts/s is about 83 minutes, over the 30m timeout.
        r = self.run_script("--yes", env={"LEANKG_HF_EST_RATE": "0.01"})
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("LEANKG_HF_JOB_TIMEOUT", r.stderr)
        self.assertEqual(self.hf_calls(), [])

    def test_unknown_flavor_refused_before_any_call(self) -> None:
        r = self.run_script("--yes", env={"LEANKG_HF_JOB_FLAVOR": "h100"})
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("LEANKG_HF_JOB_FLAVOR", r.stderr)
        self.assertEqual(self.calls(), [])

    def test_bad_timeout_refused_before_any_call(self) -> None:
        r = self.run_script("--yes", env={"LEANKG_HF_JOB_TIMEOUT": "30x"})
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("LEANKG_HF_JOB_TIMEOUT", r.stderr)
        self.assertEqual(self.calls(), [])

    def test_limit_uploads_only_first_lines(self) -> None:
        r = self.run_script("--yes", "--limit", "5", lines=50)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertRegex(r.stdout, r"lines to embed:\s+5\b")
        uploaded = (self.dir / "uploaded.ndjson").read_text().splitlines()
        self.assertEqual(len(uploaded), 5)
        self.assertTrue(uploaded[0].startswith('{"qualified_name":"pkg.F1"'))

    def test_happy_path_call_order_and_import_of_download(self) -> None:
        r = self.run_script("--yes")
        out = r.stdout + r.stderr
        self.assertEqual(r.returncode, 0, out)
        self.assertNotIn("test-token-not-real-123", out)
        self.assert_in_order(self.calls(), [
            r"^leankg-embed export .*--model embeddinggemma-2",
            rf"^hf buckets cp \S+ {re.escape(BUCKET_URI)}/leankg/\S+/export\.ndjson$",
            r"^hf jobs uv run --detach --quiet --flavor l4x1 --timeout 30m --attempts 1 "
            rf"-v {re.escape(BUCKET_URI)}:/data \S+hf_embed_job\.py "
            r"--model-dir /data --in /data/leankg/\S+/export\.ndjson --out /data/leankg/\S+/vectors\.ndjson$",
            r"^hf jobs wait job-stub-1$",
            r"^hf jobs inspect --json job-stub-1$",
            rf"^hf buckets cp {re.escape(BUCKET_URI)}/leankg/\S+/vectors\.ndjson \S+$",
            r"^leankg-embed import --project \S+ --in \S+$",
            rf"^hf buckets rm --recursive --yes {re.escape(BUCKET)}/leankg/\S+/$",
        ])
        self.assertEqual(
            (self.dir / "imported.ndjson").read_text(),
            VECTOR_LINE + "\n",
        )
        self.assertIn("measured:", out)

    def test_job_failure_fetches_logs_and_imports_nothing(self) -> None:
        r = self.run_script("--yes", env={"HF_STUB_WAIT_FAIL": "1"})
        calls = self.calls()
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("stub log line", r.stdout)
        self.assertTrue(any(re.search(r"^hf jobs logs .*job-stub-1$", c) for c in calls), calls)
        self.assertFalse(any(c.startswith("leankg-embed import") for c in calls), calls)
        self.assertFalse(any(c.startswith("hf buckets rm") for c in calls), calls)
        self.assertFalse(any(re.search(r"^hf buckets cp hf://", c) for c in calls), calls)

    def test_keep_skips_bucket_rm(self) -> None:
        r = self.run_script("--yes", "--keep")
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertTrue((self.dir / "imported.ndjson").exists())
        self.assertFalse(any(c.startswith("hf buckets rm") for c in self.calls()))


if __name__ == "__main__":
    unittest.main()
