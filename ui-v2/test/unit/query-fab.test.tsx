/**
 * FR-UI2-08 / US-UI2-06 — Query FAB dual-mode UI.
 */
import React from 'react';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { QueryFAB } from '../../src/components/QueryFAB';

const runQueryGraph = vi.fn();
const runQuery = vi.fn();

vi.mock('../../src/services/backend-client', () => ({
  runQueryGraph: (...args: unknown[]) => runQueryGraph(...args),
  runQuery: (...args: unknown[]) => runQuery(...args),
}));

describe('QueryFAB dual-mode (FR-UI2-08)', () => {
  beforeEach(() => {
    runQueryGraph.mockReset();
    runQuery.mockReset();
    runQueryGraph.mockResolvedValue({ question: 'q', nodes: [], edges: [], seeds: [] });
    runQuery.mockResolvedValue({ rows: [] });
  });

  it('opens in NL mode by default', async () => {
    render(<QueryFAB />);
    fireEvent.click(screen.getByTestId('query-fab'));
    expect(screen.getByTestId('query-mode-nl').getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByTestId('query-mode-advanced').getAttribute('aria-pressed')).toBe('false');
    expect(screen.getByTestId('query-panel-title').textContent).toMatch(/natural|nl|query/i);
  });

  it('submits NL questions via runQueryGraph', async () => {
    render(<QueryFAB />);
    fireEvent.click(screen.getByTestId('query-fab'));
    fireEvent.change(screen.getByTestId('query-input'), {
      target: { value: 'what connects auth to database?' },
    });
    fireEvent.click(screen.getByTestId('query-run'));
    await waitFor(() => {
      expect(runQueryGraph).toHaveBeenCalledWith('what connects auth to database?');
    });
    expect(runQuery).not.toHaveBeenCalled();
  });

  it('switches to Advanced and posts raw graph query via runQuery', async () => {
    render(<QueryFAB />);
    fireEvent.click(screen.getByTestId('query-fab'));
    fireEvent.click(screen.getByTestId('query-mode-advanced'));
    expect(screen.getByTestId('query-mode-advanced').getAttribute('aria-pressed')).toBe('true');
    fireEvent.change(screen.getByTestId('query-input'), {
      target: { value: '?[a] := *code_elements{qualified_name: a}' },
    });
    fireEvent.click(screen.getByTestId('query-run'));
    await waitFor(() => {
      expect(runQuery).toHaveBeenCalledWith('?[a] := *code_elements{qualified_name: a}');
    });
    expect(runQueryGraph).not.toHaveBeenCalled();
  });
});
