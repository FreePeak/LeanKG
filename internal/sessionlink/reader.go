package sessionlink

import (
	"context"
	"errors"
)

// maxMemo bounds how many parsed transcripts one Reader keeps. Each entry can
// hold a whole transcript, so an unbounded memo grows with every file a pass
// touches.
const maxMemo = 8

// Reader memoizes parsed transcripts for one Link pass. Every call in the pass
// asks the same transcripts for its candidates (Locate) and its window
// (Window), so each file is parsed once and reused. A nil *Reader parses on
// every call (Transcript, one-off callers).
type Reader struct {
	parsed map[string]any
	order  []string // keys in insertion order, oldest first, for eviction
}

// NewReader returns an empty Reader for one Link pass.
func NewReader() *Reader { return &Reader{parsed: map[string]any{}} }

// keep stores m for key and evicts the oldest keys beyond maxMemo.
func (r *Reader) keep(key string, m any) {
	if _, ok := r.parsed[key]; !ok {
		r.order = append(r.order, key)
	}
	r.parsed[key] = m
	for len(r.order) > maxMemo {
		delete(r.parsed, r.order[0])
		r.order = r.order[1:]
	}
}

// memo keeps a parse result, including its error, for one key.
type memo[T any] struct {
	v   T
	err error
}

// Parsed returns what parse produced for key, calling parse at most once per
// key for the life of r. key is the transcript path, or a path plus session id
// when one store holds several sessions. A context error is not kept, so a
// cancelled pass does not poison a later one.
func Parsed[T any](r *Reader, key string, parse func() (T, error)) (T, error) {
	if r == nil {
		return parse()
	}
	if m, ok := r.parsed[key].(memo[T]); ok {
		return m.v, m.err
	}
	v, err := parse()
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		r.keep(key, memo[T]{v: v, err: err})
	}
	return v, err
}
