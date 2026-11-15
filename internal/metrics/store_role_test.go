package metrics

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// ledgerOnly implements exactly the store.Metrics role that metrics uses.
// Passing it proves the dependency is narrowed: it would not compile against
// store.Backend.
type ledgerOnly struct{ reset int64 }

func (l *ledgerOnly) RecordMetric(context.Context, store.Metric) error { return nil }

func (l *ledgerOnly) MetricsSummary(context.Context, string, int) (store.MetricSummary, error) {
	return store.MetricSummary{}, nil
}

func (l *ledgerOnly) UsageAggregates(context.Context, int64) (store.UsageAggregates, error) {
	return store.UsageAggregates{}, nil
}

func (l *ledgerOnly) CleanupMetrics(context.Context, int) (int64, error) { return 0, nil }

func (l *ledgerOnly) ResetMetrics(context.Context) (int64, error) { return l.reset, nil }

func TestShowResetOverMetricsRole(t *testing.T) {
	out, err := Show(context.Background(), &ledgerOnly{reset: 3}, Options{Reset: true})
	if err != nil {
		t.Fatalf("show reset: %v", err)
	}
	if want := "Reset 3 metric record(s).\n"; out != want {
		t.Fatalf("show reset = %q; want %q", out, want)
	}
}
