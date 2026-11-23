# Scenario 03: regression for #598. After an incremental re-index that edits
# a.go, the cross-file edges survive and match a fresh full index of the same tree.
# Elements are addressed by qualified name: a bare name such as AlphaTwo is an
# ambiguous L1 match (see scenario 07), which is not what this scenario tests.
scenario_main() {
  local inc="$WORK/s03/incremental" fresh="$WORK/s03/fresh" qn
  write_fixture "$inc"

  run_cmd index-1 "$LEANKG_BIN" index "$inc" --no-embed
  assert_eq "first index exits 0" 0 "$RC"
  assert_cross_file_callees "before-edit" "$inc"

  # Comment-only edit of a.go, the same edit the regression test makes.
  fixture_file a.go | sed 's/drives the cross-file calls\./drives the cross-file calls, reworded./' >"$inc/a.go"
  run_cmd index-2 "$LEANKG_BIN" index "$inc" --no-embed
  assert_eq "incremental re-index exits 0" 0 "$RC"
  assert_cross_file_callees "after-edit" "$inc"

  write_fixture "$fresh"
  fixture_file a.go | sed 's/drives the cross-file calls\./drives the cross-file calls, reworded./' >"$fresh/a.go"
  run_cmd index-fresh "$LEANKG_BIN" index "$fresh" --no-embed
  assert_eq "fresh full index exits 0" 0 "$RC"

  for qn in a.go::Alpha a.go::AlphaTwo b.go::betaHelper b.go::alphaTwo c.go::gammaHelper; do
    run_cmd "inc-callees-$qn" "$LEANKG_BIN" query --action callees "$qn" --project "$inc"
    local inc_callees
    inc_callees=$(printf '%s' "$OUT" | jq -c '.callees | sort')
    run_cmd "fresh-callees-$qn" "$LEANKG_BIN" query --action callees "$qn" --project "$fresh"
    assert_eq "callees of $qn match a fresh full index" \
      "$(printf '%s' "$OUT" | jq -c '.callees | sort')" "$inc_callees"
  done

  run_cmd inc-status "$LEANKG_BIN" status --project "$inc"
  local inc_rels
  inc_rels=$(printf '%s' "$OUT" | jq -r '.relationships')
  run_cmd fresh-status "$LEANKG_BIN" status --project "$fresh"
  assert_eq "relationship count matches a fresh full index" \
    "$(printf '%s' "$OUT" | jq -r '.relationships')" "$inc_rels"
}
