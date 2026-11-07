package githistory

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/gitobj"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Change is one commit that touched a file, as recorded on its modified_in
// edge.
type Change struct {
	SHA        string `json:"sha"`
	Date       string `json:"date"`
	Author     string `json:"author"`
	Subject    string `json:"subject"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// Partner is a file that changed in the same commits as the queried file.
type Partner struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// ChurnEntry is one file with the number of commits that touched it in the
// window.
type ChurnEntry struct {
	Path       string `json:"path"`
	Commits    int    `json:"commits"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// ChurnOptions bounds a Churn read. Limit <= 0 means 20. Since is a git date.
// MaxCommits <= 0 means DefaultMaxCommits.
type ChurnOptions struct {
	Limit      int
	Since      string
	MaxCommits int
}

// History lists the commits that touched path, newest first, from the stored
// modified_in edges. limit <= 0 means no limit.
func History(st store.Backend, path string, limit int) ([]Change, error) {
	rels, err := st.Outgoing("git:file:" + path)
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, r := range rels {
		if r.RelType != "modified_in" {
			continue
		}
		out = append(out, Change{
			SHA:        metaString(r.Metadata, "sha"),
			Date:       metaString(r.Metadata, "date"),
			Author:     metaString(r.Metadata, "author"),
			Subject:    metaString(r.Metadata, "subject"),
			Insertions: metaInt(r.Metadata, "insertions"),
			Deletions:  metaInt(r.Metadata, "deletions"),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return newer(out[i].Date, out[j].Date) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// CoChange lists the files that changed in the same commits as path, most
// frequent first, from the stored co_changed_with edges. limit <= 0 means no
// limit.
func CoChange(st store.Backend, path string, limit int) ([]Partner, error) {
	rels, err := st.Outgoing("git:file:" + path)
	if err != nil {
		return nil, err
	}
	var out []Partner
	for _, r := range rels {
		if r.RelType != "co_changed_with" {
			continue
		}
		out = append(out, Partner{Path: strings.TrimPrefix(r.Target, "git:file:"), Count: metaInt(r.Metadata, "count")})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Churn ranks files by the number of commits that touched them. It reads git
// directly over the bounded window, so it needs no import and honours Since
// as a git date.
func Churn(ctx context.Context, root string, opts ChurnOptions) ([]ChurnEntry, error) {
	max := opts.MaxCommits
	if max <= 0 {
		max = DefaultMaxCommits
	}
	commits, err := gitobj.History(ctx, root, gitobj.HistoryOptions{Max: max, After: opts.Since})
	if err != nil {
		return nil, err
	}
	byPath := map[string]*ChurnEntry{}
	for _, c := range commits {
		for _, f := range c.Files {
			e := byPath[f.Path]
			if e == nil {
				e = &ChurnEntry{Path: f.Path}
				byPath[f.Path] = e
			}
			e.Commits++
			e.Insertions += f.Insertions
			e.Deletions += f.Deletions
		}
	}
	out := make([]ChurnEntry, 0, len(byPath))
	for _, e := range byPath {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Commits != out[j].Commits {
			return out[i].Commits > out[j].Commits
		}
		return out[i].Path < out[j].Path
	})
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func metaString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func metaInt(m map[string]any, key string) int {
	f, _ := m[key].(float64)
	return int(f)
}

// newer orders two ISO 8601 dates, newest first; an unparsable date sorts last.
func newer(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA != nil || errB != nil {
		return errB != nil && errA == nil
	}
	return ta.After(tb)
}
