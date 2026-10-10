// Consent controls (plan v4.15 DS-24). Used by the first-run consent screen and
// by Settings. Saving POSTs /consent with the CSRF token from the last GET.
// Revoking sets capture to off, and an optional dialog also purges the ledger.

import { useState } from 'react';
import { Link } from 'react-router';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { getApi } from '@/api/client';
import type { ConsentRequest, Settings } from '@/api/types';
import { humanize } from '@/lib/format';

type Level = 'off' | 'metadata' | 'bodies';

const LEVELS: { value: Level; label: string; detail: string }[] = [
  { value: 'off', label: 'Off', detail: 'Nothing is recorded. No telemetry ledger is written.' },
  {
    value: 'metadata',
    label: 'Metadata',
    detail: 'Records each LeanKG call: tool, action, outcome, rung, latency, and token counts. Arguments and response bodies are not stored.',
  },
  {
    value: 'bodies',
    label: 'Bodies',
    detail: 'Also stores call arguments and response bodies, cut to the maximum body size per field.',
  },
];

export function ConsentForm({ settings, onSaved }: { settings: Settings; onSaved: () => void }) {
  const [level, setLevel] = useState<Level>((settings.capture as Level) || 'off');
  const [granted, setGranted] = useState<string[]>(settings.clients.filter((c) => c.granted).map((c) => c.client));
  const [busy, setBusy] = useState<null | 'save' | 'revoke'>(null);
  const [message, setMessage] = useState<{ kind: 'ok' | 'error'; text: string } | null>(null);
  const [purge, setPurge] = useState(false);
  const [revokeOpen, setRevokeOpen] = useState(false);

  const transcriptsAllowed = level !== 'off';

  async function send(req: ConsentRequest, which: 'save' | 'revoke') {
    setBusy(which);
    setMessage(null);
    try {
      const api = await getApi();
      await api.postConsent(req, settings.csrf_token);
      setMessage({
        kind: 'ok',
        text: req.capture === 'off' ? (req.purge ? 'Consent revoked and the ledger was deleted.' : 'Consent revoked. Existing data is kept.') : 'Consent saved.',
      });
      setRevokeOpen(false);
      onSaved();
    } catch (err) {
      setMessage({ kind: 'error', text: err instanceof Error ? err.message : 'Save failed.' });
    } finally {
      setBusy(null);
    }
  }

  function save() {
    void send({ capture: level, sessions: transcriptsAllowed ? granted : [], purge: false }, 'save');
  }

  function revoke() {
    void send({ capture: 'off', sessions: [], purge }, 'revoke');
  }

  function toggle(client: string, on: boolean) {
    setGranted((g) => (on ? [...new Set([...g, client])] : g.filter((c) => c !== client)));
  }

  return (
    <div className="flex flex-col gap-5">
      <Card>
        <CardHeader>
          <CardTitle>Capture level</CardTitle>
          <CardDescription>Everything stays on this machine. Pick the least you need.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <label htmlFor="capture-level" className="text-sm font-medium">
              Record LeanKG calls
            </label>
            <Select value={level} onValueChange={(v) => setLevel(v as Level)}>
              <SelectTrigger id="capture-level" aria-label="Capture level" className="max-w-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {LEVELS.map((l) => (
                  <SelectItem key={l.value} value={l.value}>
                    {l.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <ul className="flex flex-col gap-2 text-sm text-secondary-foreground">
            {LEVELS.map((l) => (
              <li key={l.value} className={level === l.value ? 'text-foreground' : undefined}>
                <span className="font-medium">{l.label}:</span> {l.detail}
              </li>
            ))}
          </ul>
          <p className="text-sm text-secondary-foreground">
            Data is kept for {settings.retention_days} days. Bodies are cut to {settings.max_body_bytes.toLocaleString('en-US')} bytes per field.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Session transcripts</CardTitle>
          <CardDescription>
            Reading an agent's own session file lets the dashboard show full replays and context-use signals. It is read-only and granted per client.
            Without it, replays show only the captured LeanKG calls.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {!transcriptsAllowed ? <p className="text-sm text-muted-foreground">Choose a capture level other than Off to grant transcript reading.</p> : null}
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <caption className="sr-only">Agent stores found on this machine</caption>
              <thead className="text-left text-xs uppercase tracking-wide text-muted-foreground">
                <tr>
                  <th scope="col" className="px-2 py-2 font-medium">Client</th>
                  <th scope="col" className="px-2 py-2 font-medium">Found</th>
                  <th scope="col" className="px-2 py-2 font-medium">Paths</th>
                  <th scope="col" className="px-2 py-2 text-right font-medium">Grant reading</th>
                </tr>
              </thead>
              <tbody>
                {settings.clients.map((c) => {
                  const id = `grant-${c.client}`;
                  return (
                    <tr key={c.client} className="border-t border-border align-top">
                      <td className="px-2 py-2 font-medium">{humanize(c.client)}</td>
                      <td className="px-2 py-2">{c.found ? 'Yes' : <span className="text-muted-foreground">Not found</span>}</td>
                      <td className="px-2 py-2 break-all font-mono text-xs text-secondary-foreground">
                        {c.roots.length ? c.roots.join(', ') : '-'}
                      </td>
                      <td className="px-2 py-2 text-right">
                        <label htmlFor={id} className="inline-flex items-center gap-2">
                          <span className="sr-only">Grant transcript reading for {c.client}</span>
                          <Checkbox
                            id={id}
                            checked={granted.includes(c.client)}
                            disabled={!c.found || !transcriptsAllowed}
                            onChange={(e) => toggle(c.client, e.target.checked)}
                          />
                        </label>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {message ? (
        <p role={message.kind === 'error' ? 'alert' : 'status'} className={message.kind === 'error' ? 'text-sm text-status-critical' : 'text-sm text-foreground'}>
          {message.text}
        </p>
      ) : null}

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-xs text-muted-foreground">
          Config: <span className="font-mono">{settings.config_path}</span>
          {settings.granted_at ? (
            <>
              {' '}
              | granted {new Date(settings.granted_at).toLocaleString('en-US')} by {settings.granted_by ?? 'unknown'}
            </>
          ) : null}
          {' '}
          <Link to="/settings" className="underline underline-offset-2">
            Settings
          </Link>
        </div>
        <div className="flex flex-col gap-2 sm:flex-row">
          <Dialog open={revokeOpen} onOpenChange={setRevokeOpen}>
            <DialogTrigger asChild>
              <Button variant="outline" disabled={settings.capture === 'off' || busy !== null}>
                Revoke consent
              </Button>
            </DialogTrigger>
            <DialogContent aria-describedby="revoke-desc">
              <DialogHeader>
                <DialogTitle>Revoke consent?</DialogTitle>
                <DialogDescription id="revoke-desc">
                  Capture turns off and transcript reading is removed. Recorded data stays unless you also delete the ledger.
                </DialogDescription>
              </DialogHeader>
              <label className="flex items-start gap-2 text-sm">
                <Checkbox checked={purge} onChange={(e) => setPurge(e.target.checked)} className="mt-0.5" />
                <span>Also delete the telemetry ledger. This cannot be undone.</span>
              </label>
              <DialogFooter>
                <Button variant="outline" onClick={() => setRevokeOpen(false)}>
                  Cancel
                </Button>
                <Button variant="destructive" onClick={revoke} disabled={busy !== null}>
                  {purge ? 'Revoke and delete' : 'Revoke'}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
          <Button onClick={save} disabled={busy !== null}>
            {busy === 'save' ? 'Saving...' : 'Save consent'}
          </Button>
        </div>
      </div>
    </div>
  );
}
