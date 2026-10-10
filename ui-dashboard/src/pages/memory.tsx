import { AsyncBody, PageHeader } from '@/components/page-state';
import { ProvenanceBadge } from '@/components/provenance-badge';
import { StatTile, formatKPIValue } from '@/components/stat-tile';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useAsync } from '@/api/hooks';
import { formatDateTime, formatNumber, formatRatio, formatDuration, humanize } from '@/lib/format';
import { kpiProvenance, proxyProvenance } from '@/lib/provenance';
import type { Memory } from '@/api/types';

const NEVER_RECALLED_METHOD =
  'Share of bank entries with no recall in the window. A proxy: a per-id recall counter is not recorded yet.';

export function MemoryPage() {
  const state = useAsync((api, signal) => api.memory({}, signal), []);
  return (
    <>
      <PageHeader title="Memory" description="Recall and retain activity for the memory banks LeanKG uses." />
      <AsyncBody
        data={state.data}
        error={state.error}
        loading={state.loading}
        reload={state.reload}
        isEmpty={(m) => m.kpis.length === 0 && m.banks.length === 0 && m.recent.length === 0}
        emptyTitle="No memory activity in this window."
        emptyHint="Recall and retain events appear once the memory hook or a memory tool is used."
      >
        {(m: Memory) => (
          <div className="flex flex-col gap-5">
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              {m.kpis.map((k) => (
                <StatTile
                  key={k.key}
                  label={k.label}
                  value={formatKPIValue(k)}
                  // The never-recalled figure is a proxy, not a counterfactual estimate.
                  provenance={k.key === 'never_recalled' ? proxyProvenance(k.method) : kpiProvenance(k)}
                />
              ))}
            </div>

            <Card>
              <CardHeader>
                <CardTitle>Banks</CardTitle>
                <CardDescription>Per bank: recall volume, hit rate, writes and the never-recalled proxy.</CardDescription>
              </CardHeader>
              <CardContent>
                <Table>
                  <caption className="sr-only">Memory banks</caption>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Bank</TableHead>
                      <TableHead className="text-right">Recalls</TableHead>
                      <TableHead className="text-right">Hit rate</TableHead>
                      <TableHead className="text-right">Written</TableHead>
                      <TableHead className="text-right">
                        <span className="inline-flex items-center gap-1">
                          Never recalled
                          <ProvenanceBadge provenance={proxyProvenance(NEVER_RECALLED_METHOD)} />
                        </span>
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {m.banks.map((b) => (
                      <TableRow key={b.bank}>
                        <TableCell className="font-mono text-xs">{b.bank}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(b.recalls)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatRatio(b.hit_rate)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(b.written)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatRatio(b.never_recalled_share)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle>Recent events</CardTitle>
              </CardHeader>
              <CardContent>
                <Table>
                  <caption className="sr-only">Recent memory events</caption>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Time</TableHead>
                      <TableHead>Verb</TableHead>
                      <TableHead>Banks</TableHead>
                      <TableHead className="text-right">Returned</TableHead>
                      <TableHead className="text-right">Written</TableHead>
                      <TableHead className="text-right">Skipped</TableHead>
                      <TableHead className="text-right">Oldest hit</TableHead>
                      <TableHead className="text-right">Latency</TableHead>
                      <TableHead>Error</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {m.recent.map((r) => (
                      <TableRow key={r.id}>
                        <TableCell className="whitespace-nowrap text-xs">{formatDateTime(r.ts)}</TableCell>
                        <TableCell className="text-xs">{humanize(r.verb)}</TableCell>
                        <TableCell className="font-mono text-xs">{r.banks || '-'}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(r.returned)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(r.written)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(r.skipped)}</TableCell>
                        <TableCell className="text-right tabular-nums">{r.age_max_s > 0 ? formatDuration(r.age_max_s) : '-'}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(r.latency_ms)} ms</TableCell>
                        <TableCell className="max-w-xs text-xs text-status-critical">{r.error ?? ''}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
          </div>
        )}
      </AsyncBody>
    </>
  );
}
