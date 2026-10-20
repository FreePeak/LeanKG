// Number, percent, duration and date formatters. Everything is pinned to
// en-US so output is deterministic in tests and across viewers.

const LOCALE = 'en-US';

/** Format a count or quantity. Compact (1.2K, 3.4M) from 10,000 up. */
export function formatNumber(value: number): string {
  if (!Number.isFinite(value)) return '-';
  const abs = Math.abs(value);
  if (abs >= 10_000) {
    return new Intl.NumberFormat(LOCALE, { notation: 'compact', maximumFractionDigits: 1 }).format(value);
  }
  return new Intl.NumberFormat(LOCALE, { maximumFractionDigits: abs < 10 && value % 1 !== 0 ? 2 : 1 }).format(value);
}

/**
 * Format a percentage. The argument is already in percent points (0-100),
 * which is how KPI values with unit "%" arrive from the server.
 */
export function formatPercent(points: number, digits = 0): string {
  if (!Number.isFinite(points)) return '-';
  return `${points.toFixed(digits)}%`;
}

/** Format a 0..1 ratio (SessionSummary.success_rate, Signals.used_precision) as a percentage. */
export function formatRatio(ratio: number, digits = 0): string {
  if (!Number.isFinite(ratio)) return '-';
  return formatPercent(ratio * 100, digits);
}

/** Format milliseconds: "850 ms" below one second, "1.2 s" below one minute, then "2 m 05 s". */
export function formatMs(ms: number): string {
  if (!Number.isFinite(ms)) return '-';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return formatDuration(ms / 1000);
}

/** Format seconds as a short duration: "42s", "3m 12s", "2h 05m", "1d 4h". */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds)) return '-';
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${String(m % 60).padStart(2, '0')}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

/** Format a byte size as KB / MB / GB with one decimal. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes)) return '-';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return unit === 0 ? `${value} B` : `${value.toFixed(1)} ${units[unit]}`;
}

/** Format an ISO timestamp for a table cell: "Oct 10, 14:32". */
export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '-';
  return new Intl.DateTimeFormat(LOCALE, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(d);
}

/** Format a YYYY-MM-DD day label for chart axes: "Oct 10". */
export function formatDay(day: string): string {
  const d = new Date(`${day}T00:00:00`);
  if (Number.isNaN(d.getTime())) return day;
  return new Intl.DateTimeFormat(LOCALE, { month: 'short', day: 'numeric' }).format(d);
}

/** Title-case a snake_case or kebab-case key for display: "tokens_saved" -> "Tokens saved". */
export function humanize(key: string): string {
  const spaced = key.replace(/[_-]+/g, ' ').trim();
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
