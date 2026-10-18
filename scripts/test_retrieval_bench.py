"""Unit tests for scripts/retrieval-bench.py: the pure scoring and parsing parts.

Synthetic fixtures only. Run with:

    python3 -I -m unittest scripts/test_retrieval_bench.py -v
"""
import importlib.util
import json
import os
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location(
    "retrieval_bench", os.path.join(HERE, "retrieval-bench.py"))
bench = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bench)


class ServeArgvTest(unittest.TestCase):
    def test_served_store_is_opened_read_only(self):
        # Without --read-only, serve opens the store RW, runs Migrate and may
        # auto-index: a bench that only queries must never write the store.
        argv = bench.serve_argv("/bin/leankg", "/repo")
        self.assertIn("--read-only", argv)
        # --memory opens <project>/.leankg/memory/index.db read-write, the same
        # file the live server holds: the bench must not touch it.
        self.assertNotIn("--memory", argv)
        self.assertEqual(argv[:2], ["/bin/leankg", "serve"])


class ArmsOfTest(unittest.TestCase):
    def test_reads_every_arm_rank_key(self):
        answer = {"hits": [
            {"qualified_name": "p::H1", "ranks": {"vector": 2, "fts5": 1, "name": 3}},
            {"qualified_name": "p::H2", "ranks": {"vector": 1, "fts5": 2}},
        ]}
        vec, kw, name = bench.arms_of(answer, 10)
        self.assertEqual(vec, ["p::H2", "p::H1"])
        self.assertEqual(kw, ["p::H1", "p::H2"])
        self.assertEqual(name, ["p::H1"])

    def test_every_keyword_arm_key_feeds_the_keyword_list(self):
        for arm in ("fts5", "tsvector", "trigram"):
            with self.subTest(arm=arm):
                answer = {"hits": [{"qualified_name": "p::K", "ranks": {arm: 1}}]}
                _, kw, _ = bench.arms_of(answer, 10)
                self.assertEqual(kw, ["p::K"])

    def test_pre_wave1_engine_without_ranks_is_keyword_only(self):
        answer = {"hits": [{"qualified_name": "p::A"}, {"qualified_name": "p::B"}]}
        vec, kw, name = bench.arms_of(answer, 10)
        self.assertEqual(vec, [])
        self.assertEqual(kw, ["p::A", "p::B"])
        self.assertEqual(name, [])


class ScoreTest(unittest.TestCase):
    def test_match_is_exact_qualified_name_not_substring(self):
        self.assertIsNone(bench.score(["a::X.Open"], "a::Open"))
        # The substring defect: 'a::Open' is inside 'pkg/b::a::Open'.
        self.assertIsNone(bench.score(["pkg/b::a::Open"], "a::Open"))

    def test_exact_match_returns_one_based_rank(self):
        self.assertEqual(bench.score(["a::Open", "b::Open"], "a::Open"), 1)
        self.assertEqual(bench.score(["b::x", "a::Open"], "a::Open"), 2)


class ScoreFileTest(unittest.TestCase):
    def test_file_ranking_is_scored_at_the_label_file(self):
        spans = {"a.go::Open": ("a.go", 1, 9), "b.go::Close": ("b.go", 2, 4)}
        self.assertEqual(bench.score_file(["b.go", "a.go"], "a.go::Open", spans), 2)
        self.assertIsNone(bench.score_file(["b.go"], "a.go::Open", spans))


class EvaluateTest(unittest.TestCase):
    def test_denominator_is_scored_labels_and_stale_is_separate(self):
        e = bench.evaluate([1, 4, None], stale=1)
        self.assertEqual(e["n"], 3)
        self.assertEqual(e["stale"], 1)
        self.assertAlmostEqual(e["hits"][5] / e["n"], 2 / 3)
        self.assertEqual(e["hits"][1], 1)
        self.assertEqual(e["hits"][10], 2)
        self.assertAlmostEqual(e["mrr10"], (1 + 1 / 4) / 3)

    def test_empty_denominator_does_not_divide_by_zero(self):
        e = bench.evaluate([], stale=2)
        self.assertEqual((e["n"], e["stale"], e["mrr10"]), (0, 2, 0.0))


class RankElementsTest(unittest.TestCase):
    def test_distinct_words_then_innermost_span(self):
        spans = {"A": ("f.go", 10, 20), "B": ("f.go", 12, 15)}
        matches = {("f.go", 13): {"w1"}, ("f.go", 14): {"w2"}, ("f.go", 18): {"w1"}}
        self.assertEqual(bench.rank_elements(spans, matches), ["B", "A"])

    def test_element_without_a_matching_line_is_not_ranked(self):
        spans = {"A": ("f.go", 10, 20), "C": ("g.go", 1, 5)}
        self.assertEqual(bench.rank_elements(spans, {("f.go", 12): {"w1"}}), ["A"])


class ContentWordsTest(unittest.TestCase):
    def test_stopwords_short_words_and_duplicates_are_dropped(self):
        q = "How does the store take a lock, and does it lock?"
        self.assertEqual(bench.content_words(q), ["store", "take", "lock"])


class LoadLabelsTest(unittest.TestCase):
    def test_json_row_may_carry_its_own_project(self):
        rows = [["a::X", "first question"], ["b::Y", "second question", "/some/other"]]
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "labels.json")
            with open(path, "w") as f:
                json.dump(rows, f)
            labels = bench.load_labels(path)
        self.assertEqual([(l.num, l.truth, l.project) for l in labels],
                         [(1, "a::X", None), (2, "b::Y", "/some/other")])


if __name__ == "__main__":
    unittest.main()
