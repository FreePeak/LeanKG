// Package golden holds the W0 parity fixtures: golden JSON files pinning the
// 3-tool envelope wire shapes (status, query ladder L0-L3, impact, memory
// snapshot) captured from a deterministic fixture store. The W7 cutover
// diffs the Rust engine's responses against these files after the same
// redaction (paths, timestamps, watermark seq/at).
//
// The goldens live in go/testdata/golden/*.json; regenerate with
//
//	go test ./internal/golden/ -run Golden -update
package golden
