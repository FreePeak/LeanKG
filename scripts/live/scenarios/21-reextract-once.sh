# Scenario 21: the extractor-version bump (v5) re-extracts a pre-fix store once.
# A store written by the pre-fix binary holds the false Alpha -> AlphaTwo calls
# edge (a.go's trailing comment) and the wrong span (a.go line_end 9). The binary
# under test's first incremental `leankg index` on that store, with no manual
# re-index and no file changed, re-extracts every file and corrects both. A second
# incremental run re-extracts nothing.
#
# The pre-fix binary is LIVE_SEED_BIN. It defaults to the binary under test, so a
# run against the baseline binary shows the [fixed] assertions failing there.
scenario_main() {
  local dir="$WORK/s21/fixture" seed="$WORK/s21/seed-leankg" seed_src files
  seed_src=${LIVE_SEED_BIN:-$LEANKG_BIN}
  if [ ! -x "$seed_src" ]; then
    skip "LIVE_SEED_BIN is not an executable file"
    return 0
  fi
  mkdir -p "$WORK/s21"
  cp "$seed_src" "$seed"
  write_fixture "$dir"

  # Seed: the pre-fix binary indexes the fixture, and its store shows the bugs.
  run_cmd seed-index "$seed" index "$dir" --no-embed
  assert_eq "[guard] pre-fix binary indexes the fixture with exit 0" 0 "$RC"
  run_cmd seed-callees "$seed" query --action callees Alpha --project "$dir"
  assert_json "[guard] the pre-fix store carries the false edge Alpha -> AlphaTwo" \
    '.callees | any(. == "a.go::AlphaTwo")' true "$OUT"
  run_cmd seed-element "$seed" query Alpha --project "$dir"
  assert_json "[guard] the pre-fix store carries the wrong span (line_end 9)" \
    '.[0].line_end' 9 "$OUT"

  # Upgrade: the binary under test, incremental, on the same store, no file edits.
  run_cmd upgrade-index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "[guard] first incremental index with the binary under test exits 0" 0 "$RC"
  files=$(printf '%s' "$OUT" | sed -n 's/.*files=\([0-9]*\).*/\1/p')
  assert_eq "[fixed] the version bump re-extracts all 3 fixture files once" 3 "$files"

  run_cmd upgrade-callees "$LEANKG_BIN" query --action callees Alpha --project "$dir"
  assert_json "[fixed] after the upgrade Alpha has no edge to AlphaTwo" \
    '.callees | any(. == "a.go::AlphaTwo") | not' true "$OUT"
  assert_json "[guard] after the upgrade the real call betaHelper survives" \
    '.callees | any(. == "b.go::betaHelper")' true "$OUT"
  assert_json "[guard] after the upgrade the real call gammaHelper survives" \
    '.callees | any(. == "c.go::gammaHelper")' true "$OUT"
  run_cmd upgrade-element "$LEANKG_BIN" query Alpha --project "$dir"
  assert_json "[fixed] after the upgrade Alpha line_end is its closing brace (a.go:7)" \
    '.[0].line_end' 7 "$OUT"

  # Second incremental run: the version now matches, so nothing is re-extracted.
  run_cmd rerun-index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "[guard] second incremental index exits 0" 0 "$RC"
  files=$(printf '%s' "$OUT" | sed -n 's/.*files=\([0-9]*\).*/\1/p')
  assert_eq "[guard] second incremental run re-extracts no file (files=0)" 0 "$files"
  run_cmd rerun-element "$LEANKG_BIN" query Alpha --project "$dir"
  assert_json "[fixed] the second run keeps Alpha's corrected span (line_end 7)" \
    '.[0].line_end' 7 "$OUT"
}
