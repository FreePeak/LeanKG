# Scenario 02: index a fresh fixture with no embeddings; every element is found
# in its file, and the cross-file call edges exist in both directions.
scenario_main() {
  local dir="$WORK/s02/fixture" pair name file
  write_fixture "$dir"

  run_cmd index "$LEANKG_BIN" index "$dir" --no-embed
  assert_eq "index exits 0" 0 "$RC"
  assert_contains "index reports 3 files and 5 elements" "files=3 elements=5" "$OUT"

  run_cmd status "$LEANKG_BIN" status --project "$dir"
  assert_json "status counts 5 elements" '.elements' 5 "$OUT"
  assert_json "status counts 3 files" '.files' 3 "$OUT"

  for pair in Alpha:a.go AlphaTwo:a.go betaHelper:b.go alphaTwo:b.go gammaHelper:c.go; do
    name=${pair%%:*}
    file=${pair#*:}
    run_cmd "element-$name" "$LEANKG_BIN" query "$name" --project "$dir"
    assert_json "element $name is indexed in $file" \
      "[.[] | select(.name == \"$name\") | .file_path] | join(\",\")" "$file" "$OUT"
  done

  assert_cross_file_callees "fresh" "$dir"

  run_cmd callers-gammaHelper "$LEANKG_BIN" query --action callers gammaHelper --project "$dir"
  assert_json "[fresh] gammaHelper is called from a.go::Alpha" \
    '.callers | any(. == "a.go::Alpha")' true "$OUT"
}
