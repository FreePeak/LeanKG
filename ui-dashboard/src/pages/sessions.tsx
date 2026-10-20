import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { AsyncBody, PageHeader } from '@/components/page-state';
import { ProvenanceBadge } from '@/components/provenance-badge';
import { OutcomeBar } from '@/components/charts/outcome-bar';
import { Card } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { Badge } from '@/components/ui/badge';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Input } from '@/components/ui/input';
import { useAsync } from '@/api/hooks';
import { CLIENT_OPTIONS, SINCE_OPTIONS } from '@/lib/options';
import { formatDateTime, formatDuration, formatNumber, formatRatio, humanize } from '@/lib/format';
import type { Provenance } from '@/lib/provenance';
import type { SessionSummary } from '@/api/types';

const ALL = '__all__';

export function SessionsPage() {
  const navigate = useNavigate();
  const [client, setClient] = useState(ALL);
  const [project, setProject] = useState('');
  const [since, setSince] = useState('30d');
  const state = useAsync(
    (api, signal) => api.sessions({ client: client === ALL ? undefined : client, project: project.trim() || undefined, since }, signal),
    [client, project, since],
  );

  return (
    <>
      <PageHeader title="Sessions" description="Agent sessions that used LeanKG. Open one for its calls and replay." />
      <Card className="mb-4 flex flex-col gap-3 p-3 sm:flex-row sm:flex-wrap sm:items-end">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Client
          <Select value={client} onValueChange={setClient}>
            <SelectTrigger aria-label="Filter by client" className="min-w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All clients</SelectItem>
              {CLIENT_OPTIONS.map((c) => (
                <SelectItem key={c} value={c}>
                  {humanize(c)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Project
          <Input value={project} onChange={(e) => setProject(e.target.value)} placeholder="Any project" className="min-w-44" />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Window
          <Select value={since} onValueChange={setSince}>
            <SelectTrigger aria-label="Time window" className="min-w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {SINCE_OPTIONS.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </label>
      </Card>

      <AsyncBody
        data={state.data}
        error={state.error}
        loading={state.loading}
        reload={state.reload}
        isEmpty={(d) => d.sessions.length === 0}
        emptyTitle="No sessions match these filters."
        emptyHint="Sessions appear when capture is on and an agent calls LeanKG."
      >
        {(d) => {
          const tokensProvenance: Provenance = {
            kind: 'estimate',
            label: 'estimate',
            method: 'Baseline tokens minus delivered tokens, summed per session. The baseline is an estimate.',
          };
          return (
            <>
              <p className="mb-2 text-sm text-muted-foreground">
                {formatNumber(d.total)} session{d.total === 1 ? '' : 's'}
              </p>
              <Card>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Client</TableHead>
                      <TableHead>Project</TableHead>
                      <TableHead>Started</TableHead>
                      <TableHead className="text-right">Duration</TableHead>
                      <TableHead className="text-right">Calls</TableHead>
                      <TableHead className="text-right">Success</TableHead>
                      <TableHead className="text-right">
                        <span className="inline-flex items-center gap-1">
                          Tokens saved
                          <ProvenanceBadge provenance={tokensProvenance} />
                        </span>
                      </TableHead>
                      <TableHead>Outcome mix</TableHead>
                      <TableHead>Link</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {d.sessions.map((s: SessionSummary) => (
                      <TableRow
                        key={s.id}
                        className="cursor-pointer hover:bg-surface-muted"
                        onClick={() => navigate(`/sessions/${encodeURIComponent(s.id)}`)}
                      >
                        <TableCell className="font-medium">
                          <Link to={`/sessions/${encodeURIComponent(s.id)}`} className="underline-offset-2 hover:underline" onClick={(e) => e.stopPropagation()}>
                            {humanize(s.client_name)}
                          </Link>
                        </TableCell>
                        <TableCell className="font-mono text-xs">{s.project || '-'}</TableCell>
                        <TableCell className="whitespace-nowrap text-xs">{formatDateTime(s.first_ts)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatDuration(s.duration_s)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(s.calls)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatRatio(s.success_rate)}</TableCell>
                        <TableCell className="text-right tabular-nums">{formatNumber(s.tokens_saved)}</TableCell>
                        <TableCell className="min-w-44">
                          <OutcomeBar mix={s.outcome_mix} compact />
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-col gap-1">
                            <Badge variant="outline">{s.link_status === 'linked' ? 'Linked' : humanize(s.link_status)}</Badge>
                            <span className="text-xs text-muted-foreground">{humanize(s.correlation)}</span>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </Card>
            </>
          );
        }}
      </AsyncBody>
    </>
  );
}
