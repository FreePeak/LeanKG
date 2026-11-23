# Scenario 07: edge precision regression checks for three product bugs, fixed by
# extractor v5 (index.go extractorVersion):
#
#   Bug A, false call edge. a.go's trailing comment "// AlphaTwo calls into c.go."
#   belongs to AlphaTwo, but it sat inside Alpha's span (extract.go ended an element
#   one line before the next declaration), so it created an Alpha -> AlphaTwo edge.
#   Bug B, span. Alpha's line_end was 9; its closing brace is a.go:7.
#   Bug C, unstable L1 resolution. A plain "AlphaTwo" tied a.go::AlphaTwo with
#   b.go::alphaTwo on length, so the answer depended on the store's write history.
#   FindExact now ranks an exact-case match first, then path ascending.
scenario_main() {
  local dir="$WORK/s07/fixture" inc="$WORK/s07/incremental"

  write_fixture "$dir"
  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index exits 0" 0 "$RC"

  run_cmd callees-Alpha "$LEANKG_BIN" query --action callees Alpha --project "$dir"
  assert_json "Alpha does not call AlphaTwo (a.go:4-7 never names it)" \
    '.callees | any(. == "a.go::AlphaTwo") | not' true "$OUT"

  run_cmd element-Alpha "$LEANKG_BIN" query Alpha --project "$dir"
  assert_json "Alpha line_end is its closing brace (a.go:7)" \
    '.[0].line_end' 7 "$OUT"

  run_cmd fresh-AlphaTwo "$LEANKG_BIN" query AlphaTwo --project "$dir"
  assert_json "fresh store: plain AlphaTwo resolves to a.go::AlphaTwo" \
    '.[0].qualified_name' a.go::AlphaTwo "$OUT"

  # Same tree reached by an incremental re-index: the plain-name resolution moves.
  write_fixture "$inc"
  run_cmd inc-index-1 "$LEANKG_BIN" index "$inc" --no-embed
  fixture_file a.go | sed 's/drives the cross-file calls\./drives the cross-file calls, reworded./' >"$inc/a.go"
  run_cmd inc-index-2 "$LEANKG_BIN" index "$inc" --no-embed
  assert_eq "incremental re-index exits 0" 0 "$RC"
  run_cmd inc-AlphaTwo "$LEANKG_BIN" query AlphaTwo --project "$inc"
  assert_json "incremental store: plain AlphaTwo resolves to a.go::AlphaTwo" \
    '.[0].qualified_name' a.go::AlphaTwo "$OUT"
}
