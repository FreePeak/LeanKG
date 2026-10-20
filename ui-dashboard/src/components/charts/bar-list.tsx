// Horizontal bars for one series (top failure reasons, per-client split).
// One series needs no legend; the labels name each bar. Bars are thin with
// rounded ends anchored at the baseline. Each row shows its count and share.

import type { Count } from '@/api/types';
import { formatNumber } from '@/lib/format';

export function BarList({
  items,
  label,
  color = 'var(--viz-1)',
  emptyText = 'No data in this window.',
}: {
  items: Count[];
  label: (key: string) => string;
  color?: string;
  emptyText?: string;
}) {
  const rows = items ?? [];
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">{emptyText}</p>;
  const total = rows.reduce((s, r) => s + r.count, 0);
  const max = Math.max(1, ...rows.map((r) => r.count));
  return (
    <ul className="flex flex-col gap-3">
      {rows.map((r) => {
        const share = total > 0 ? r.count / total : 0;
        return (
          <li key={r.key} className="flex flex-col gap-1">
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="min-w-0 truncate">{label(r.key)}</span>
              <span className="shrink-0 tabular-nums text-secondary-foreground">
                {formatNumber(r.count)} <span className="text-muted-foreground">({Math.round(share * 100)}%)</span>
              </span>
            </div>
            <div className="h-2 w-full rounded-full bg-surface-muted" role="presentation">
              <div
                className="h-2 rounded-full"
                style={{ width: `${(r.count / max) * 100}%`, background: color, minWidth: r.count > 0 ? 4 : 0 }}
              />
            </div>
          </li>
        );
      })}
    </ul>
  );
}
