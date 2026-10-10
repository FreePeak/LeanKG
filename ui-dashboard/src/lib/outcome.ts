// Outcome vocabulary. Status colors are reserved for outcomes and always ship
// with an icon and a text label, never with color alone (dataviz rule).

import { CircleCheck, CircleDashed, CircleX, TriangleAlert, type LucideIcon } from 'lucide-react';
import { humanize } from '@/lib/format';

export interface OutcomeStyle {
  label: string;
  color: string;
  icon: LucideIcon;
}

const STYLES: Record<string, OutcomeStyle> = {
  ok: { label: 'Success', color: 'var(--status-good)', icon: CircleCheck },
  success: { label: 'Success', color: 'var(--status-good)', icon: CircleCheck },
  partial: { label: 'Partial', color: 'var(--status-warning)', icon: TriangleAlert },
  fallback: { label: 'Fallback', color: 'var(--status-serious)', icon: TriangleAlert },
  empty: { label: 'Empty', color: 'var(--text-muted)', icon: CircleDashed },
  error: { label: 'Error', color: 'var(--status-critical)', icon: CircleX },
};

/** Style for an outcome key. Unknown keys get a neutral style, not a series color. */
export function outcomeStyle(key: string): OutcomeStyle {
  return STYLES[key] ?? { label: humanize(key), color: 'var(--text-muted)', icon: CircleDashed };
}
