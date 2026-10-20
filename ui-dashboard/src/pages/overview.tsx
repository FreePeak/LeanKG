import { useState } from 'react';
import { Link } from 'react-router';
import { AsyncBody, PageHeader } from '@/components/page-state';
import { KPIGrid } from '@/components/stat-tile';
import { CaptureBanner } from '@/components/capture-banner';
import { DailyChart } from '@/components/charts/daily-chart';
import { BarList } from '@/components/charts/bar-list';
import { OutcomeBar } from '@/components/charts/outcome-bar';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useAsync } from '@/api/hooks';
import { SINCE_OPTIONS } from '@/lib/options';
import { humanize } from '@/lib/format';

export function OverviewPage() {
  const [since, setSince] = useState<string>('7d');
  const state = useAsync((api, signal) => api.overview(since, signal), [since]);

  return (
    <>
      <PageHeader
        title="Overview"
        description="What LeanKG did for your agents, on this machine."
        actions={
          <Select value={since} onValueChange={setSince}>
            <SelectTrigger aria-label="Time window">
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
        }
      />
      <AsyncBody
        data={state.data}
        error={state.error}
        loading={state.loading}
        reload={state.reload}
        isEmpty={(o) => o.kpis.length === 0 && o.series.length === 0}
        emptyTitle="No LeanKG calls in this window."
        emptyHint="Calls appear here once an agent uses LeanKG with capture on."
      >
        {(o) => (
          <div className="flex flex-col gap-5">
            <CaptureBanner capture={o.capture_level} />
            <KPIGrid kpis={o.kpis} />
            <div className="grid gap-5 lg:grid-cols-3">
              <Card className="lg:col-span-2">
                <CardHeader>
                  <CardTitle>Daily activity</CardTitle>
                  <CardDescription>Calls and errors per day, local time.</CardDescription>
                </CardHeader>
                <CardContent>
                  {o.series.length ? <DailyChart series={o.series} /> : <p className="text-sm text-muted-foreground">No daily data yet.</p>}
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>Outcome mix</CardTitle>
                  <CardDescription>How LeanKG calls ended.</CardDescription>
                </CardHeader>
                <CardContent>
                  <OutcomeBar mix={o.outcome_mix} />
                </CardContent>
              </Card>
            </div>
            <div className="grid gap-5 md:grid-cols-2">
              <Card>
                <CardHeader>
                  <CardTitle>Top failure reasons</CardTitle>
                  <CardDescription>
                    <Link to="/failures" className="underline underline-offset-2">
                      See failures with guidance
                    </Link>
                  </CardDescription>
                </CardHeader>
                <CardContent>
                  <BarList items={o.top_failures} label={humanize} color="var(--status-critical)" emptyText="No failures in this window." />
                </CardContent>
              </Card>
              <Card>
                <CardHeader>
                  <CardTitle>Per-client split</CardTitle>
                  <CardDescription>Calls by agent client.</CardDescription>
                </CardHeader>
                <CardContent>
                  <BarList items={o.by_client} label={humanize} />
                </CardContent>
              </Card>
            </div>
          </div>
        )}
      </AsyncBody>
    </>
  );
}
