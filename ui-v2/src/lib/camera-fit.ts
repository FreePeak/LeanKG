/**
 * Fit Sigma camera to the graph using Sigma's own animatedReset.
 * Custom graph-space setState is wrong for Sigma v3 camera coordinates
 * (ends up looking at empty space while animatedReset fills the view).
 */

import type Sigma from 'sigma';
import type Graph from 'graphology';

export function fitSigmaToGraph(
  sigma: Sigma,
  _graph?: Graph,
  opts?: { animate?: boolean; duration?: number },
): boolean {
  sigma.resize();
  const container = sigma.getContainer();
  const vw = container?.clientWidth || 0;
  const vh = container?.clientHeight || 0;
  if (vw < 32 || vh < 32) return false;

  const camera = sigma.getCamera();
  const duration = opts?.animate === false ? 0 : (opts?.duration ?? 450);
  camera.animatedReset({ duration });
  sigma.refresh();
  return true;
}

/** Used by unit tests — bounding box helpers for layout assertions. */
export interface GraphBounds {
  minX: number;
  maxX: number;
  minY: number;
  maxY: number;
  width: number;
  height: number;
  cx: number;
  cy: number;
}

export function computeGraphBounds(
  graph: Graph,
  opts?: { paddingFactor?: number },
): GraphBounds | null {
  if (graph.order === 0) return null;
  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  graph.forEachNode((_id, attrs) => {
    const x = Number(attrs.x);
    const y = Number(attrs.y);
    if (!Number.isFinite(x) || !Number.isFinite(y)) return;
    if (x < minX) minX = x;
    if (x > maxX) maxX = x;
    if (y < minY) minY = y;
    if (y > maxY) maxY = y;
  });
  if (!Number.isFinite(minX) || !Number.isFinite(maxX)) return null;
  const width = Math.max(maxX - minX, 1);
  const height = Math.max(maxY - minY, 1);
  const pad = opts?.paddingFactor ?? 1.15;
  return {
    minX,
    maxX,
    minY,
    maxY,
    width: width * pad,
    height: height * pad,
    cx: (minX + maxX) / 2,
    cy: (minY + maxY) / 2,
  };
}

export function cameraRatioForBounds(
  bounds: GraphBounds,
  containerWidth: number,
  containerHeight: number,
): number {
  const vw = Math.max(containerWidth, 1);
  const vh = Math.max(containerHeight, 1);
  return Math.max(bounds.width / vw, bounds.height / vh);
}

/** Normalize expand-service folder paths so project root expands correctly. */
export function normalizeExpandPath(path: string, projectPath?: string): string {
  const trimmed = path.trim();
  if (!trimmed || trimmed === '.' || trimmed === './' || trimmed === '/') return '.';
  if (projectPath) {
    const root = projectPath.replace(/\/$/, '');
    if (trimmed === root || trimmed === `${root}/`) return '.';
    if (trimmed.startsWith(`${root}/`)) {
      const rest = trimmed.slice(root.length + 1);
      return rest ? `./${rest}` : '.';
    }
  }
  if (trimmed === './') return '.';
  return trimmed;
}
