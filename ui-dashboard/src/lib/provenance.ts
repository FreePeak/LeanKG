// The provenance rule (plan v4.15 section 2, design rule 5).
//
//  - Every counterfactual number (tokens or turns saved) is an "estimate" or a
//    "proxy", and shows its method.
//  - Only figures from the controlled A/B run are "measured".
//  - Plain counts and rates observed in captured calls need no badge.
//
// kpiProvenance never returns "measured": a KPI from the server cannot prove
// a controlled comparison. Only controlledProvenance can, and only when the
// server reports controlled_valid.

import type { KPI } from '@/api/types';

export type ProvenanceKind = 'measured' | 'estimate' | 'proxy';

export interface Provenance {
  kind: ProvenanceKind;
  /** Badge text. */
  label: string;
  /** Tooltip text: how the number was computed. */
  method: string;
}

const MISSING_METHOD = 'Method not provided by the server.';

/** Badge for a server KPI. Null when the figure is a direct observation. */
export function kpiProvenance(kpi: Pick<KPI, 'estimate' | 'method'>): Provenance | null {
  if (!kpi.estimate) return null;
  return { kind: 'estimate', label: 'estimate', method: kpi.method?.trim() || MISSING_METHOD };
}

/** Badge for a proxy figure (an indirect signal, such as never-recalled share). */
export function proxyProvenance(method?: string): Provenance {
  return { kind: 'proxy', label: 'proxy', method: method?.trim() || MISSING_METHOD };
}

/** The only "measured" badge. Null unless the controlled A/B is valid. */
export function controlledProvenance(controlledValid: boolean, runs: number): Provenance | null {
  if (!controlledValid) return null;
  return {
    kind: 'measured',
    label: 'measured',
    method: `Controlled paired A/B, ${runs} run${runs === 1 ? '' : 's'}, with and without LeanKG.`,
  };
}
