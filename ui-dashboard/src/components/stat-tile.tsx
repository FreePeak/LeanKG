import { Card } from '@/components/ui/card';
import { ProvenanceBadge } from '@/components/provenance-badge';
import { kpiProvenance, type Provenance } from '@/lib/provenance';
import { formatMs, formatNumber, formatPercent } from '@/lib/format';
import type { KPI } from '@/api/types';

/** Render a KPI value with its unit. "%" values are already percent points. */
export function formatKPIValue(kpi: Pick<KPI, 'value' | 'unit'>): string {
  switch (kpi.unit) {
    case '%':
      return formatPercent(kpi.value, kpi.value % 1 === 0 ? 0 : 1);
    case 'ms':
      return formatMs(kpi.value);
    case 'tokens':
      return `${formatNumber(kpi.value)} tok`;
    default:
      return formatNumber(kpi.value);
  }
}

export function StatTile({
  label,
  value,
  hint,
  provenance,
}: {
  label: string;
  value: string;
  hint?: string;
  provenance?: Provenance | null;
}) {
  return (
    <Card className="flex flex-col gap-2 p-4">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</span>
        {provenance ? <ProvenanceBadge provenance={provenance} /> : null}
      </div>
      <span className="text-2xl font-semibold tabular-nums">{value}</span>
      {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </Card>
  );
}

/** Stat tile for a server KPI: the estimate badge and method come from the KPI itself. */
export function KPITile({ kpi }: { kpi: KPI }) {
  return <StatTile label={kpi.label} value={formatKPIValue(kpi)} provenance={kpiProvenance(kpi)} />;
}

export function KPIGrid({ kpis }: { kpis: KPI[] }) {
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4 xl:grid-cols-5">
      {(kpis ?? []).map((k) => (
        <KPITile key={k.key} kpi={k} />
      ))}
    </div>
  );
}
