import { useState } from 'react';
import { AsyncBody, PageHeader } from '@/components/page-state';
import { BarList } from '@/components/charts/bar-list';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useAsync } from '@/api/hooks';
import { FAILURE_GROUPS } from '@/lib/options';
import { formatDateTime, formatNumber, formatRatio, humanize } from '@/lib/format';
import type { Failures } from '@/api/types';

export function FailuresPage() {
  const [group, setGroup] = useState<string>('reason');
  const state = useAsync((api, signal) => api.failures(group, signal), [group]);

  return (
    <>
      <PageHeader
        title="Failures"
        description="Failed or empty LeanKG calls, grouped. Each group shows the guidance that was returned and sample calls."
      />
      <Tabs value={group} onValueChange={setGroup} className="mb-5">
        <TabsList aria-label="Group failures by">
          {FAILURE_GROUPS.map((g) => (
            <TabsTrigger key={g.value} value={g.value}>
              By {g.label.toLowerCase()}
            </TabsTrigger>
          ))}
        </TabsList>
      </Tabs>
      <AsyncBody
        data={state.data}
        error={state.error}
        loading={state.loading}
        reload={state.reload}
        isEmpty={(f: Failures) => f.groups.length === 0}
        emptyTitle="No failures in this window."
      >
        {(f: Failures) => (
          <div className="flex flex-col gap-5">
            <p className="text-sm text-muted-foreground">{formatNumber(f.total)} failed or empty calls.</p>
            <Card>
              <CardHeader>
                <CardTitle>Share by {f.group}</CardTitle>
              </CardHeader>
              <CardContent>
                <BarList
                  items={f.groups.map((g) => ({ key: g.key, count: g.count }))}
                  label={humanize}
                  color="var(--status-critical)"
                />
              </CardContent>
            </Card>
            {f.groups.map((g) => (
              <Card key={g.key}>
                <CardHeader>
                  <CardTitle>
                    {humanize(g.key)}{' '}
                    <span className="text-sm font-normal text-muted-foreground">
                      {formatNumber(g.count)} ({formatRatio(g.share)})
                    </span>
                  </CardTitle>
                  {g.guidance ? (
                    <CardDescription>
                      <span className="font-medium text-foreground">Guidance returned: </span>
                      {g.guidance}
                    </CardDescription>
                  ) : null}
                </CardHeader>
                <CardContent>
                  {g.samples.length === 0 ? (
                    <p className="text-sm text-muted-foreground">No sample calls for this group.</p>
                  ) : (
                    <Table>
                      <caption className="sr-only">Sample calls for {g.key}</caption>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Time</TableHead>
                          <TableHead>Call</TableHead>
                          <TableHead>Command</TableHead>
                          <TableHead>Reason</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {g.samples.map((c) => (
                          <TableRow key={c.id}>
                            <TableCell className="whitespace-nowrap text-xs">{formatDateTime(c.ts)}</TableCell>
                            <TableCell className="font-mono text-xs">
                              {c.tool}
                              {c.action ? `.${c.action}` : ''}
                            </TableCell>
                            <TableCell className="max-w-xs truncate font-mono text-xs" title={c.command}>{c.command || '-'}</TableCell>
                            <TableCell className="text-xs">{c.outcome_reason ? humanize(c.outcome_reason) : humanize(c.outcome)}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </AsyncBody>
    </>
  );
}
