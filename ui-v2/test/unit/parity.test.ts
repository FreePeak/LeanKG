import { describe, expect, it } from 'vitest';
import {
  decideSkipGraph,
  parseSkipGraphParam,
  shouldConfirmGraphLoad,
} from '../../src/lib/graph-load-decision';
import { LARGE_GRAPH_NODE_THRESHOLD } from '../../src/lib/constants';
import { normalizeGraphPayload } from '../../src/lib/normalize';
import { buildLayoutGraph } from '../../src/lib/graph-adapter';
import { DEFAULT_VISIBLE_LABELS, DEFAULT_NODE_TYPE_ORDER } from '../../src/lib/constants';
import { parseProjectParam } from '../../src/services/backend-client';
import {
  cameraRatioForBounds,
  computeGraphBounds,
  normalizeExpandPath,
} from '../../src/lib/camera-fit';

describe('decideSkipGraph', () => {
  it('respects explicit true/false', () => {
    expect(
      decideSkipGraph({
        explicit: true,
        nodeCount: 10,
        threshold: LARGE_GRAPH_NODE_THRESHOLD,
      }),
    ).toBe(true);
    expect(
      decideSkipGraph({
        explicit: false,
        nodeCount: 999_999,
        threshold: LARGE_GRAPH_NODE_THRESHOLD,
      }),
    ).toBe(false);
  });

  it('auto-skips when over node threshold', () => {
    expect(
      decideSkipGraph({
        explicit: undefined,
        nodeCount: LARGE_GRAPH_NODE_THRESHOLD + 1,
        threshold: LARGE_GRAPH_NODE_THRESHOLD,
      }),
    ).toBe(true);
  });

  it('fails open when counts unknown', () => {
    expect(
      decideSkipGraph({
        explicit: undefined,
        nodeCount: null,
        threshold: LARGE_GRAPH_NODE_THRESHOLD,
      }),
    ).toBe(false);
  });
});

describe('shouldConfirmGraphLoad', () => {
  it('confirms when unknown or large', () => {
    expect(shouldConfirmGraphLoad(null, 100)).toBe(true);
    expect(shouldConfirmGraphLoad(200, 100)).toBe(true);
    expect(shouldConfirmGraphLoad(50, 100)).toBe(false);
  });
});

describe('parseSkipGraphParam', () => {
  it('parses tri-state', () => {
    expect(parseSkipGraphParam('1')).toBe(true);
    expect(parseSkipGraphParam('true')).toBe(true);
    expect(parseSkipGraphParam('0')).toBe(false);
    expect(parseSkipGraphParam(null)).toBeUndefined();
  });
});

describe('normalizeGraphPayload', () => {
  it('normalizes camelCase and snake_case edges', () => {
    const kg = normalizeGraphPayload({
      nodes: [
        {
          id: 'a',
          label: 'Function',
          properties: { name: 'main', filePath: 'src/main.rs' },
        },
      ],
      relationships: [
        { source_id: 'a', target_id: 'b', rel_type: 'calls' },
      ],
    });
    expect(kg.nodes[0].properties.filePath).toBe('src/main.rs');
    expect(kg.relationships[0].sourceId).toBe('a');
    expect(kg.relationships[0].type).toBe('CALLS');
  });
});

describe('buildLayoutGraph', () => {
  it('builds force/tree/circles graphs', () => {
    const kg = normalizeGraphPayload({
      nodes: [
        {
          id: 'f1',
          label: 'Folder',
          properties: { name: 'src', filePath: 'src', elementType: 'Folder' },
        },
        {
          id: 'file1',
          label: 'File',
          properties: { name: 'main.rs', filePath: 'src/main.rs', elementType: 'File' },
        },
        {
          id: 'fn1',
          label: 'Function',
          properties: {
            name: 'main',
            filePath: 'src/main.rs',
            elementType: 'Function',
          },
        },
      ],
      relationships: [
        { sourceId: 'f1', targetId: 'file1', type: 'CONTAINS' },
        { sourceId: 'file1', targetId: 'fn1', type: 'DEFINES' },
      ],
    });
    for (const mode of ['force', 'tree', 'circles'] as const) {
      const g = buildLayoutGraph(kg, mode);
      expect(g.order).toBe(3);
      expect(g.size).toBeGreaterThanOrEqual(1);
    }
  });
});

describe('US-MG-04 defaults', () => {
  it('defaults visible labels include structure + LeanKG symbol types', () => {
    expect(DEFAULT_VISIBLE_LABELS).toContain('Service');
    expect(DEFAULT_VISIBLE_LABELS).toContain('Folder');
    expect(DEFAULT_VISIBLE_LABELS).toContain('File');
    expect(DEFAULT_VISIBLE_LABELS).toContain('Function');
    expect(DEFAULT_VISIBLE_LABELS).toContain('Property');
    expect(DEFAULT_VISIBLE_LABELS).toContain('Method');
    expect(DEFAULT_NODE_TYPE_ORDER[0]).toBe('Service');
  });
});

describe('parseProjectParam', () => {
  it('trims and rejects empty', () => {
    expect(parseProjectParam('  /workspace  ')).toBe('/workspace');
    expect(parseProjectParam('')).toBeUndefined();
    expect(parseProjectParam(null)).toBeUndefined();
  });
});

describe('camera-fit', () => {
  it('normalizes expand paths for project root', () => {
    expect(normalizeExpandPath('')).toBe('.');
    expect(normalizeExpandPath('./')).toBe('.');
    expect(normalizeExpandPath('/proj', '/proj')).toBe('.');
    expect(normalizeExpandPath('/proj/', '/proj')).toBe('.');
    expect(normalizeExpandPath('/proj/src/cli', '/proj')).toBe('./src/cli');
    expect(normalizeExpandPath('src/cli')).toBe('src/cli');
  });

  it('computes bounds and camera ratio that prefer filling tall containers', () => {
    const g = buildLayoutGraph(
      normalizeGraphPayload({
        nodes: [
          { id: 'a', label: 'Folder', properties: { name: 'a', elementType: 'Folder' } },
          { id: 'b', label: 'File', properties: { name: 'b', elementType: 'File' } },
          { id: 'c', label: 'Function', properties: { name: 'c', elementType: 'Function' } },
        ],
        relationships: [
          { sourceId: 'a', targetId: 'b', type: 'CONTAINS' },
          { sourceId: 'b', targetId: 'c', type: 'DEFINES' },
        ],
      }),
      'tree',
    );
    const bounds = computeGraphBounds(g, { paddingFactor: 1 });
    expect(bounds).not.toBeNull();
    // Tree layers should span meaningful vertical space (taller canvas).
    expect(bounds!.height).toBeGreaterThan(200);
    const ratioWide = cameraRatioForBounds(bounds!, 1400, 400);
    const ratioTall = cameraRatioForBounds(bounds!, 800, 900);
    expect(ratioWide).toBeGreaterThan(0);
    expect(ratioTall).toBeGreaterThan(0);
  });
});
