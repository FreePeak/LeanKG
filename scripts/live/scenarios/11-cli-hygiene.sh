# Scenario 11 (issue #592): CLI hygiene.
#   (a) `leankg delete-annotation` parsed its flags twice, so a bad flag or -h
#       printed the usage twice. Each must print it exactly once.
#   (b) `leankg-embed` took LEANKG_PG_URL straight from the environment and
#       ignored db.url in leankg.yaml. With LEANKG_DB_ENGINE=postgres, no
#       LEANKG_PG_URL and a leankg.yaml db.url on a closed port, it must try that
#       URL and fail with a connection error, not "requires LEANKG_PG_URL".
# No PostgreSQL is started; the closed port is enough.
# The rest of the #592 patch (telemetry writerOnce cleanup, archtest repo-hygiene
# test) has no CLI-observable behaviour and is left to `go test`.
scenario_main() {
  local emb_bin dir="$WORK/s11/pg" port

  run_cmd annot-bad-flag "$LEANKG_BIN" delete-annotation --no-such-flag
  assert_eq "delete-annotation with a bad flag exits 2" 2 "$RC"
  assert_eq "delete-annotation with a bad flag prints its usage exactly once" 1 \
    "$(usage_count "$OUT" "$ERR")"

  run_cmd annot-help "$LEANKG_BIN" delete-annotation -h
  assert_eq "delete-annotation -h exits 0" 0 "$RC"
  assert_eq "delete-annotation -h prints its usage exactly once" 1 \
    "$(usage_count "$OUT" "$ERR")"

  emb_bin="$(dirname "$LEANKG_BIN")/leankg-embed"
  if [ ! -x "$emb_bin" ]; then
    skip "leankg-embed is not beside $LEANKG_BIN; pass --bin with a path whose directory holds leankg-embed"
    return 0
  fi
  port=$(free_port)
  mkdir -p "$dir"
  printf 'db:\n  url: postgresql://embed-test@127.0.0.1:%s/leankg\n' "$port" >"$dir/leankg.yaml"
  # The deterministic provider needs no model and no network, so the run gets as far as the DB.
  run_cmd embed-pg-url-from-yaml env LEANKG_DB_ENGINE=postgres LEANKG_EMBED_PROVIDER=deterministic \
    "$emb_bin" run --project "$dir"
  assert_eq "leankg-embed on postgres with a closed db.url port exits 1" 1 "$RC"
  assert_eq "leankg-embed does not demand LEANKG_PG_URL when leankg.yaml has db.url" 0 \
    "$(printf '%s' "$ERR" | grep -c 'requires LEANKG_PG_URL')"
  assert_contains "leankg-embed reports a connection failure for the yaml URL" "connect" "$ERR"
}

# usage_count OUT ERR: how many times the delete-annotation usage header appears.
usage_count() {
  printf '%s%s' "$1" "$2" | grep -c 'Usage of delete-annotation'
}
