// Outcome mix as one stacked bar with a legend. Multiple series, so the
// legend is always present and shows the count and share as text. Colors come
// from the fixed status palette and always pair with an icon and a label.
// Segments are separated by a 2px surface gap.

import type { Count } from '@/api/types';
import { formatNumber } from '@/lib/format';
import { outcomeStyle } from '@/lib/outcome';

export function OutcomeBar({ mix, compact = false }: { mix: Count[]; compact?: boolean }) {
  const rows = (mix ?? []).filter((m) => m.count > 0);
  const total = rows.reduce((s, r) => s + r.count, 0);
  if (total === 0) return <span className="text-sm text-muted-foreground">No calls</span>;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex h-3 w-full overflow-hidden rounded-full bg-surface-muted" role="img" aria-label={rows.map((r) => `${outcomeStyle(r.key).label} ${r.count}`).join(', ')}>
        {rows.map((r, i) => (
          <div
            key={r.key}
            style={{ width: `${(r.count / total) * 100}%`, background: outcomeStyle(r.key).color, marginLeft: i > 0 ? 2 : 0 }}
            className="h-full"
          />
        ))}
      </div>
      {compact ? null : (
        <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-secondary-foreground">
          {rows.map((r) => {
            const s = outcomeStyle(r.key);
            const Icon = s.icon;
            return (
              <li key={r.key} className="inline-flex items-center gap-1.5">
                <Icon className="size-3.5" style={{ color: s.color }} aria-hidden="true" />
                <span>{s.label}</span>
                <span className="tabular-nums text-foreground">{formatNumber(r.count)}</span>
                <span className="tabular-nums text-muted-foreground">({Math.round((r.count / total) * 100)}%)</span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
