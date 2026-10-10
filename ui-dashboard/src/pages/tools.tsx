import { AsyncBody, PageHeader } from '@/components/page-state';
import { ProvenanceBadge } from '@/components/provenance-badge';
import { OutcomeBar } from '@/components/charts/outcome-bar';
import { Card } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useAsync } from '@/api/hooks';
import { formatMs, formatNumber, formatRatio } from '@/lib/format';
import type { Provenance } from '@/lib/provenance';

const TOKENS_PROVENANCE: Provenance = {
  kind: 'estimate',
  label: 'estimate',
  method: 'Baseline tokens minus delivered tokens, summed per tool and action. The baseline is an estimate.',
};

export function ToolsPage() {
  const state = useAsync((api, signal) => api.tools(signal), []);
  return (
    <>
      <PageHeader title="Tools" description="Latency, outcomes and rung mix per tool and action. Rung shows which retrieval tier answered." />
      <AsyncBody
        data={state.data}
        error={state.error}
        loading={state.loading}
        reload={state.reload}
        isEmpty={(t) => t.tools.length === 0}
        emptyTitle="No tool calls in this window."
      >
        {(t) => (
          <Card>
            <Table>
              <caption className="sr-only">Tool statistics</caption>
              <TableHeader>
                <TableRow>
                  <TableHead>Tool</TableHead>
                  <TableHead className="text-right">Calls</TableHead>
                  <TableHead className="text-right">Success</TableHead>
                  <TableHead className="text-right">p50</TableHead>
                  <TableHead className="text-right">p95</TableHead>
                  <TableHead>Rung mix</TableHead>
                  <TableHead>Outcomes</TableHead>
                  <TableHead className="text-right">
                    <span className="inline-flex items-center gap-1">
                      Tokens saved
                      <ProvenanceBadge provenance={TOKENS_PROVENANCE} />
                    </span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {t.tools.map((r) => (
                  <TableRow key={`${r.tool}.${r.action}`}>
                    <TableCell className="font-mono text-xs">
                      {r.tool}
                      {r.action ? `.${r.action}` : ''}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{formatNumber(r.calls)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatRatio(r.success_rate)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatMs(r.p50_ms)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatMs(r.p95_ms)}</TableCell>
                    <TableCell className="text-xs">
                      {r.rung_mix.length
                        ? r.rung_mix.map((m) => `${m.key} ${formatNumber(m.count)}`).join(' | ')
                        : '-'}
                    </TableCell>
                    <TableCell className="min-w-40">
                      <OutcomeBar mix={r.outcome_mix} compact />
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{formatNumber(r.tokens_saved)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </Card>
        )}
      </AsyncBody>
    </>
  );
}
