package memory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// fixtureRows: ten rows, timestamps ascending with the index, so the newest
// row is r09. Short rows all match "vector index"; long rows (about 3000
// bytes each) do not contain it and push the first-turn token budget.
func fixtureRows(longBody bool) []Entry {
	var out []Entry
	for i := 0; i < 10; i++ {
		body := fmt.Sprintf("memory row %02d about the vector index", i)
		if longBody {
			body = fmt.Sprintf("row %02d ", i) + strings.Repeat("x", 3000)
		}
		out = append(out, Entry{
			ID:        fmt.Sprintf("r%02d", i),
			Content:   body,
			Timestamp: int64(1000 + i),
		})
	}
	return out
}

// TestFirstTurnGoldenOutput pins FirstTurnMemoriesWithIDs and
// FirstTurnMemories byte for byte, for the cold arm and the ranked arm, short
// and budget-cut rows. The values were captured from the code before
// InjectBanks existed; the refactor must keep them identical.
func TestFirstTurnGoldenOutput(t *testing.T) {
	scope, _ := ParseScope("")
	shortCold := "<memories>\n" +
		"- memory row 09 about the vector index\n" +
		"- memory row 08 about the vector index\n" +
		"- memory row 07 about the vector index\n" +
		"- memory row 06 about the vector index\n" +
		"- memory row 05 about the vector index\n" +
		"- memory row 04 about the vector index\n" +
		"- memory row 03 about the vector index\n" +
		"- memory row 02 about the vector index\n" +
		"\n<!-- 2 more memories hidden by the injection budget of 8 entries / 5000 tokens -->\n" +
		"</memories>"
	shortRanked := "<memories>\n" +
		"- memory row 09 about the vector index\n" +
		"- memory row 08 about the vector index\n" +
		"- memory row 07 about the vector index\n" +
		"- memory row 06 about the vector index\n" +
		"- memory row 05 about the vector index\n" +
		"- memory row 04 about the vector index\n" +
		"- memory row 03 about the vector index\n" +
		"- memory row 02 about the vector index\n" +
		"</memories>"
	var longCold strings.Builder
	longCold.WriteString("<memories>\n")
	for i := 9; i >= 4; i-- {
		fmt.Fprintf(&longCold, "- row %02d %s\n", i, strings.Repeat("x", 3000))
	}
	longCold.WriteString("\n<!-- 4 more memories hidden by the injection budget of 8 entries / 5000 tokens -->\n</memories>")

	for _, c := range []struct {
		name     string
		long     bool
		query    string
		wantText string
		wantIDs  []string
		wantAll  []string
		total    int
	}{
		{"cold-short", false, "", shortCold,
			[]string{"r09", "r08", "r07", "r06", "r05", "r04", "r03", "r02"},
			[]string{"r09", "r08", "r07", "r06", "r05", "r04", "r03", "r02"}, 10},
		{"ranked-short", false, "vector index", shortRanked,
			[]string{"r09", "r08", "r07", "r06", "r05", "r04", "r03", "r02"},
			[]string{"r09", "r08", "r07", "r06", "r05", "r04", "r03", "r02"}, 8},
		{"cold-long", true, "", longCold.String(),
			[]string{"r09", "r08", "r07", "r06", "r05", "r04"},
			[]string{"r09", "r08", "r07", "r06", "r05", "r04", "r03", "r02"}, 10},
		{"ranked-long-no-match", true, "vector index", "",
			nil, nil, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := openTest(t)
			if err := m.RetainRaw(context.Background(), "probe", fixtureRows(c.long)); err != nil {
				t.Fatal(err)
			}
			text, ids, total, err := m.FirstTurnMemoriesWithIDs(context.Background(), scope, "", "probe", c.query)
			if err != nil {
				t.Fatal(err)
			}
			if text != c.wantText {
				t.Errorf("WithIDs text mismatch:\n got len %d\nwant len %d", len(text), len(c.wantText))
			}
			if !reflect.DeepEqual(ids, c.wantIDs) || total != c.total {
				t.Errorf("WithIDs ids=%v total=%d, want ids=%v total=%d", ids, total, c.wantIDs, c.total)
			}
			text2, all, err := m.FirstTurnMemories(context.Background(), scope, "", "probe", c.query)
			if err != nil {
				t.Fatal(err)
			}
			if text2 != c.wantText {
				t.Errorf("FirstTurnMemories text mismatch:\n got len %d\nwant len %d", len(text2), len(c.wantText))
			}
			var allIDs []string
			for _, e := range all {
				allIDs = append(allIDs, e.ID)
			}
			if !reflect.DeepEqual(allIDs, c.wantAll) {
				t.Errorf("FirstTurnMemories rows=%v, want %v", allIDs, c.wantAll)
			}
		})
	}
}

func mustRetain(t *testing.T, m *Memory, bank string, rows []Entry) {
	t.Helper()
	if err := m.RetainRaw(context.Background(), bank, rows); err != nil {
		t.Fatal(err)
	}
}

func rowIDs(rows []Entry) []string {
	var ids []string
	for _, e := range rows {
		ids = append(ids, e.ID)
	}
	return ids
}

// TestInjectBanksColdFilterBeforeLimit: with no query the filter runs before
// the limit, so the block carries the newest ACCEPTED rows, and the rows the
// filter rejects are neither rendered nor counted.
func TestInjectBanksColdFilterBeforeLimit(t *testing.T) {
	m := openTest(t)
	mustRetain(t, m, "probe", fixtureRows(false))
	even := map[string]bool{"r00": true, "r02": true, "r04": true, "r06": true, "r08": true}

	text, rows, injected, total, err := m.InjectBanks(context.Background(), []string{"probe"}, "", 3, 0,
		func(e Entry) bool { return even[e.ID] })
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"r08", "r06", "r04"}; !reflect.DeepEqual(rowIDs(rows), want) || injected != 3 {
		t.Fatalf("rows=%v injected=%d, want rows=%v injected=3", rowIDs(rows), injected, want)
	}
	if total != 5 {
		t.Fatalf("total = %d, want the 5 accepted rows (rejected rows are not counted)", total)
	}
	for _, rejected := range []string{"row 09", "row 07", "row 05"} {
		if strings.Contains(text, rejected) {
			t.Errorf("rejected row %q rendered:\n%s", rejected, text)
		}
	}
	if !strings.Contains(text, "2 more memories hidden") {
		t.Errorf("the 2 accepted rows cut by the limit are not named:\n%s", text)
	}
}

// TestInjectBanksRankedFilterBeforeLimit: with a query the filter runs before
// the ranked limit. The two best-ranked rows are rejected, so a filter applied
// after the limit would return nothing; the filter-first result is two rows.
func TestInjectBanksRankedFilterBeforeLimit(t *testing.T) {
	m := openTest(t)
	mustRetain(t, m, "probe", fixtureRows(false))
	reject := map[string]bool{"r08": true, "r09": true}

	text, rows, injected, _, err := m.InjectBanks(context.Background(), []string{"probe"}, "vector index", 2, 0,
		func(e Entry) bool { return !reject[e.ID] })
	if err != nil {
		t.Fatal(err)
	}
	if injected != 2 || len(rows) != 2 {
		t.Fatalf("injected=%d rows=%v, want two accepted rows", injected, rowIDs(rows))
	}
	for _, e := range rows {
		if reject[e.ID] {
			t.Errorf("rejected row %s was returned", e.ID)
		}
	}
	if strings.Contains(text, "row 08") || strings.Contains(text, "row 09") {
		t.Errorf("rejected row rendered:\n%s", text)
	}
}

// TestInjectBanksRejectedRowsNeverRendered: a row the filter rejects does
// not reach the block, and total excludes it.
func TestInjectBanksRejectedRowsNeverRendered(t *testing.T) {
	m := openTest(t)
	mustRetain(t, m, "probe", []Entry{
		{ID: "ok-1", Content: "keep this note", Timestamp: 1},
		{ID: "bad-1", Content: "SECRET project note", Timestamp: 2},
		{ID: "ok-2", Content: "keep that note", Timestamp: 3},
	})

	text, rows, injected, total, err := m.InjectBanks(context.Background(), []string{"probe"}, "", 8, 0,
		func(e Entry) bool { return !strings.Contains(e.Content, "SECRET") })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "SECRET") {
		t.Errorf("rejected content rendered:\n%s", text)
	}
	if total != 2 || injected != 2 || !reflect.DeepEqual(rowIDs(rows), []string{"ok-2", "ok-1"}) {
		t.Fatalf("total=%d injected=%d rows=%v, want 2, 2, [ok-2 ok-1]", total, injected, rowIDs(rows))
	}
}

// TestInjectBanksRecordsOneInjectEvent: one call records exactly one inject
// event carrying the injected rows.
func TestInjectBanksRecordsOneInjectEvent(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	mustRetain(t, m, "probe", fixtureRows(false))

	_, _, injected, _, err := m.InjectBanks(identityCtx(), []string{"probe"}, "", 3, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	evs := verbEvents(rec.memEvents(), "inject")
	if len(evs) != 1 || evs[0].Returned != injected || evs[0].Returned != 3 {
		t.Fatalf("inject events = %+v, want one event with Returned=3", evs)
	}
}

// TestInjectBanksCancelledContext returns the context error and records
// nothing.
func TestInjectBanksCancelledContext(t *testing.T) {
	rec := useRecorder(t, telemetry.Metadata)
	m := openTest(t)
	mustRetain(t, m, "probe", fixtureRows(false))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, _, err := m.InjectBanks(ctx, []string{"probe"}, "", 3, 0, nil); err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if evs := verbEvents(rec.memEvents(), "inject"); len(evs) != 0 {
		t.Fatalf("cancelled inject recorded events: %+v", evs)
	}
}
