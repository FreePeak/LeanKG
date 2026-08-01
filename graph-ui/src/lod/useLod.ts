import { useMemo } from 'react';
import { Camera } from 'three';
import {
  DEFAULT_LOD_CONFIG,
  applyInteractionLod,
  decideLod,
  dist3,
  isCulled,
  type LODConfig,
  type LodDecision,
} from '../lod/lod';

export interface LodRenderState {
  decisions: LodDecision[];
  /** Indices of nodes that survive culling + collapse (renderable). */
  visible: number[];
  /** World-space distance from the camera (for culling). */
  cameraDistance: number;
}

/**
 * Compute per-node LOD decisions for the current frame.
 * Pure-ish: derives SceneState from the node positions and camera.
 */
export function computeLodState(
  nodes: { x: number; y: number; z: number }[],
  degrees: Float32Array,
  camera: Camera,
  frameMs: number,
  speed: number,
  interactiveIndex: number | null,
  cfg: LODConfig = DEFAULT_LOD_CONFIG,
): LodRenderState {
  const count = nodes.length;
  const distances = new Float32Array(count);
  const collapsed = new Uint8Array(count);
  const ndc: { x: number; y: number; z: number }[] = new Array(count);
  const camPos = camera.position;
  const matrix = camera.matrixWorldInverse;
  const proj = camera.projectionMatrix;

  const cameraDistance = Math.sqrt(
    camPos.x * camPos.x + camPos.y * camPos.y + camPos.z * camPos.z,
  );

  for (let i = 0; i < count; i++) {
    const n = nodes[i];
    distances[i] = dist3(n.x, n.y, n.z, camPos.x, camPos.y, camPos.z);
    collapsed[i] = degrees[i] >= cfg.clusterMinDegree ? 1 : 0;
    // View-space transform, then perspective divide -> NDC.
    const vx = matrix.elements[0] * n.x + matrix.elements[4] * n.y + matrix.elements[8] * n.z + matrix.elements[12];
    const vy = matrix.elements[1] * n.x + matrix.elements[5] * n.y + matrix.elements[9] * n.z + matrix.elements[13];
    const vz = matrix.elements[2] * n.x + matrix.elements[6] * n.y + matrix.elements[10] * n.z + matrix.elements[14];
    const vw = matrix.elements[3] * n.x + matrix.elements[7] * n.y + matrix.elements[11] * n.z + matrix.elements[15];
    const w = vw !== 0 ? vw : 1e-6;
    const px = proj.elements[0] * vx + proj.elements[8] * vz;
    const py = proj.elements[5] * vy + proj.elements[9] * vz;
    const pz = proj.elements[10] * vz + proj.elements[14] * vw;
    ndc[i] = { x: px / w, y: py / w, z: pz / w };
  }

  const decisions = decideLod(
    { distances, degrees, ndc, collapsed },
    speed,
    frameMs,
    cfg,
  );
  const boosted = applyInteractionLod(decisions, interactiveIndex);

  const visible: number[] = [];
  for (let i = 0; i < count; i++) {
    const d = boosted[i];
    if (!d.culled && !d.isHidden) visible.push(i);
  }
  return { decisions: boosted, visible, cameraDistance };
}

/** Per-frame camera speed from position deltas. */
export function trackCameraSpeed(
  pos: { x: number; y: number; z: number },
  lastPos: { x: number; y: number; z: number } | null,
  dtMs: number,
): number {
  if (!lastPos) return 0;
  const dt = Math.max(dtMs, 1) / 1000;
  return dist3(pos.x, pos.y, pos.z, lastPos.x, lastPos.y, lastPos.z) / dt;
}

export function useLodState(
  nodes: { x: number; y: number; z: number }[],
  degrees: Float32Array,
  camera: Camera | null,
  frameMs: number,
  speed: number,
  interactiveIndex: number | null,
  cfg: LODConfig = DEFAULT_LOD_CONFIG,
): LodRenderState {
  return useMemo(
    () =>
      computeLodState(
        nodes,
        degrees,
        camera ?? new Camera(),
        frameMs,
        speed,
        interactiveIndex,
        cfg,
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [nodes, degrees, camera, frameMs, speed, interactiveIndex],
  );
}

export { isCulled };
