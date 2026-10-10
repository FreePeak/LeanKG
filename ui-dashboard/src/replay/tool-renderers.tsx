// Tool-call renderers for the replay.
//
// Registration (verified against @assistant-ui/react 0.15.26 .d.ts):
//   - defineToolkit({ leankg: { render } }) is the non-deprecated API. The
//     toolkit goes into the provider through AuiConfig({ tools: Tools({ toolkit }) }).
//   - makeAssistantToolUI is marked @deprecated in 0.15.26 and is not used.
//   - MessagePrimitive.Parts components.tools.Fallback renders every tool
//     call that has no toolkit renderer (the non-LeanKG calls).

import { defineToolkit, AuiConfig, Tools, type ToolCallMessagePartProps } from '@assistant-ui/react';
import { ProvenanceBadge } from '@/components/provenance-badge';
import { Badge } from '@/components/ui/badge';
import { proxyProvenance, type Provenance } from '@/lib/provenance';
import { formatMs, formatNumber, formatRatio } from '@/lib/format';
import { outcomeStyle } from '@/lib/outcome';
import { cn } from '@/lib/utils';
import type { CallRow, Signals } from '@/api/types';
import { asReplayResult, LEANKG_TOOL_NAME } from './mapper';

const FALLBACK_HINT = 'Post-LeanKG fallback: a search outside the returned hits followed this call.';

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-sm tabular-nums">{value || '-'}</dd>
    </div>
  );
}

function SignalsBlock({ signals }: { signals?: Signals }) {
  if (!signals) return <p className="text-xs text-muted-foreground">Context-use signals are not available for this call.</p>;
  const precision: Provenance = proxyProvenance('Share of returned hits that the later transcript references by name. A proxy for context use, not ground truth.');
  return (
    <ul className="flex flex-col gap-1 text-xs text-secondary-foreground">
      <li className="flex flex-wrap items-center gap-2">
        {signals.hits_returned > 0 ? (
          <>
            <span>
              Used {signals.hits_used} of {signals.hits_returned} hits ({formatRatio(signals.used_precision)})
            </span>
            <ProvenanceBadge provenance={precision} />
          </>
        ) : (
          <span>No hits returned, so there is no context-use proxy.</span>
        )}
      </li>
      <li>
        {signals.fallback ? (
          <span className="text-foreground">Fallback: {signals.fallback_tools.length ? signals.fallback_tools.join(', ') : 'search'} outside the hits</span>
        ) : (
          'No fallback search'
        )}
      </li>
      {signals.requery ? <li>Re-query: the agent asked again for the same thing.</li> : null}
      {signals.abandoned_after ? <li>Abandoned: no follow-up after this call.</li> : null}
      {signals.followed_guidance === undefined ? null : (
        <li>{signals.followed_guidance ? 'Followed the returned guidance.' : 'Did not follow the returned guidance.'}</li>
      )}
    </ul>
  );
}

/** Renders one LeanKG call: outcome, rung, quality, tokens, latency and signals. */
export function LeanKGToolCall({ result, isError }: ToolCallMessagePartProps) {
  const r = asReplayResult(result);
  const call: CallRow | undefined = r.leankg;
  if (!call) return <OtherToolCall result={result} isError={isError} toolName={LEANKG_TOOL_NAME} />;

  const outcome = outcomeStyle(call.outcome);
  const OutcomeIcon = outcome.icon;
  // Savings are claimed only for a successful call that returned something.
  const showSaved = call.outcome === 'ok' && call.tokens_saved > 0;
  const tokens: Provenance | null = call.baseline_method
    ? { kind: 'estimate', label: 'estimate', method: call.baseline_method }
    : null;

  return (
    <section
      aria-label={`LeanKG ${call.tool} ${call.action}`}
      className={cn('flex flex-col gap-3 rounded-lg border bg-surface p-3', r.fallback ? 'border-status-serious border-l-4' : 'border-border')}
    >
      <header className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-sm font-medium">
          leankg {call.tool}
          {call.action ? `.${call.action}` : ''}
        </span>
        <Badge variant="outline" className="gap-1">
          <OutcomeIcon className="size-3.5" style={{ color: outcome.color }} aria-hidden="true" />
          {outcome.label}
          {call.outcome_reason ? <span className="text-muted-foreground">({call.outcome_reason})</span> : null}
        </Badge>
        {call.rung ? <Badge variant="outline">rung {call.rung}</Badge> : null}
        {call.confidence ? <Badge variant="outline">confidence {call.confidence}</Badge> : null}
        {call.freshness ? <Badge variant="outline">{call.freshness}</Badge> : null}
        {r.fallback ? <Badge variant="serious">fallback</Badge> : null}
      </header>
      {r.fallback ? <p className="text-xs text-foreground">{FALLBACK_HINT}</p> : null}
      {call.command ? <p className="truncate font-mono text-xs text-muted-foreground" title={call.command}>{call.command}</p> : null}
      <dl className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Field label="Hits" value={formatNumber(call.hits)} />
        <Field label="Latency" value={formatMs(call.latency_ms)} />
        <Field label="Delivered" value={`${formatNumber(call.out_tokens)} tok`} />
        <div className="flex flex-col">
          <dt className="text-xs text-muted-foreground">Baseline</dt>
          <dd className="flex flex-wrap items-center gap-1.5 text-sm tabular-nums">
            {formatNumber(call.baseline_tokens)} tok
            {tokens ? <ProvenanceBadge provenance={tokens} /> : null}
          </dd>
        </div>
      </dl>
      {showSaved ? (
        <p className="text-xs text-secondary-foreground">
          Saved about {formatNumber(call.tokens_saved)} tok against the baseline (estimate).
        </p>
      ) : null}
      <div className="flex flex-col gap-1">
        <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Signals</span>
        <SignalsBlock signals={call.signals} />
      </div>
    </section>
  );
}

/** Collapsed one-line row for a non-LeanKG tool call. Post-LeanKG fallbacks are highlighted. */
export function OtherToolCall({ result, toolName, isError }: ToolCallMessagePartProps | { result: unknown; toolName: string; isError?: boolean }) {
  const r = asReplayResult(result);
  const name = r.originalToolName || toolName;
  const firstLine = r.output.split('\n')[0] ?? '';
  return (
    <details
      className={cn(
        'group rounded-md border bg-surface px-3 py-2 text-sm',
        r.fallback ? 'border-status-serious border-l-4' : 'border-border',
        isError && 'text-status-critical',
      )}
    >
      <summary className="flex cursor-pointer list-none items-center gap-2">
        <span className="font-mono text-xs font-medium">{name}</span>
        {r.fallback ? <Badge variant="serious">post-LeanKG fallback</Badge> : null}
        {isError ? <Badge variant="critical">error</Badge> : null}
        <span className="min-w-0 truncate text-xs text-muted-foreground">{firstLine}</span>
      </summary>
      {r.output ? (
        <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded bg-surface-muted p-2 font-mono text-xs">{r.output}</pre>
      ) : (
        <p className="mt-2 text-xs text-muted-foreground">No output captured.</p>
      )}
      {r.fallback ? <p className="mt-2 text-xs text-foreground">{FALLBACK_HINT}</p> : null}
    </details>
  );
}

/** Toolkit with the LeanKG renderer. Fed to the provider through AuiConfig. */
export const leankgToolkit = defineToolkit({
  [LEANKG_TOOL_NAME]: { render: LeanKGToolCall },
});

/** Provider config: installs the toolkit's renderers. Built once, at module load. */
export const replayConfig = AuiConfig({
  tools: Tools({ toolkit: leankgToolkit }),
});
