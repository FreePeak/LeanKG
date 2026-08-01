/**
 * Scene level-of-detail (LOD) core — FR-E20..E28.
 *
 * Pure functions: LOD level selection, cluster collapse, progressive batching,
 * label LOD, occlusion culling, frame-budget adaptation, camera-speed LOD,
 * importance-based sizing, interaction LOD.
 *
 * Units: distances in world units, speeds in world units / second.
 */

export type LODLevel = 0 | 1 | 2;
/** 0 = point sprite, 1 = small sphere, 2 = full sphere + detail. */
export const LOD_LEVELS: readonly LODLevel[] = [0, 1, 2];

export interface LODConfig {
  /** Distance thresholds between levels. */
  nearThreshold: number;
  farThreshold: number;
  /** Speed above which the camera drops one LOD level (FR-E26). */
  fastSpeed: number;
  /** Speed above which the camera drops two LOD levels. */
  veryFastSpeed: number;
  /** ms per frame budget (FR-E25). */
  frameBudgetMs: number;
  /** ms above which quality drops one level. */
  slowFrameMs: number;
  /** ms above which quality drops two levels. */
  verySlowFrameMs: number;
  /** Node count at which progressive loading kicks in (FR-E22). */
  progressiveNodeCount: number;
  /** Nodes loaded per batch (FR-E22). */
  batchSize: number;
  /** Distance beyond which labels are hidden (FR-E23). */
  labelDistance: number;
  /** View half-diagonal (radians) of the camera frustum, e.g. 1.2. */
  fovHalfRad: number;
  /** World radius (distance to the most distant node) for culling (FR-E24). */
  worldRadius: number;
  /** NDC clamp (0..1) used to push off-screen nodes to the culled list. */
  ndcClamp: number;
  /** Min degree treated as a cluster hub (FR-E21). */
  clusterMinDegree: number;
  /** Multiplier applied to level when camera is fast (FR-E26). */
  speedLevelBonus: number;
}

export const DEFAULT_LOD_CONFIG: LODConfig = {
  nearThreshold: 120,
  farThreshold: 260,
  fastSpeed: 60,
  veryFastSpeed: 150,
  frameBudgetMs: 16.7,
  slowFrameMs: 24,
  verySlowFrameMs: 40,
  progressiveNodeCount: 1200,
  batchSize: 240,
  labelDistance: 90,
  fovHalfRad: 1.2,
  worldRadius: 400,
  ndcClamp: 1.4,
  clusterMinDegree: 8,
  speedLevelBonus: 1,
};

export interface SceneState {
  /** Distance from camera to each node (world units). */
  distances: Float32Array;
  /** Node degree (FR-E27 fallback when backend lacks god score). */
  degrees: Float32Array;
  /** Node NDC positions after projection (x, y in [-1, 1], z = depth). */
  ndc: { x: number; y: number; z: number }[];
  /** True if the node is part of a collapsed cluster (FR-E21). */
  collapsed: Uint8Array;
}

export interface LodDecision {
  level: LODLevel;
  /** True when the node is a point sprite (level 0). */
  isPoint: boolean;
  /** True when the node is inside a collapsed cluster (hidden). */
  isHidden: boolean;
  /** True when a label should render (FR-E23). */
  showLabel: boolean;
  /** True when the node is outside the frustum (FR-E24). */
  culled: boolean;
}

/** FR-E25: adaptive quality from frame time. Returns an LOD level penalty. */
export function frameQualityPenalty(frameMs: number, cfg: LODConfig): number {
  if (frameMs >= cfg.verySlowFrameMs) return 2;
  if (frameMs >= cfg.slowFrameMs) return 1;
  return 0;
}

/** FR-E26: camera-speed LOD — faster movement lowers detail. */
export function cameraSpeedPenalty(speed: number, cfg: LODConfig): number {
  if (speed >= cfg.veryFastSpeed) return 2;
  if (speed >= cfg.fastSpeed) return 1;
  return 0;
}

/** Clamp a level to [0, 2] and return the numeric value. */
function clampLevel(level: number): LODLevel {
  if (level <= 0) return 0;
  if (level >= 2) return 2;
  return Math.round(level) as LODLevel;
}

/**
 * Base LOD level by camera distance (FR-E20):
 * 2 near, 1 mid, 0 far (points).
 */
export function levelByDistance(distance: number, cfg: LODConfig): LODLevel {
  if (distance <= cfg.nearThreshold) return 2;
  if (distance <= cfg.farThreshold) return 1;
  return 0;
}

/** Combined LOD level: distance, then penalties from frame budget + speed. */
export function selectLodLevel(
  distance: number,
  speed: number,
  frameMs: number,
  cfg: LODConfig,
): LODLevel {
  const base = levelByDistance(distance, cfg);
  const penalty =
    frameQualityPenalty(frameMs, cfg) + cameraSpeedPenalty(speed, cfg);
  return clampLevel(base - penalty);
}

/** FR-E21: cluster collapse — nodes behind the threshold collapse to a glyph. */
export function clusterCollapse(node: { degree: number }, cfg: LODConfig): boolean {
  return node.degree >= cfg.clusterMinDegree;
}

/** FR-E23: label LOD — labels only near camera. */
export function showLabel(distance: number, cfg: LODConfig): boolean {
  return distance <= cfg.labelDistance;
}

/** FR-E24: occlusion culling — skip nodes outside the frustum (NDC clamp). */
export function isCulled(
  ndc: { x: number; y: number; z: number },
  cfg: LODConfig,
): boolean {
  const c = cfg.ndcClamp;
  return ndc.x < -c || ndc.x > c || ndc.y < -c || ndc.y > c || ndc.z > 1;
}

/** FR-E27: node size by importance — god score if exposed, else degree. */
export function importanceSize(
  godScore: number | undefined,
  degree: number,
  min = 0.5,
  max = 3,
): number {
  const score = godScore ?? degree;
  if (score <= 0) return min;
  // Squash to [min, max]: 1 -> min, 10+ -> max, degrees in between scale.
  return Math.min(max, Math.max(min, min + Math.log2(1 + score)));
}

/** FR-E22: progressive loading — split nodes into ordered batches. */
export function batchNodes(
  count: number,
  batchSize: number,
  cfg: LODConfig,
): number[] {
  if (count <= cfg.progressiveNodeCount) return [count];
  const batches: number[] = [];
  for (let loaded = 0; loaded < count; loaded += batchSize) {
    batches.push(Math.min(batchSize, count - loaded));
  }
  return batches;
}

/**
 * Per-node LOD decision combining all rules. Order matters:
 * collapse (hide) > cull (skip render) > level > label.
 */
export function decideLod(
  state: SceneState,
  speed: number,
  frameMs: number,
  cfg: LODConfig,
): LodDecision[] {
  const penalty =
    frameQualityPenalty(frameMs, cfg) + cameraSpeedPenalty(speed, cfg);
  const decisions: LodDecision[] = new Array(state.distances.length);
  for (let i = 0; i < state.distances.length; i++) {
    const dist = state.distances[i];
    const collapsed = state.collapsed[i] === 1;
    const hidden = collapsed && dist > cfg.nearThreshold;
    const ndc = state.ndc[i];
    const culled = isCulled(ndc, cfg);
    const level = clampLevel(levelByDistance(dist, cfg) - penalty);
    decisions[i] = {
      level,
      isPoint: level === 0,
      isHidden: hidden,
      showLabel: !hidden && !culled && showLabel(dist, cfg),
      culled,
    };
  }
  return decisions;
}

/** FR-E28: interaction LOD — hovered/selected node upgrades one level. */
export function applyInteractionLod(
  decisions: LodDecision[],
  interactiveIndex: number | null,
  boost = 1,
): LodDecision[] {
  if (interactiveIndex === null) return decisions;
  const out = decisions.slice();
  const d = out[interactiveIndex];
  if (d && !d.isHidden) {
    out[interactiveIndex] = {
      ...d,
      level: clampLevel(d.level + boost),
      isPoint: false,
      showLabel: true,
      culled: false,
    };
  }
  return out;
}

/** Distance between two 3D points (world units). */
export function dist3(
  ax: number,
  ay: number,
  az: number,
  bx: number,
  by: number,
  bz: number,
): number {
  const dx = ax - bx;
  const dy = ay - by;
  const dz = az - bz;
  return Math.sqrt(dx * dx + dy * dy + dz * dz);
}
