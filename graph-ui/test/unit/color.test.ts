/**
 * FR-E04 — cluster coloring: stable per cluster id, follows backend cluster
 * rule (parent directory of filePath, else "root").
 */
import { describe, expect, it } from 'vitest';
import { CLUSTER_COLORS, clusterColorOf, clusterIdOf } from '../../src/lib/color';

describe('cluster coloring (FR-E04)', () => {
  it('clusterIdOf derives parent directory of filePath', () => {
    expect(clusterIdOf('src/main.rs')).toBe('src');
    expect(clusterIdOf('src/web/handlers.rs')).toBe('src/web');
    expect(clusterIdOf('lib.rs')).toBe('root');
    expect(clusterIdOf('')).toBe('root');
  });

  it('clusterColorOf is stable per cluster id', () => {
    expect(clusterColorOf('src')).toBe(clusterColorOf('src'));
    expect(clusterColorOf('src/web')).toBe(clusterColorOf('src/web'));
  });

  it('clusterColorOf always picks from the palette', () => {
    const ids = ['', 'src', 'src/web', 'api', 'deeply/nested/dir'];
    for (const id of ids) {
      expect(CLUSTER_COLORS).toContain(clusterColorOf(id));
    }
  });
});
