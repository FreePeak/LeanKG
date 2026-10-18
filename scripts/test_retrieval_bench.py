"""Unit tests for scripts/retrieval-bench.py: the pure scoring and parsing parts.

Synthetic fixtures only. Run with:

    python3 -I scripts/test_retrieval_bench.py
"""
import importlib.util
import json
import os
import sqlite3
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


class CandidateTest(unittest.TestCase):
    """Container scopes are not answers: a whole file, module or doc section is never
    the function a developer would name."""

    def test_container_kind_over_200_lines_is_not_a_candidate(self):
        for kind in ("file", "module", "package", "namespace", "class", "doc"):
            with self.subTest(kind=kind):
                self.assertFalse(bench.is_candidate(kind, 1, 300))

    def test_small_container_and_any_size_code_element_stay_candidates(self):
        self.assertTrue(bench.is_candidate("class", 1, 40))
        self.assertTrue(bench.is_candidate("function", 1, 350))
        self.assertTrue(bench.is_candidate("method", 10, 40))

    def test_lines_inside_a_skipped_container_earn_no_credit(self):
        elements = {"Big": ("class", "f.go", 1, 300), "F": ("function", "f.go", 10, 20)}
        matches = {("f.go", 12): {"w1"}, ("f.go", 50): {"w2"}}
        spans = bench.candidate_spans(elements)
        self.assertEqual(sorted(spans), ["F"])
        self.assertEqual(bench.rank_elements(spans, matches), ["F"])


class RankByNameTest(unittest.TestCase):
    def test_question_words_in_the_name_rank_above_body_only_matches(self):
        spans = {"a.go::Rank": ("a.go", 1, 10), "b.go::Other": ("b.go", 1, 10)}
        matches = {("a.go", 5): {"rank"}, ("b.go", 5): {"rank", "fusion", "reciprocal"}}
        self.assertEqual(bench.rank_by_name(spans, matches, ["rank", "fusion", "reciprocal"]),
                         ["a.go::Rank", "b.go::Other"])

    def test_name_ties_break_on_distinct_body_words(self):
        spans = {"a.go::Rank": ("a.go", 1, 10), "b.go::RankAll": ("b.go", 1, 10)}
        matches = {("a.go", 5): {"rank"}, ("b.go", 5): {"rank", "fusion", "reciprocal"}}
        self.assertEqual(bench.rank_by_name(spans, matches, ["rank", "fusion", "reciprocal"]),
                         ["b.go::RankAll", "a.go::Rank"])

    def test_name_match_is_case_insensitive_substring_of_a_name_token(self):
        spans = {"x.go::ParseRRFList": ("x.go", 1, 10)}
        self.assertEqual(bench.rank_by_name(spans, {}, ["rrf"]), ["x.go::ParseRRFList"])
        self.assertEqual(bench.rank_by_name(spans, {}, ["zebra"]), [])


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


class RefillTest(unittest.TestCase):
    """Dropping a label-file hit frees a slot; the slot takes the next rank, and the
    rest of the served order is left exactly as the server gave it."""

    def test_freed_slots_take_the_next_ranks_without_reordering_the_top(self):
        def hit(qn, path="internal/a.go"):
            return {"qualified_name": qn, "file_path": path}
        kept = [hit("A"), hit("B"), hit("C")]
        deeper = [hit("A"), hit("L", "docs/retrieval-label-set.md"), hit("B"), hit("D"), hit("E")]
        out = bench.refill_hits(kept, deeper, 4, {"docs/retrieval-label-set.md"})
        self.assertEqual([h["qualified_name"] for h in out], ["A", "B", "C", "D"])


class LabelFileExclusionTest(unittest.TestCase):
    """The label file contains every question verbatim, so it must never count as a hit."""

    def test_default_label_file_is_always_excluded(self):
        self.assertEqual(bench.label_paths(None, "/repo"), {"docs/retrieval-label-set.md"})

    def test_labels_json_inside_the_project_maps_to_its_repo_relative_path(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "evals"))
            path = os.path.join(d, "evals", "labels.json")
            open(path, "w").close()
            self.assertEqual(bench.label_paths(path, d),
                             {"docs/retrieval-label-set.md", "evals/labels.json"})

    def test_grep_matches_on_the_label_file_are_dropped_and_counted(self):
        matches = {("docs/retrieval-label-set.md", 12): {"cache", "evict"},
                   ("docs/retrieval-label-set.md", 40): {"cache"},
                   ("internal/cache.go", 7): {"cache"}}
        kept, dropped = bench.drop_label_matches(matches, {"docs/retrieval-label-set.md"})
        self.assertEqual(kept, {("internal/cache.go", 7): {"cache"}})
        self.assertEqual(dropped, 2)

    def test_label_file_cannot_win_element_ranking_once_dropped(self):
        spans = {"docs/x.md::Q": ("docs/retrieval-label-set.md", 1, 209),
                 "internal/cache.go::Evict": ("internal/cache.go", 1, 30)}
        matches = {("docs/retrieval-label-set.md", 12): {"cache", "evict"},
                   ("internal/cache.go", 7): {"cache"}}
        kept, _ = bench.drop_label_matches(matches, {"docs/retrieval-label-set.md"})
        self.assertEqual(bench.rank_elements(spans, kept), ["internal/cache.go::Evict"])

    def test_served_hits_from_the_label_file_are_dropped_and_counted(self):
        answer = {"hits": [{"qualified_name": "docs/x.md::Q", "file_path": "docs/retrieval-label-set.md"},
                           {"qualified_name": "internal/a.go::A", "element": {"file_path": "internal/a.go"}},
                           {"qualified_name": "internal/b.go::B", "file_path": "internal/b.go"}]}
        kept, dropped = bench.drop_label_served(answer, {"docs/retrieval-label-set.md"})
        self.assertEqual([h["qualified_name"] for h in kept["hits"]], ["internal/a.go::A", "internal/b.go::B"])
        self.assertEqual(dropped, 1)


def make_store(root, files, elements=(), vectors=0, vectors_table=True):
    """A synthetic <root>/.leankg/leankg.db with the tables the bench reads, and the
    given files written under root (paths in files that are not written stay missing)."""
    os.makedirs(os.path.join(root, ".leankg"), exist_ok=True)
    for path in files:
        full = os.path.join(root, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w") as f:
            f.write("needle here\n")
    con = sqlite3.connect(os.path.join(root, ".leankg", "leankg.db"))
    con.execute("CREATE TABLE code_files (path TEXT)")
    con.executemany("INSERT INTO code_files VALUES (?)", [(p,) for p in files])
    con.execute("CREATE TABLE code_elements (qualified_name TEXT, element_type TEXT, "
                "file_path TEXT, line_start INT, line_end INT)")
    con.executemany("INSERT INTO code_elements VALUES (?,?,?,?,?)", list(elements))
    if vectors_table:
        con.execute("CREATE TABLE embedding_vectors (model_id TEXT, qualified_name TEXT, vec BLOB)")
        con.executemany("INSERT INTO embedding_vectors VALUES ('m', ?, x'00')",
                        [(f"v{i}",) for i in range(vectors)])
    con.commit()
    con.close()


class ZeroVectorStoreTest(unittest.TestCase):
    """A store with no embedding vectors must not abort the run: its labels are
    scored as non-L3 / no-vector. A store that HAS vectors but degrades still aborts."""

    def test_store_vector_rows_counts_rows_and_tolerates_a_missing_table(self):
        with tempfile.TemporaryDirectory() as d:
            make_store(d, [], vectors=0)
            self.assertEqual(bench.store_vector_rows(d), 0)
            make_store(os.path.join(d, "b"), [], vectors=3)
            self.assertEqual(bench.store_vector_rows(os.path.join(d, "b")), 3)
            make_store(os.path.join(d, "c"), [], vectors_table=False)
            self.assertEqual(bench.store_vector_rows(os.path.join(d, "c")), 0)

    def test_degraded_with_no_vectors_is_no_vector_not_an_abort(self):
        ret = {"rung": "L2", "reason": "degraded from L3: no vector collection for model m"}
        self.assertTrue(bench.no_vector_store(ret, 0))

    def test_degraded_with_vectors_still_aborts(self):
        ret = {"rung": "L2", "reason": "degraded from L3: stamp mismatch"}
        self.assertFalse(bench.no_vector_store(ret, 5))

    def test_an_l3_answer_is_never_no_vector(self):
        self.assertFalse(bench.no_vector_store({"rung": "L3", "reason": ""}, 0))


class BinaryNoticeTest(unittest.TestCase):
    """rg prints 'path: binary file matches' with no NUL separator; it is a notice, not a hit."""

    def test_notice_lines_are_skipped_and_counted(self):
        out = ("internal/a.go\x003: needle here\n"
               "assets/blob.bin: binary file matches (found \"\\0\" byte around offset 12)\n"
               "internal/b.go\x0041: needle again\n")
        found, notices = bench.parse_rg(out)
        self.assertEqual(found, [("internal/a.go", 3), ("internal/b.go", 41)])
        self.assertEqual(notices, 1)

    def test_word_hits_survive_a_binary_notice_end_to_end(self):
        with tempfile.TemporaryDirectory() as d:
            make_store(d, ["src/a.go"])
            with open(os.path.join(d, "src", "blob.bin"), "wb") as f:
                f.write(b"needle\x00\x01needle\n")
            con = sqlite3.connect(os.path.join(d, ".leankg", "leankg.db"))
            con.execute("INSERT INTO code_files VALUES ('src/blob.bin')")
            con.commit()
            con.close()
            index = bench.GrepIndex(d)
            self.assertEqual(index._word_hits("needle"), [("src/a.go", 1)])
            self.assertEqual(index.binary_notices, 1)


class MissingSourceTest(unittest.TestCase):
    """Indexed files absent from the checkout must not abort the grep arm."""

    def test_rg_exit_2_from_missing_paths_is_tolerable(self):
        err = "rg: gone.go: No such file or directory (os error 2)\n"
        self.assertTrue(bench.rg_failure_is_tolerable(2, err))
        self.assertTrue(bench.rg_failure_is_tolerable(1, ""))
        self.assertTrue(bench.rg_failure_is_tolerable(0, ""))

    def test_rg_exit_2_for_any_other_error_still_aborts(self):
        self.assertFalse(bench.rg_failure_is_tolerable(2, "regex parse error\n"))
        self.assertFalse(bench.rg_failure_is_tolerable(2, "rg: gone.go: No such file or directory\nregex parse error\n"))

    def test_missing_files_are_dropped_from_the_search_and_counted(self):
        with tempfile.TemporaryDirectory() as d:
            make_store(d, ["present.go", "gone.go"])
            os.remove(os.path.join(d, "gone.go"))
            index = bench.GrepIndex(d)
            self.assertEqual(index.files, ["present.go"])
            self.assertEqual(index.missing, 1)
            self.assertEqual(index._word_hits("needle"), [("present.go", 1)])


class SweepTest(unittest.TestCase):
    def test_sweep_grid_has_five_to_one_and_vector_only(self):
        self.assertIn("5,1", bench.SWEEP_WEIGHTS)
        self.assertIn("1,0", bench.SWEEP_WEIGHTS)

    def test_sweep_progress_line_names_question_and_every_weight(self):
        row = {"num": 7, "sweep": {"1,0": 2, "5,1": None}}
        line = bench.sweep_progress(row)
        self.assertTrue(line.startswith("sweep #7"))
        self.assertIn("1,0=2", line)
        self.assertIn("5,1=-", line)

    def test_dump_records_carry_every_arm_and_weight_rank(self):
        row = {"num": 3, "project": "/p", "question": "how is x?", "truth": "a::X",
               "rung": "L3", "stale": False,
               "served": ["a::X"], "element": ["a::X"], "names": [], "file": ["a.go"],
               "spans": {"a::X": ("a.go", 1, 9)},
               "sweep": {"1,0": 1, "5,1": None}}
        rec = bench.dump_records([row])
        self.assertEqual(rec, [{
            "id": 3, "project": "/p", "question": "how is x?", "label": "a::X",
            "rung": "L3", "stale": False, "scored": True,
            "ranks": {"semantic": 1, "grep_element": 1, "grep_element_names": None,
                      "grep_file": 1},
            "sweep": {"1,0": 1, "5,1": None}}])
        json.dumps(rec)


if __name__ == "__main__":
    unittest.main()
