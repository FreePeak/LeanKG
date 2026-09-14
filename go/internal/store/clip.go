package store

import (
	"strings"
	"unicode/utf8"
)

// ClipUTF8 bounds s to at most maxBytes without splitting a UTF-8 rune.
//
// This exists because the two engines disagree about invalid UTF-8: SQLite
// stores raw bytes and never validates, so a cut through a multi-byte rune is
// invisible there, while PostgreSQL rejects the whole row with
// "invalid byte sequence for encoding \"UTF8\"" (SQLSTATE 22021). The bug was
// found by indexing this repository's own docs into Postgres — an ellipsis
// (U+2026, E2 80 A6) clipped to two bytes.
//
// A backoff that stops on the first RuneStart is NOT sufficient: after
// dropping the continuation bytes it lands on the lone leading byte (0xE2),
// which is itself an incomplete rune. Dropping until the prefix is valid is
// bounded by the rune width (max 3 iterations).
func ClipUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// ValidText forces foreign text to valid UTF-8. SQLite stores bytes without
// validating, so an invalid sequence survives a sqlite write and then fails the
// WHOLE PostgreSQL batch with SQLSTATE 22021 — one bad element aborts hundreds.
// Both backends run text through this at the write boundary, so the engines
// store identical bytes (the golden parity fixtures depend on that) and no
// producer — indexer, summarizer, federation import, remote source — can
// re-introduce the class by being subtly wrong on its own clipping.
func ValidText(s string) string {
	if utf8.ValidString(s) {
		return s // fast path: 99.99% of text is already valid (no copy)
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// sanitize returns a copy of the element with every text column forced to valid
// UTF-8. Applied by both backends' element upsert.
func (e Element) sanitize() Element {
	e.Name = ValidText(e.Name)
	e.QualifiedName = ValidText(e.QualifiedName)
	e.FilePath = ValidText(e.FilePath)
	e.Language = ValidText(e.Language)
	e.ParentQualified = ValidText(e.ParentQualified)
	e.Content = ValidText(e.Content)
	return e
}
