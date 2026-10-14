"""Tests for scripts/hf_embed_job.py: the I/O contract, with a stub encoder.

Run from the repository root:

    python3 -m unittest scripts/test_hf_embed_job.py -v

Standard library only; the model is never loaded.
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import hf_embed_job  # noqa: E402


class StubEncoder:
    """Records every batch it is asked to embed and returns a 3-d vector per text."""

    def __init__(self, vec=None):
        self.calls: list[list[str]] = []
        self.vec = vec

    def __call__(self, texts):
        self.calls.append(list(texts))
        if self.vec is not None:
            return [list(self.vec) for _ in texts]
        return [[float(len(t)), 0.5, -1.0] for t in texts]

    @property
    def inputs(self) -> list[str]:
        return [t for batch in self.calls for t in batch]


def export_line(qn, text, content_hash=None):
    rec = {"qualified_name": qn, "text": text}
    if content_hash is not None:
        rec["content_hash"] = content_hash
    return json.dumps(rec, ensure_ascii=False)


class HfEmbedJobTest(unittest.TestCase):
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self._tmp.name)

    def tearDown(self):
        self._tmp.cleanup()

    def write_input(self, lines):
        path = self.dir / "export.ndjson"
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        return path

    def read_output(self, path):
        return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line]

    def test_texts_pass_through_unchanged(self):
        texts = [
            "function a::f1 — src/f.go\nfunc f1() {}",
            "title: none | text: already prefixed by the export",
            "line one\nline two with \"quotes\" and \\backslash\\",
        ]
        src = self.write_input([export_line(f"a::f{i}", t) for i, t in enumerate(texts)])
        enc = StubEncoder()
        hf_embed_job.run(src, self.dir / "vectors.ndjson", enc, batch_size=64)
        self.assertEqual(enc.inputs, texts)

    def test_qualified_name_and_content_hash_are_copied(self):
        src = self.write_input([
            export_line("a::f1", "func f1() {}", content_hash="abc123"),
            export_line("b::g", "def g(): pass", content_hash="def456"),
        ])
        out = self.dir / "vectors.ndjson"
        hf_embed_job.run(src, out, StubEncoder(), batch_size=64)
        rows = self.read_output(out)
        self.assertEqual([r["qualified_name"] for r in rows], ["a::f1", "b::g"])
        self.assertEqual([r["content_hash"] for r in rows], ["abc123", "def456"])

    def test_line_without_content_hash_has_no_content_hash_key(self):
        src = self.write_input([export_line("a::f1", "func f1() {}")])
        out = self.dir / "vectors.ndjson"
        hf_embed_job.run(src, out, StubEncoder(), batch_size=64)
        (row,) = self.read_output(out)
        self.assertNotIn("content_hash", row)
        self.assertEqual(set(row), {"qualified_name", "vec"})

    def test_output_is_one_vector_record_per_line_with_no_header(self):
        src = self.write_input([export_line("a::f1", "x"), export_line("a::f2", "yy")])
        out = self.dir / "vectors.ndjson"
        n = hf_embed_job.run(src, out, StubEncoder(vec=[0.25, -0.5, 1.0]), batch_size=64)
        self.assertEqual(n, 2)
        rows = self.read_output(out)
        self.assertEqual(len(rows), 2)
        self.assertEqual(rows[0]["vec"], [0.25, -0.5, 1.0])
        self.assertEqual(rows[1]["qualified_name"], "a::f2")

    def test_batching_splits_in_order(self):
        src = self.write_input([export_line(f"a::f{i}", f"text {i}") for i in range(5)])
        enc = StubEncoder()
        out = self.dir / "vectors.ndjson"
        hf_embed_job.run(src, out, enc, batch_size=2)
        self.assertEqual([len(c) for c in enc.calls], [2, 2, 1])
        self.assertEqual(enc.inputs, [f"text {i}" for i in range(5)])
        self.assertEqual([r["qualified_name"] for r in self.read_output(out)],
                         [f"a::f{i}" for i in range(5)])

    def test_malformed_json_names_the_line_number(self):
        src = self.write_input([
            export_line("a::f1", "ok"),
            export_line("a::f2", "ok"),
            '{"qualified_name": "a::f3", "text": ',
        ])
        with self.assertRaisesRegex(hf_embed_job.InputError, "line 3"):
            hf_embed_job.run(src, self.dir / "vectors.ndjson", StubEncoder(), batch_size=64)

    def test_missing_text_names_the_line_number(self):
        src = self.write_input([export_line("a::f1", "ok"), json.dumps({"qualified_name": "a::f2"})])
        with self.assertRaisesRegex(hf_embed_job.InputError, "line 2"):
            hf_embed_job.run(src, self.dir / "vectors.ndjson", StubEncoder(), batch_size=64)

    def test_non_finite_vector_is_an_error_not_written(self):
        src = self.write_input([export_line("a::f1", "ok")])
        out = self.dir / "vectors.ndjson"
        with self.assertRaisesRegex(ValueError, "a::f1"):
            hf_embed_job.run(src, out, StubEncoder(vec=[float("nan"), 0.0]), batch_size=64)
        self.assertEqual(out.read_text(encoding="utf-8"), "")


if __name__ == "__main__":
    unittest.main()
