/**
 * FR-E34 — URL-based routing: tab + project params survive refresh.
 */
import { describe, expect, it } from 'vitest';
import { buildUrlSearch, parseTab, readUrlState, writeUrlState } from '../../src/lib/url';

describe('URL routing (FR-E34)', () => {
  it('parseTab falls back to graph for unknown values', () => {
    expect(parseTab('search')).toBe('search');
    expect(parseTab('export')).toBe('export');
    expect(parseTab('graph')).toBe('graph');
    expect(parseTab(null)).toBe('graph');
    expect(parseTab('bogus')).toBe('graph');
  });

  it('readUrlState reads tab + project from search', () => {
    expect(readUrlState('?tab=search&project=/workspace')).toEqual({
      tab: 'search',
      project: '/workspace',
    });
    expect(readUrlState('')).toEqual({ tab: 'graph', project: undefined });
  });

  it('buildUrlSearch round-trips through readUrlState', () => {
    const state = { tab: 'export' as const, project: '/workspace' };
    const qs = buildUrlSearch(state);
    expect(readUrlState(qs)).toEqual(state);
  });

  it('writeUrlState replaces history with the new search', () => {
    let written = '';
    writeUrlState(
      { tab: 'search', project: 'p' },
      (url) => {
        written = url;
      },
    );
    expect(written).toContain('tab=search');
    expect(written).toContain('project=p');
  });
});
