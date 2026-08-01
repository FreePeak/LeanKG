import { describe, expect, it } from 'vitest';
import {
  DEFAULT_LOD_CONFIG,
  applyInteractionLod,
  batchNodes,
  cameraSpeedPenalty,
  clusterCollapse,
  decideLod,
  dist3,
  frameQualityPenalty,
  importanceSize,
  isCulled,
  levelByDistance,
  selectLodLevel,
  showLabel,
  type LODConfig,
  type SceneState,
} from './lod';

const cfg: LODConfig = { ...DEFAULT_LOD_CONFIG };

describe('FR-E20 — distance LOD', () => {
  it('near nodes render as full spheres (level 2)', () => {
    expect(levelByDistance(0, cfg)).toBe(2);
    expect(levelByDistance(cfg.nearThreshold - 1, cfg)).toBe(2);
  });
  it('mid nodes render as small spheres (level 1)', () => {
    expect(levelByDistance(cfg.nearThreshold + 1, cfg)).toBe(1);
    expect(levelByDistance(cfg.farThreshold - 1, cfg)).toBe(1);
  });
  it('far nodes render as points (level 0)', () => {
    expect(levelByDistance(cfg.farThreshold + 1, cfg)).toBe(0);
    expect(levelByDistance(1e6, cfg)).toBe(0);
  });
});

describe('FR-E25 — frame budget adaptive quality', () => {
  it('healthy frame rate yields no penalty', () => {
    expect(frameQualityPenalty(cfg.frameBudgetMs, cfg)).toBe(0);
  });
  it('slow frames drop one level', () => {
    expect(frameQualityPenalty(cfg.slowFrameMs + 1, cfg)).toBe(1);
  });
  it('very slow frames drop two levels', () => {
    expect(frameQualityPenalty(cfg.verySlowFrameMs + 1, cfg)).toBe(2);
  });
  it('combined selectLodLevel drops detail at low fps', () => {
    expect(selectLodLevel(50, 0, cfg.verySlowFrameMs + 1, cfg)).toBe(0);
  });
});

describe('FR-E26 — camera-speed LOD', () => {
  it('walking speed keeps detail', () => {
    expect(cameraSpeedPenalty(cfg.fastSpeed - 1, cfg)).toBe(0);
  });
  it('fast movement drops one level', () => {
    expect(cameraSpeedPenalty(cfg.fastSpeed + 1, cfg)).toBe(1);
  });
  it('very fast movement drops two levels', () => {
    expect(cameraSpeedPenalty(cfg.veryFastSpeed + 1, cfg)).toBe(2);
  });
  it('moving fast lowers the level of near nodes', () => {
    expect(selectLodLevel(50, cfg.fastSpeed + 1, 10, cfg)).toBe(1);
  });
});

describe('FR-E21 — cluster collapse', () => {
  it('high-degree nodes collapse to cluster glyphs at distance', () => {
    const collapsed = clusterCollapse({ degree: cfg.clusterMinDegree }, cfg);
    expect(collapsed).toBe(true);
  });
  it('low-degree nodes never collapse', () => {
    expect(clusterCollapse({ degree: 2 }, cfg)).toBe(false);
  });
});

describe('FR-E22 — progressive loading', () => {
  it('small graphs load in one batch', () => {
    expect(batchNodes(100, cfg.batchSize, cfg)).toEqual([100]);
  });
  it('large graphs stream in ordered batches', () => {
    const batches = batchNodes(
      cfg.progressiveNodeCount + cfg.batchSize * 2,
      cfg.batchSize,
      cfg,
    );
    expect(batches.length).toBeGreaterThan(1);
    expect(batches.reduce((a, b) => a + b, 0)).toBe(
      cfg.progressiveNodeCount + cfg.batchSize * 2,
    );
    expect(batches[0]).toBe(cfg.batchSize);
  });
});

describe('FR-E23 — label LOD', () => {
  it('labels only near the camera', () => {
    expect(showLabel(10, cfg)).toBe(true);
    expect(showLabel(cfg.labelDistance, cfg)).toBe(true);
    expect(showLabel(cfg.labelDistance + 1, cfg)).toBe(false);
  });
});

describe('FR-E24 — occlusion culling', () => {
  it('nodes inside the frustum are kept', () => {
    expect(isCulled({ x: 0, y: 0, z: 0.5 }, cfg)).toBe(false);
  });
  it('nodes outside NDC clamp are culled', () => {
    expect(isCulled({ x: 10, y: 0, z: 0.5 }, cfg)).toBe(true);
    expect(isCulled({ x: 0, y: -10, z: 0.5 }, cfg)).toBe(true);
  });
  it('nodes behind the camera are culled', () => {
    expect(isCulled({ x: 0, y: 0, z: 2 }, cfg)).toBe(true);
  });
});

describe('FR-E27 — node size by importance', () => {
  it('uses god score when the backend exposes it', () => {
    expect(importanceSize(10, 1000)).toBeGreaterThan(importanceSize(1, 1000));
  });
  it('falls back to degree', () => {
    expect(importanceSize(undefined, 8)).toBeGreaterThan(
      importanceSize(undefined, 2),
    );
  });
  it('clamps to bounds', () => {
    expect(importanceSize(undefined, 0)).toBe(0.5);
    expect(importanceSize(1e9, 1e9)).toBe(3);
  });
});

describe('FR-E28 — interaction LOD', () => {
  it('hovered node upgrades detail', () => {
    const state: SceneState = {
      distances: new Float32Array([cfg.farThreshold + 100, 10]),
      degrees: new Float32Array([1, 1]),
      ndc: [
        { x: 0, y: 0, z: 0.5 },
        { x: 0, y: 0, z: 0.5 },
      ],
      collapsed: new Uint8Array([0, 0]),
    };
    const decisions = decideLod(state, 0, 10, cfg);
    expect(decisions[0].level).toBe(0);
    expect(decisions[0].isPoint).toBe(true);
    const upgraded = applyInteractionLod(decisions, 0);
    expect(upgraded[0].level).toBeGreaterThan(0);
    expect(upgraded[0].isPoint).toBe(false);
    expect(upgraded[0].showLabel).toBe(true);
    expect(upgraded[0].culled).toBe(false);
  });
  it('no-op when nothing is hovered', () => {
    const decisions = [{
      level: 0 as const,
      isPoint: true,
      isHidden: false,
      showLabel: false,
      culled: false,
    }];
    expect(applyInteractionLod(decisions, null)).toBe(decisions);
  });
});

describe('decideLod — combined rules', () => {
  it('far + slow frames + fast camera yields points with no labels', () => {
    const state: SceneState = {
      distances: new Float32Array([cfg.farThreshold + 100]),
      degrees: new Float32Array([1]),
      ndc: [{ x: 0, y: 0, z: 0.5 }],
      collapsed: new Uint8Array([0]),
    };
    const [d] = decideLod(state, cfg.veryFastSpeed + 1, cfg.verySlowFrameMs + 1, cfg);
    expect(d.level).toBe(0);
    expect(d.isPoint).toBe(true);
    expect(d.showLabel).toBe(false);
    expect(d.culled).toBe(false);
  });
  it('collapsed distant hub is hidden', () => {
    const state: SceneState = {
      distances: new Float32Array([cfg.farThreshold + 100]),
      degrees: new Float32Array([cfg.clusterMinDegree]),
      ndc: [{ x: 0, y: 0, z: 0.5 }],
      collapsed: new Uint8Array([1]),
    };
    const [d] = decideLod(state, 0, 10, cfg);
    expect(d.isHidden).toBe(true);
  });
  it('off-screen nodes are culled even when near', () => {
    const state: SceneState = {
      distances: new Float32Array([10]),
      degrees: new Float32Array([1]),
      ndc: [{ x: 50, y: 0, z: 0.5 }],
      collapsed: new Uint8Array([0]),
    };
    const [d] = decideLod(state, 0, 10, cfg);
    expect(d.culled).toBe(true);
    expect(d.showLabel).toBe(false);
  });
});

describe('dist3', () => {
  it('computes euclidean distance', () => {
    expect(dist3(0, 0, 0, 3, 4, 0)).toBe(5);
  });
});
