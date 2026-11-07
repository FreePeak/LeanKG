package githistory

import (
	"context"
	"testing"
	"time"
)

func TestHistoryReturnsCommitsTouchingFile(t *testing.T) {
	root := repoWithCommits(t)
	st := openStore(t)
	mustImport(t, st, root)

	got, err := History(st, "a.go", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("History(a.go) = %d entries, want 2", len(got))
	}
	subjects := map[string]bool{}
	for _, c := range got {
		subjects[c.Subject] = true
		if c.Author != "t" || c.Date == "" || c.SHA == "" {
			t.Fatalf("entry missing fields: %+v", c)
		}
	}
	if !subjects["first"] || !subjects["second"] {
		t.Fatalf("subjects = %v, want first and second", subjects)
	}
	if capped, _ := History(st, "a.go", 1); len(capped) != 1 {
		t.Fatalf("limit 1 returned %d entries", len(capped))
	}
	if none, _ := History(st, "missing.go", 10); len(none) != 0 {
		t.Fatalf("History(missing.go) = %d entries, want 0", len(none))
	}
}

func TestCoChangeRanksPartnersByCount(t *testing.T) {
	root := repoWithCommits(t)
	st := openStore(t)
	mustImport(t, st, root)

	got, err := CoChange(st, "a.go", 10)
	if err != nil {
		t.Fatalf("CoChange: %v", err)
	}
	if len(got) != 1 || got[0].Path != "b.go" || got[0].Count != 1 {
		t.Fatalf("CoChange(a.go) = %+v, want [b.go x1]", got)
	}
}

func TestChurnCountsCommitsPerFileWithinWindow(t *testing.T) {
	root := repoWithCommits(t)
	got, err := Churn(context.Background(), root, ChurnOptions{Limit: 10})
	if err != nil {
		t.Fatalf("Churn: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Churn = %+v, want a.go and b.go", got)
	}
	if got[0].Path != "a.go" || got[0].Commits != 2 {
		t.Fatalf("top churn = %+v, want a.go with 2 commits", got[0])
	}
	if got[1].Path != "b.go" || got[1].Commits != 1 {
		t.Fatalf("second churn = %+v, want b.go with 1 commit", got[1])
	}
	ahead := time.Now().Add(time.Hour).Format(time.RFC3339)
	future, _ := Churn(context.Background(), root, ChurnOptions{Limit: 10, Since: ahead})
	if len(future) != 0 {
		t.Fatalf("Churn since a future date = %+v, want none", future)
	}
	limited, _ := Churn(context.Background(), root, ChurnOptions{Limit: 1})
	if len(limited) != 1 || limited[0].Path != "a.go" {
		t.Fatalf("Churn limit 1 = %+v", limited)
	}
}
