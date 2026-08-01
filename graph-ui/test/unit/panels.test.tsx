/**
 * FR-E30..E43 — panel component tests.
 */
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import DetailPanel, { countRelationships } from '../../src/components/DetailPanel';
import GraphSummary from '../../src/components/GraphSummary';
import FilterPanel from '../../src/components/FilterPanel';
import SettingsPanel from '../../src/components/SettingsPanel';
import SearchPanel, { matchNode } from '../../src/components/SearchPanel';
import HistoryPanel from '../../src/components/HistoryPanel';
import ExportPanel from '../../src/components/ExportPanel';
import ProjectPanel from '../../src/components/ProjectPanel';
import ErrorBanner from '../../src/components/ErrorBanner';
import LoadingOverlay from '../../src/components/LoadingOverlay';
import type { GraphData, GraphNode } from '../../src/lib/types';

const NODE: GraphNode = {
  id: 'src/main.rs::main',
  label: 'Main',
  properties: { name: 'main', filePath: 'src/main.rs', elementType: 'Function' },
};

const GRAPH: GraphData = {
  nodes: [NODE],
  relationships: [
    { id: 'e1', sourceId: 'src/main.rs::main', targetId: 'other', type: 'calls', confidenceLabel: 'HIGH' },
    { id: 'e2', sourceId: 'src/main.rs::main', targetId: 'other2', type: 'imports', confidenceLabel: 'HIGH' },
  ],
  filtered: null,
  hasMore: false,
};

describe('DetailPanel (FR-E30)', () => {
  it('renders qualified name, type, file, and connection counts', () => {
    render(<DetailPanel node={NODE} relationships={GRAPH.relationships} onClose={() => {}} />);
    expect(screen.getByTestId('detail-title').textContent).toBe('main');
    expect(screen.getByText('src/main.rs')).toBeTruthy();
    expect(screen.getByText('Function')).toBeTruthy();
    expect(screen.getByText('2')).toBeTruthy();
  });

  it('countRelationships breaks down by type', () => {
    const c = countRelationships(GRAPH.relationships, 'src/main.rs::main');
    expect(c.total).toBe(2);
    expect(c.byType).toEqual({ calls: 1, imports: 1 });
  });

  it('fetches a source snippet for the node file (FR-E30)', async () => {
    const fetchMock = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        new Response(
          JSON.stringify({ success: true, data: { content: 'fn main() {}\n' }, error: null }),
          { status: 200 },
        ),
      );
    render(<DetailPanel node={NODE} relationships={[]} onClose={() => {}} />);
    await screen.findByText('fn main() {}');
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/file?path=src%2Fmain.rs',
    );
  });
});

describe('GraphSummary (FR-E30/E33)', () => {
  it('shows node/edge counts', () => {
    render(<GraphSummary graph={GRAPH} />);
    expect(screen.getByTestId('summary-nodes').textContent).toBe('1');
    expect(screen.getByTestId('summary-edges').textContent).toBe('2');
  });

  it('shows empty state without graph', () => {
    render(<GraphSummary graph={null} />);
    expect(screen.getByText('No graph data loaded.')).toBeTruthy();
  });
});

describe('FilterPanel (FR-E31)', () => {
  it('renders a checkbox per relationship type', () => {
    render(<FilterPanel graph={GRAPH} filter={{ calls: true, imports: true }} onChange={() => {}} />);
    const boxes = screen.getAllByRole('checkbox');
    expect(boxes).toHaveLength(2);
  });

  it('toggles call onChange', async () => {
    const onChange = vi.fn();
    render(<FilterPanel graph={GRAPH} filter={{ calls: true, imports: true }} onChange={onChange} />);
    await userEvent.click(screen.getAllByRole('checkbox')[0]);
    expect(onChange).toHaveBeenCalledWith({ calls: false, imports: true });
  });
});

describe('SettingsPanel (FR-E32/E38)', () => {
  it('renders sliders and label toggle', () => {
    render(<SettingsPanel settings={{ bloomIntensity: 0.8, edgeBrightness: 0.45, labelsVisible: true, densityScale: 1 }} onChange={() => {}} />);
    expect(screen.getAllByRole('slider')).toHaveLength(3);
    expect(screen.getByRole('checkbox')).toBeTruthy();
  });

  it('emits density changes', () => {
    const onChange = vi.fn();
    render(<SettingsPanel settings={{ bloomIntensity: 0.8, edgeBrightness: 0.45, labelsVisible: true, densityScale: 1 }} onChange={onChange} />);
    const sliders = screen.getAllByRole('slider');
    fireEvent.change(sliders[2], { target: { value: '1.5' } });
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ densityScale: 1.5 }),
    );
  });
});

describe('SearchPanel (FR-E33)', () => {
  it('matchNode filters by name/file/type', () => {
    expect(matchNode(NODE, 'main')).toBe(true);
    expect(matchNode(NODE, 'src/main.rs')).toBe(true);
    expect(matchNode(NODE, 'function')).toBe(true);
    expect(matchNode(NODE, 'zzz')).toBe(false);
    expect(matchNode(NODE, '')).toBe(true);
  });

  it('renders matching nodes and selects on click', async () => {
    const onSelect = vi.fn();
    render(<SearchPanel graph={GRAPH} onSelect={onSelect} />);
    await userEvent.type(screen.getByRole('searchbox'), 'main');
    await userEvent.click(screen.getByRole('button', { name: /main/i }));
    expect(onSelect).toHaveBeenCalledWith('src/main.rs::main');
  });
});

describe('HistoryPanel (FR-E36)', () => {
  it('shows entries with undo/redo buttons', () => {
    render(
      <HistoryPanel
        entries={[{ id: 'a', label: 'src/main.rs::main' }]}
        undoEnabled
        redoEnabled={false}
        onUndo={() => {}}
        onRedo={() => {}}
      />,
    );
    expect(screen.getByText('src/main.rs::main')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Undo' })).toBeTruthy();
  });
});

describe('ExportPanel (FR-E37)', () => {
  it('downloads JSON snapshot', async () => {
    const createUrl = vi.fn(() => 'blob:test');
    const revoke = vi.fn();
    const append = vi.fn();
    Object.assign(URL, { createObjectURL: createUrl, revokeObjectURL: revoke });
    const origAppend = document.body.appendChild.bind(document.body);
    document.body.appendChild = ((node: Node) => {
      append(node);
      origAppend(node);
      return node;
    }) as typeof document.body.appendChild;
    render(<ExportPanel graph={GRAPH} tab="graph" />);
    await userEvent.click(screen.getByRole('button', { name: 'Download JSON' }));
    expect(createUrl).toHaveBeenCalled();
    expect(append).toHaveBeenCalled();
    document.body.appendChild = origAppend;
  });
});

describe('ProjectPanel (FR-E33/E36)', () => {
  it('lists projects and highlights active', () => {
    const projects = [
      { name: 'leankg', path: '/workspace', elementCount: 100 },
      { name: 'freepeak', path: '/workspace-freepeak' },
    ];
    render(
      <ProjectPanel
        projects={projects}
        activePath="/workspace"
        onSwitch={() => {}}
        onRefresh={() => {}}
      />,
    );
    expect(screen.getByText('leankg')).toBeTruthy();
    expect(screen.getByText('100 elements')).toBeTruthy();
    const active = screen.getByRole('button', { name: /leankg/ });
    expect(active.getAttribute('aria-pressed')).toBe('true');
  });

  it('calls onSwitch with the project path', async () => {
    const onSwitch = vi.fn();
    render(
      <ProjectPanel
        projects={[{ name: 'p', path: '/p' }]}
        activePath={null}
        onSwitch={onSwitch}
        onRefresh={() => {}}
      />,
    );
    await userEvent.click(screen.getByRole('button', { name: /p/ }));
    expect(onSwitch).toHaveBeenCalledWith('/p');
  });
});

describe('ErrorBanner (FR-E40)', () => {
  it('renders message with retry/dismiss', () => {
    render(<ErrorBanner message="boom" onRetry={() => {}} onDismiss={() => {}} />);
    expect(screen.getByRole('alert')).toBeTruthy();
    expect(screen.getByText('boom')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy();
  });

  it('renders nothing without message', () => {
    const { container } = render(<ErrorBanner message={null} />);
    expect(container.firstChild).toBeNull();
  });
});

describe('LoadingOverlay (FR-E39)', () => {
  it('shows progress overlay when loading', () => {
    render(<LoadingOverlay loading label="Loading graph…" progress={42} />);
    expect(screen.getByTestId('loading-overlay')).toBeTruthy();
    expect(screen.getByText('42%')).toBeTruthy();
  });

  it('renders nothing when idle', () => {
    const { container } = render(<LoadingOverlay loading={false} />);
    expect(container.firstChild).toBeNull();
  });
});
