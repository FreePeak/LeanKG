import { lazy, Suspense } from 'react';
import { Link } from 'react-router';
import { ArrowLeft } from 'lucide-react';
import { AsyncBody, ErrorState, LoadingState, PageHeader } from '@/components/page-state';
import { KPIGrid } from '@/components/stat-tile';
import { BarList } from '@/components/charts/bar-list';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { useAsync } from '@/api/hooks';
import { formatDateTime, formatDuration, formatMs, formatNumber, formatRatio, humanize } from '@/lib/format';
import { outcomeStyle } from '@/lib/outcome';
import { Button } from '@/components/ui/button';
import { ErrorBoundary } from '@/components/error-boundary';
import type { CallRow, SessionDetail, Transcript } from '@/api/types';

// assistant-ui is the heavy part of the bundle: load it only when the Replay tab opens.
const ReplayThread = lazy(() => import('@/replay/replay-thread').then((m) => ({ default: m.ReplayThread })));

export function SessionDetailPage({ id }: { id: string }) {
  const state = useAsync((api, signal) => api.session(id, signal), [id]);

  return (
    <>
      <Button asChild variant="ghost" size="sm" className="-ml-2 mb-2">
        <Link to="/sessions">
          <ArrowLeft aria-hidden="true" />
          Sessions
        </Link>
      </Button>
      <AsyncBody data={state.data} error={state.error} loading={state.loading} reload={state.reload}>
        {(d: SessionDetail) => {
          const s = d.session;
          return (
            <>
              <PageHeader
                title={`${humanize(s.client_name)} session`}
                description={
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-xs">{s.project || 'no project'}</span>
                    <span>{formatDateTime(s.first_ts)} to {formatDateTime(s.last_ts)}</span>
                    <span>({formatDuration(s.duration_s)})</span>
                    <Badge variant={d.linked ? 'good' : 'outline'}>{d.linked ? 'Transcript linked' : 'Captured calls only'}</Badge>
                  </span>
                }
              />
              <KPIGrid kpis={d.kpis} />
              <Tabs defaultValue="timeline" className="mt-6">
                <TabsList>
                  <TabsTrigger value="timeline">Call timeline</TabsTrigger>
                  <TabsTrigger value="replay">Replay</TabsTrigger>
                  <TabsTrigger value="failures">Failures</TabsTrigger>
                  <TabsTrigger value="memory">Memory</TabsTrigger>
                </TabsList>
                <TabsContent value="timeline">
                  <CallTimeline calls={d.calls} />
                </TabsContent>
                <TabsContent value="replay">
                  <ReplayTab id={s.id} />
                </TabsContent>
                <TabsContent value="failures">
                  <Card>
                    <CardHeader>
                      <CardTitle>Failures in this session</CardTitle>
                      <CardDescription>Counts by reason. Guidance is on the Failures page.</CardDescription>
                    </CardHeader>
                    <CardContent>
                      <BarList items={d.failures} label={humanize} color="var(--status-critical)" emptyText="No failed calls in this session." />
                    </CardContent>
                  </Card>
                </TabsContent>
                <TabsContent value="memory">
                  <MemoryEvents rows={d.memory} />
                </TabsContent>
              </Tabs>
            </>
          );
        }}
      </AsyncBody>
    </>
  );
}

function CallTimeline({ calls }: { calls: CallRow[] }) {
  if (calls.length === 0) return <p className="text-sm text-muted-foreground">No LeanKG calls were captured in this session.</p>;
  return (
    <Card>
      <Table>
        <caption className="sr-only">LeanKG calls in time order</caption>
        <TableHeader>
          <TableRow>
            <TableHead>Time</TableHead>
            <TableHead>Call</TableHead>
            <TableHead>Outcome</TableHead>
            <TableHead>Rung</TableHead>
            <TableHead className="text-right">Hits</TableHead>
            <TableHead className="text-right">Latency</TableHead>
            <TableHead className="text-right">Tokens</TableHead>
            <TableHead>Signals</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {calls.map((c) => {
            const o = outcomeStyle(c.outcome);
            const Icon = o.icon;
            const sig = c.signals;
            return (
              <TableRow key={c.id}>
                <TableCell className="whitespace-nowrap text-xs">{formatDateTime(c.ts)}</TableCell>
                <TableCell className="font-mono text-xs">
                  {c.tool}
                  {c.action ? `.${c.action}` : ''}
                </TableCell>
                <TableCell>
                  <span className="inline-flex items-center gap-1 text-xs">
                    <Icon className="size-3.5" style={{ color: o.color }} aria-hidden="true" />
                    {o.label}
                    {c.outcome_reason ? <span className="text-muted-foreground">({humanize(c.outcome_reason)})</span> : null}
                  </span>
                </TableCell>
                <TableCell className="text-xs">{c.rung || '-'}</TableCell>
                <TableCell className="text-right tabular-nums">{formatNumber(c.hits)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatMs(c.latency_ms)}</TableCell>
                <TableCell className="text-right tabular-nums text-xs">
                  {formatNumber(c.out_tokens)} / {formatNumber(c.baseline_tokens)}
                </TableCell>
                <TableCell className="text-xs text-secondary-foreground">
                  {sig ? (
                    <span>
                      used {sig.hits_used}/{sig.hits_returned}
                      {sig.hits_returned > 0 ? ` (${formatRatio(sig.used_precision)})` : ''}
                      {sig.fallback ? ', fallback' : ''}
                      {sig.requery ? ', re-query' : ''}
                    </span>
                  ) : (
                    '-'
                  )}
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </Card>
  );
}

/** Loads the transcript only when the Replay tab mounts (Radix unmounts inactive tabs). */
function ReplayTab({ id }: { id: string }) {
  const state = useAsync((api, signal) => api.transcript(id, signal), [id]);
  if (state.error) return <ErrorState error={state.error} onRetry={state.reload} />;
  if (!state.data) return <LoadingState label="Loading replay" />;
  const t: Transcript = state.data;
  return (
    <ErrorBoundary label="Replay">
      <Suspense fallback={<LoadingState label="Loading replay" />}>
        <ReplayThread transcript={t} />
      </Suspense>
    </ErrorBoundary>
  );
}

function MemoryEvents({ rows }: { rows: SessionDetail['memory'] }) {
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No memory events in this session.</p>;
  return (
    <Card>
      <Table>
        <caption className="sr-only">Memory events in this session</caption>
        <TableHeader>
          <TableRow>
            <TableHead>Time</TableHead>
            <TableHead>Verb</TableHead>
            <TableHead>Banks</TableHead>
            <TableHead className="text-right">Returned</TableHead>
            <TableHead className="text-right">Written</TableHead>
            <TableHead className="text-right">Latency</TableHead>
            <TableHead>Error</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow key={r.id}>
              <TableCell className="whitespace-nowrap text-xs">{formatDateTime(r.ts)}</TableCell>
              <TableCell className="text-xs">{r.verb}</TableCell>
              <TableCell className="font-mono text-xs">{r.banks || '-'}</TableCell>
              <TableCell className="text-right tabular-nums">{formatNumber(r.returned)}</TableCell>
              <TableCell className="text-right tabular-nums">{formatNumber(r.written)}</TableCell>
              <TableCell className="text-right tabular-nums">{formatMs(r.latency_ms)}</TableCell>
              <TableCell className="text-xs text-status-critical">{r.error ?? ''}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
