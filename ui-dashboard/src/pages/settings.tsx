import { AsyncBody, PageHeader } from '@/components/page-state';
import { ConsentForm } from '@/components/consent-form';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { useAsync } from '@/api/hooks';
import { formatBytes, formatDateTime, formatNumber, humanize } from '@/lib/format';
import type { Settings } from '@/api/types';

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-0.5 border-t border-border py-2 first:border-t-0 sm:flex-row sm:justify-between sm:gap-4">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="break-all text-sm tabular-nums sm:text-right">{value}</dd>
    </div>
  );
}

export function SettingsPage() {
  const state = useAsync((api, signal) => api.consent(signal), []);
  return (
    <>
      <PageHeader title="Settings" description="Consent, retention and the telemetry store on this machine." />
      <AsyncBody data={state.data} error={state.error} loading={state.loading} reload={state.reload}>
        {(s: Settings) => (
          <div className="flex flex-col gap-5">
            <Card>
              <CardHeader>
                <CardTitle>Store</CardTitle>
              </CardHeader>
              <CardContent>
                <dl>
                  <Row label="Capture" value={humanize(s.capture)} />
                  <Row label="Consent current" value={s.consent_current ? 'Yes' : 'No'} />
                  <Row label="Granted" value={s.granted_at ? `${formatDateTime(s.granted_at)} by ${s.granted_by ?? 'unknown'}` : 'Not granted'} />
                  <Row label="Retention" value={`${formatNumber(s.retention_days)} days`} />
                  <Row label="Max body size" value={formatBytes(s.max_body_bytes)} />
                  <Row label="Telemetry database" value={s.db_path} />
                  <Row label="Database size" value={formatBytes(s.db_size_bytes)} />
                  <Row label="Dropped events" value={formatNumber(s.dropped_events)} />
                  <Row label="Config file" value={s.config_path} />
                </dl>
              </CardContent>
            </Card>
            <ConsentForm settings={s} onSaved={state.reload} />
          </div>
        )}
      </AsyncBody>
    </>
  );
}
