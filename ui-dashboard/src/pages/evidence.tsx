import { AsyncBody, PageHeader } from '@/components/page-state';
import { ProvenanceBadge } from '@/components/provenance-badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';
import { useAsync } from '@/api/hooks';
import { controlledProvenance, type Provenance } from '@/lib/provenance';
import { formatMs, formatNumber } from '@/lib/format';
import type { ArmCompare, Dist, Evidence } from '@/api/types';

const OBSERVATIONAL_BADGE: Provenance = {
  kind: 'estimate',
  label: 'estimate',
  method: 'Observational comparison of sessions with and without LeanKG. Confounded by task size, project and client: not causal.',
};

function fmt(value: number, unit: string): string {
  return unit === 'ms' ? formatMs(value) : formatNumber(value);
}

function DistCell({ d, unit }: { d: Dist; unit: string }) {
  if (!d || d.n === 0) return <span className="text-muted-foreground">no data</span>;
  return (
    <span className="tabular-nums">
      <span className="font-medium">{fmt(d.median, unit)}</span>{' '}
      <span className="text-muted-foreground">
        (IQR {fmt(d.p25, unit)} to {fmt(d.p75, unit)}, n={formatNumber(d.n)})
      </span>
    </span>
  );
}

function ArmTable({ rows, caption }: { rows: ArmCompare[]; caption: string }) {
  return (
    <Table>
      <caption className="sr-only">{caption}</caption>
      <TableHeader>
        <TableRow>
          <TableHead>Metric</TableHead>
          <TableHead>With LeanKG</TableHead>
          <TableHead>Without</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.metric}>
            <TableCell className="font-medium">{r.metric}</TableCell>
            <TableCell className="text-sm">
              <DistCell d={r.with} unit={r.unit} />
            </TableCell>
            <TableCell className="text-sm">
              <DistCell d={r.without} unit={r.unit} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function EvidencePage() {
  const state = useAsync((api, signal) => api.evidence(signal), []);
  return (
    <>
      <PageHeader title="Evidence" description="Two separate panels. Only the controlled A/B panel is measured." />
      <AsyncBody data={state.data} error={state.error} loading={state.loading} reload={state.reload}>
        {(e: Evidence) => {
          const measured = controlledProvenance(e.controlled_valid, e.controlled_runs);
          return (
            <div className="flex flex-col gap-5">
              <Card>
                <CardHeader>
                  <div className="flex flex-wrap items-center gap-2">
                    <CardTitle>Observational</CardTitle>
                    <ProvenanceBadge provenance={OBSERVATIONAL_BADGE} />
                    <Badge variant="outline">confounded: not causal</Badge>
                  </div>
                  <CardDescription>{e.observational_note}</CardDescription>
                </CardHeader>
                <CardContent>
                  {e.observational.length ? (
                    <ArmTable rows={e.observational} caption="Observational comparison, with and without LeanKG" />
                  ) : (
                    <p className="text-sm text-muted-foreground">Not enough sessions on both sides to compare yet.</p>
                  )}
                </CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <div className="flex flex-wrap items-center gap-2">
                    <CardTitle>Controlled A/B</CardTitle>
                    {measured ? <ProvenanceBadge provenance={measured} /> : null}
                  </div>
                  <CardDescription>{e.controlled_note}</CardDescription>
                </CardHeader>
                <CardContent>
                  {measured && e.controlled.length ? (
                    <ArmTable rows={e.controlled} caption="Controlled paired A/B, with and without LeanKG" />
                  ) : (
                    <div role="note" className="rounded-md border border-dashed border-border p-3 text-sm text-secondary-foreground">
                      No valid controlled A/B result is available, so no measured figures are shown. Run the paired benchmark to produce them.
                    </div>
                  )}
                </CardContent>
              </Card>
            </div>
          );
        }}
      </AsyncBody>
    </>
  );
}
