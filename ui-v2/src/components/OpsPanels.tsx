/**
 * US-UI2-09 / FR-UI2-11 — ops panels ported from legacy ui/ into ui-v2:
 * environment selector, incidents (GET /api/incidents), conflicts
 * (GET /api/conflicts). Service and env selection reused from Explore sidebar.
 */
import { useCallback, useEffect, useState } from 'react';

export const ENVIRONMENTS = [
  { value: 'local', label: 'Local' },
  { value: 'staging', label: 'Staging' },
  { value: 'production', label: 'Production' },
] as const;

export type EnvValue = (typeof ENVIRONMENTS)[number]['value'];

export interface Incident {
  id: string;
  title: string;
  severity: string;
  root_cause: string;
  resolution: string;
  occurred_at: number;
}

export interface Conflict {
  conflict_type: string;
  detail: string;
  risk: string;
}

export interface OpsPanelsProps {
  service: string;
  env: EnvValue;
  onEnvChange: (env: EnvValue) => void;
}

function severityColor(severity: string): string {
  switch (severity.toUpperCase()) {
    case 'P0': return '#F44336';
    case 'P1': return '#FF5722';
    case 'P2': return '#FF9800';
    case 'P3': return '#FFC107';
    default: return '#9E9E9E';
  }
}

function riskStyles(risk: string): { border: string; chip: string } {
  if (risk === 'HIGH') return { border: '#F44336', chip: 'bg-red-900/40 text-red-300' };
  if (risk === 'MEDIUM') return { border: '#FF9800', chip: 'bg-amber-900/40 text-amber-300' };
  return { border: '#4CAF50', chip: 'bg-green-900/40 text-green-300' };
}

export function OpsPanels({ service, env, onEnvChange }: OpsPanelsProps) {
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [incidentsLoading, setIncidentsLoading] = useState(false);
  const [conflicts, setConflicts] = useState<Conflict[]>([]);
  const [conflictsLoading, setConflictsLoading] = useState(false);

  const loadIncidents = useCallback(async () => {
    if (!service) return;
    setIncidentsLoading(true);
    try {
      const res = await fetch(
        `/api/incidents?service=${encodeURIComponent(service)}&env=${env}`,
      );
      if (res.ok) {
        const data = (await res.json()) as {
          success?: boolean;
          data?: { incidents?: Incident[] };
        };
        if (data.success) setIncidents(data.data?.incidents || []);
      }
    } catch {
      /* backend unreachable — panel stays silent */
    } finally {
      setIncidentsLoading(false);
    }
  }, [service, env]);

  const loadConflicts = useCallback(async () => {
    if (!service) return;
    setConflictsLoading(true);
    try {
      const res = await fetch(`/api/conflicts?service=${encodeURIComponent(service)}`);
      if (res.ok) {
        const data = (await res.json()) as {
          success?: boolean;
          data?: { conflicts?: Conflict[] };
        };
        if (data.success) setConflicts(data.data?.conflicts || []);
      }
    } catch {
      /* backend unreachable — panel stays silent */
    } finally {
      setConflictsLoading(false);
    }
  }, [service]);

  useEffect(() => {
    setIncidents([]);
    setConflicts([]);
  }, [service]);

  return (
    <div
      className="border-t border-border-subtle pt-2 shrink-0"
      data-testid="ops-panels"
    >
      <h3 className="text-[11px] uppercase text-text-muted mb-1">Ops</h3>
      <div
        className="flex gap-1 mb-2"
        data-testid="env-selector"
        role="group"
        aria-label="Environment"
      >
        {ENVIRONMENTS.map((e) => (
          <button
            key={e.value}
            type="button"
            data-testid={`env-${e.value}`}
            aria-pressed={env === e.value}
            onClick={() => onEnvChange(e.value)}
            className={`px-2 py-0.5 rounded text-[11px] ${
              env === e.value
                ? 'bg-accent text-white'
                : 'bg-elevated text-text-secondary hover:text-text-primary'
            }`}
          >
            {e.label}
          </button>
        ))}
      </div>

      <div className="mb-2">
        <div className="flex items-center justify-between mb-1">
          <h4 className="text-[11px] text-text-secondary">Incidents</h4>
          <button
            type="button"
            data-testid="load-incidents"
            onClick={() => void loadIncidents()}
            disabled={!service || incidentsLoading}
            className="text-[10px] text-accent hover:underline disabled:opacity-50"
          >
            {incidentsLoading ? 'Loading…' : 'Load'}
          </button>
        </div>
        {!service && (
          <p className="text-[10px] text-text-muted">Select a service node.</p>
        )}
        {service && incidents.length === 0 && !incidentsLoading && (
          <p className="text-[10px] text-text-muted" data-testid="incidents-empty">
            No incidents loaded.
          </p>
        )}
        <ul className="space-y-1 max-h-40 overflow-y-auto" data-testid="incidents-list">
          {incidents.map((inc) => (
            <li
              key={inc.id}
              data-testid={`incident-${inc.id}`}
              className="rounded px-2 py-1.5 bg-elevated border-l-4"
              style={{ borderLeftColor: severityColor(inc.severity) }}
            >
              <div className="flex items-center gap-2">
                <span className="text-[11px] font-medium truncate">{inc.title}</span>
                <span className="text-[9px] uppercase text-text-muted shrink-0">
                  {inc.severity}
                </span>
              </div>
              <p className="text-[10px] text-text-secondary">{inc.root_cause}</p>
              <p className="text-[10px] text-text-muted">
                Resolution: {inc.resolution}
                {inc.occurred_at ? ` · ${new Date(inc.occurred_at * 1000).toLocaleString()}` : ''}
              </p>
            </li>
          ))}
        </ul>
      </div>

      <div>
        <div className="flex items-center justify-between mb-1">
          <h4 className="text-[11px] text-text-secondary">Env conflicts</h4>
          <button
            type="button"
            data-testid="load-conflicts"
            onClick={() => void loadConflicts()}
            disabled={!service || conflictsLoading}
            className="text-[10px] text-accent hover:underline disabled:opacity-50"
          >
            {conflictsLoading ? 'Checking…' : 'Check'}
          </button>
        </div>
        {!service && (
          <p className="text-[10px] text-text-muted">Select a service node.</p>
        )}
        {service && conflicts.length === 0 && !conflictsLoading && (
          <p className="text-[10px] text-text-muted" data-testid="conflicts-empty">
            No conflicts detected.
          </p>
        )}
        <ul className="space-y-1 max-h-40 overflow-y-auto" data-testid="conflicts-list">
          {conflicts.map((c, i) => {
            const styles = riskStyles(c.risk);
            return (
              <li
                key={`${c.conflict_type}-${i}`}
                data-testid={`conflict-${i}`}
                className="rounded px-2 py-1.5 bg-elevated border-l-4"
                style={{ borderLeftColor: styles.border }}
              >
                <div className="flex items-center gap-2">
                  <span className="text-[11px] font-medium truncate">{c.conflict_type}</span>
                  <span
                    className={`text-[9px] uppercase px-1 rounded shrink-0 ${styles.chip}`}
                  >
                    {c.risk}
                  </span>
                </div>
                <p className="text-[10px] text-text-secondary">{c.detail}</p>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
