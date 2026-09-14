package summarize

import (
	"context"
	"os"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// AfterIndexEnv is the opt-in switch of the post-index hook:
//
//	LEANKG_SUMMARIZE_AFTER_INDEX=1   run the meaning pipeline after `leankg index`
//	                                 (default: unset = OFF)
//
// The default is OFF on purpose. Summarizing spends tokens, and the
// lazy-activation contract says an idle engine does no work — indexing a repo
// must never quietly start calling an LLM just because someone configured one
// for the query tier. The explicit `leankg summarize` verb needs no switch:
// typing the command IS the opt-in.
const AfterIndexEnv = "LEANKG_SUMMARIZE_AFTER_INDEX"

// AfterIndexEnabled reports whether the post-index hook is opted in.
func AfterIndexEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AfterIndexEnv))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// RunAfterIndex is the post-index seam an index command calls after its own
// pass completes. ran reports whether the pipeline actually executed, so a
// caller can stay silent when nobody asked for the meaning tier.
//
// A returned error means the tier could not run at all (no LEANKG_LLM_MODEL,
// an unreadable store). Callers should report it and still exit on the INDEX
// result: the meaning tier is optional enrichment, and the index it follows
// already succeeded. An incomplete-but-started run reports through
// Result.Fatal instead (that one is not an error, and it is not green either).
func RunAfterIndex(ctx context.Context, st store.Backend, dir string) (Result, bool, error) {
	if !AfterIndexEnabled() {
		return Result{}, false, nil
	}
	res, err := Run(ctx, st, Options{ProjectDir: dir})
	return res, true, err
}
