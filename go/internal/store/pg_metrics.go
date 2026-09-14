// PostgreSQL context-metrics methods: the mirror of store_metrics.go over pgx.
// The table, the fold rules and the readout shapes are shared with the sqlite
// backend — see store_metrics.go for the contract, the Rust provenance and the
// documented ceilings. Only the dialect differs here: $n placeholders and
// boolean literals.
package store

import "fmt"

// RecordMetric appends one context_metrics row (Rust db::record_metric). A
// read-only backend no-ops with a nil error, like the sqlite implementation.
func (s *PGStore) RecordMetric(m Metric) error {
	if s.mode == RO {
		return nil
	}
	if _, err := s.pool.Exec(pgCtx, `INSERT INTO context_metrics (`+metricWriteCols+`) VALUES (`+
		sqlPlaceholders(metricColumnCount, true)+`)`, metricValues(m)...); err != nil {
		return fmt.Errorf("store: record metric %s: %w", m.ToolName, err)
	}
	return nil
}

// metricRows reads one live window (soft-deleted rows excluded, ascending by
// timestamp then tool name).
func (s *PGStore) metricRows(tool string, sinceCutoff int64) ([]Metric, error) {
	q := `SELECT ` + metricReadCols + ` FROM context_metrics WHERE is_deleted = FALSE AND timestamp >= $1`
	args := []any{sinceCutoff}
	if tool != "" {
		q += ` AND tool_name = $2`
		args = append(args, tool)
	}
	q += ` ORDER BY timestamp, tool_name`
	rows, err := s.pool.Query(pgCtx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: metrics window: %w", err)
	}
	defer rows.Close()
	return scanMetrics(rows)
}

// MetricsSummary reports the last retentionDays of the ledger, optionally for
// one tool (empty = every tool).
func (s *PGStore) MetricsSummary(tool string, retentionDays int) (MetricSummary, error) {
	rows, err := s.metricRows(tool, metricsCutoff(retentionDays))
	if err != nil {
		return MetricSummary{}, err
	}
	return summarizeMetrics(rows, retentionDays), nil
}

// UsageAggregates returns the H10/FR-PLG-8 buckets; sinceCutoff == 0 means all
// time.
func (s *PGStore) UsageAggregates(sinceCutoff int64) (UsageAggregates, error) {
	rows, err := s.metricRows("", sinceCutoff)
	if err != nil {
		return UsageAggregates{}, err
	}
	return aggregateUsage(rows), nil
}

// CleanupMetrics deletes rows older than retentionDays and reports how many
// went. Soft-deleted rows are purged too (retention sweep, not a view filter).
func (s *PGStore) CleanupMetrics(retentionDays int) (int64, error) {
	cutoff := metricsCutoff(retentionDays)
	var n int64
	if err := s.pool.QueryRow(pgCtx,
		`SELECT COUNT(*) FROM context_metrics WHERE timestamp < $1`, cutoff).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count metrics before cleanup: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := s.pool.Exec(pgCtx, `DELETE FROM context_metrics WHERE timestamp < $1`, cutoff); err != nil {
		return 0, fmt.Errorf("store: cleanup metrics: %w", err)
	}
	return n, nil
}

// ResetMetrics deletes the whole ledger and reports how many rows went.
func (s *PGStore) ResetMetrics() (int64, error) {
	var n int64
	if err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM context_metrics`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count metrics for reset: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := s.pool.Exec(pgCtx, `DELETE FROM context_metrics`); err != nil {
		return 0, fmt.Errorf("store: reset metrics: %w", err)
	}
	return n, nil
}
