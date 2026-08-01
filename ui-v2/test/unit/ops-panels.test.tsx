/**
 * US-UI2-09 / FR-UI2-11 — ops panels (env selector, incidents, conflicts)
 * ported from legacy ui/.
 */
import React from 'react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { OpsPanels, ENVIRONMENTS } from '../../src/components/OpsPanels';

const INCIDENTS_BODY = {
  success: true,
  data: {
    incidents: [
      {
        id: 'inc-1',
        title: 'Auth timeout spike',
        severity: 'P1',
        root_cause: 'connection pool exhausted',
        resolution: 'raised pool size',
        occurred_at: 1754000000,
      },
    ],
  },
};

const CONFLICTS_BODY = {
  success: true,
  data: {
    conflicts: [
      { conflict_type: 'version mismatch', detail: 'staging pins v1.2, prod v1.3', risk: 'HIGH' },
    ],
  },
};

describe('OpsPanels (US-UI2-09)', () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('renders the three env buttons and marks current env pressed', () => {
    render(<OpsPanels service="svc-auth" env="staging" onEnvChange={() => {}} />);
    for (const e of ENVIRONMENTS) {
      const btn = screen.getByTestId(`env-${e.value}`);
      expect(btn.textContent).toBe(e.label);
    }
    expect(screen.getByTestId('env-staging').getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByTestId('env-local').getAttribute('aria-pressed')).toBe('false');
  });

  it('fires onEnvChange when an env button is clicked', () => {
    const onEnvChange = vi.fn();
    render(<OpsPanels service="svc-auth" env="local" onEnvChange={onEnvChange} />);
    fireEvent.click(screen.getByTestId('env-production'));
    expect(onEnvChange).toHaveBeenCalledWith('production');
  });

  it('loads incidents from /api/incidents with service+env and renders them', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      json: async () => INCIDENTS_BODY,
    });
    render(<OpsPanels service="svc-auth" env="staging" onEnvChange={() => {}} />);
    fireEvent.click(screen.getByTestId('load-incidents'));
    await waitFor(() => {
      expect(screen.getByTestId('incident-inc-1').textContent).toMatch(/Auth timeout spike/);
    });
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/incidents?service=svc-auth&env=staging',
    );
  });

  it('loads conflicts from /api/conflicts and renders them with risk chip', async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      json: async () => CONFLICTS_BODY,
    });
    render(<OpsPanels service="svc-auth" env="local" onEnvChange={() => {}} />);
    fireEvent.click(screen.getByTestId('load-conflicts'));
    await waitFor(() => {
      const row = screen.getByTestId('conflict-0');
      expect(row.textContent).toMatch(/version mismatch/);
      expect(row.textContent).toMatch(/HIGH/);
    });
    expect(fetchMock).toHaveBeenCalledWith('/api/conflicts?service=svc-auth');
  });

  it('disables loads when no service is selected', () => {
    render(<OpsPanels service="" env="local" onEnvChange={() => {}} />);
    expect((screen.getByTestId('load-incidents') as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByTestId('load-conflicts') as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getAllByText('Select a service node.').length).toBe(2);
  });
});
