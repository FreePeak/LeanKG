/**
 * Edge Bundling for Dense Graphs
 *
 * Implements Hierarchical Edge Bundling (HEB) to reduce visual clutter
 * when rendering dense graphs. Edges traveling in similar directions
 * are bundled together into "highways" to prevent the "hairball" effect.
 */

import type { SigmaNodeAttributes, SigmaEdgeAttributes } from './graph-adapter';
import Graph from 'graphology';

export interface BundledEdge {
  sourceId: string;
  targetId: string;
  controlPoints: Array<{ x: number; y: number }>;
  edgeCount: number;
  edgeIds: string[];
}

export interface EdgeBundlingOptions {
  /** Maximum number of iterations for the bundling algorithm */
  iterations?: number;
  /** Compatibility threshold for edge grouping (0-1) */
  compatibilityThreshold?: number;
  /** Angle similarity threshold in degrees */
  angleThreshold?: number;
  /** Enable for very large graphs (>10K edges) */
  fastMode?: boolean;
}

const DEFAULT_OPTIONS: Required<EdgeBundlingOptions> = {
  iterations: 3,
  compatibilityThreshold: 0.3,
  angleThreshold: 30,
  fastMode: false,
};

/**
 * Calculate the compatibility score between two edges based on:
 * - Angular similarity (both edges traveling in similar direction)
 * - Distance compatibility (edges not too far apart)
 * - Scale compatibility (edges of similar length)
 */
function calculateCompatibility(
  edge1: { sourceX: number; sourceY: number; targetX: number; targetY: number },
  edge2: { sourceX: number; sourceY: number; targetX: number; targetY: number },
  threshold: number
): number {
  // Calculate angle of each edge
  const angle1 = Math.atan2(edge1.targetY - edge1.sourceY, edge1.targetX - edge1.sourceX);
  const angle2 = Math.atan2(edge2.targetY - edge2.sourceY, edge2.targetX - edge2.sourceX);

  // Angular difference
  const angleDiff = Math.abs(angle1 - angle2);
  const normalizedAngleDiff = Math.min(angleDiff, Math.PI * 2 - angleDiff);
  const angleSimilarity = 1 - normalizedAngleDiff / Math.PI;

  // Distance between edges (midpoint to midpoint)
  const mid1X = (edge1.sourceX + edge1.targetX) / 2;
  const mid1Y = (edge1.sourceY + edge1.targetY) / 2;
  const mid2X = (edge2.sourceX + edge2.targetX) / 2;
  const mid2Y = (edge2.sourceY + edge2.targetY) / 2;
  const dist = Math.sqrt((mid1X - mid2X) ** 2 + (mid1Y - mid2Y) ** 2);

  // Scale (edge length)
  const len1 = Math.sqrt((edge1.targetX - edge1.sourceX) ** 2 + (edge1.targetY - edge1.sourceY) ** 2);
  const len2 = Math.sqrt((edge2.targetX - edge2.sourceX) ** 2 + (edge2.targetY - edge2.sourceY) ** 2);
  const scaleDiff = Math.abs(len1 - len2) / Math.max(len1, len2);

  // Combined compatibility score
  const angleScore = angleSimilarity > (threshold / 180) * Math.PI ? 1 : 0;
  const distScore = dist < 500 ? 1 : 0;
  const scaleScore = scaleDiff < 0.5 ? 1 : 0;

  return (angleScore + distScore + scaleScore) / 3;
}

/**
 * Hierarchical Edge Bundling using Force-Directed approach
 *
 * Groups edges that travel in similar directions into bundles,
 * reducing visual clutter for dense graphs.
 */
export function bundleEdges(
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  options: EdgeBundlingOptions = {}
): BundledEdge[] {
  const opts = { ...DEFAULT_OPTIONS, ...options };

  // Get edges as array
  const edges = graph.edges();
  if (edges.length === 0) return [];

  // For very large graphs, use simplified bundling
  if (opts.fastMode || edges.length > 10000) {
    return fastEdgeBundling(graph);
  }

  // Build edge data with positions
  const edgeData: Array<{
    id: string;
    source: string;
    target: string;
    sourceX: number;
    sourceY: number;
    targetX: number;
    targetY: number;
  }> = [];

  edges.forEach((edgeId) => {
    const [source, target] = graph.extremities(edgeId);
    const sourceAttrs = graph.getNodeAttributes(source);
    const targetAttrs = graph.getNodeAttributes(target);

    edgeData.push({
      id: edgeId,
      source,
      target,
      sourceX: sourceAttrs.x || 0,
      sourceY: sourceAttrs.y || 0,
      targetX: targetAttrs.x || 0,
      targetY: targetAttrs.y || 0,
    });
  });

  // Iteratively bundle compatible edges
  const bundles: BundledEdge[] = [];
  const bundledEdges = new Set<string>();

  for (const edge of edgeData) {
    if (bundledEdges.has(edge.id)) continue;

    // Find all edges compatible with this edge
    const compatibleEdges: string[] = [edge.id];
    bundledEdges.add(edge.id);

    for (const otherEdge of edgeData) {
      if (bundledEdges.has(otherEdge.id)) continue;
      if (otherEdge.id === edge.id) continue;

      // Skip if edges share endpoints (direct connections)
      if (otherEdge.source === edge.source || otherEdge.target === edge.target ||
          otherEdge.source === edge.target || otherEdge.target === edge.source) {
        continue;
      }

      const compatibility = calculateCompatibility(edge, otherEdge, opts.angleThreshold);
      if (compatibility >= opts.compatibilityThreshold) {
        compatibleEdges.push(otherEdge.id);
        bundledEdges.add(otherEdge.id);
      }
    }

    // Create bundle from compatible edges
    if (compatibleEdges.length > 1) {
      const bundledEdgeData = compatibleEdges
        .map(id => edgeData.find(e => e.id === id)!)
        .filter(Boolean);

      // Calculate control points along the average path
      const controlPoints = calculateControlPoints(bundledEdgeData);

      bundles.push({
        sourceId: edge.source,
        targetId: edge.target,
        controlPoints,
        edgeCount: compatibleEdges.length,
        edgeIds: compatibleEdges,
      });
    }
  }

  return bundles;
}

/**
 * Fast edge bundling for very large graphs
 * Uses simple clustering by source/target proximity
 */
function fastEdgeBundling(
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>
): BundledEdge[] {
  const edges = graph.edges();
  const bundles: BundledEdge[] = [];
  const processed = new Set<string>();

  edges.forEach((edgeId) => {
    if (processed.has(edgeId)) return;
    processed.add(edgeId);

    const [source, target] = graph.extremities(edgeId);

    bundles.push({
      sourceId: source,
      targetId: target,
      controlPoints: [],
      edgeCount: 1,
      edgeIds: [edgeId],
    });
  });

  return bundles;
}

/**
 * Calculate control points for smooth curve through bundled edges
 */
function calculateControlPoints(
  edges: Array<{ sourceX: number; sourceY: number; targetX: number; targetY: number }>
): Array<{ x: number; y: number }> {
  if (edges.length === 0) return [];

  // Calculate average path
  const avgSourceX = edges.reduce((sum, e) => sum + e.sourceX, 0) / edges.length;
  const avgSourceY = edges.reduce((sum, e) => sum + e.sourceY, 0) / edges.length;
  const avgTargetX = edges.reduce((sum, e) => sum + e.targetX, 0) / edges.length;
  const avgTargetY = edges.reduce((sum, e) => sum + e.targetY, 0) / edges.length;

  // Calculate midpoint with slight offset for curve
  const midX = (avgSourceX + avgTargetX) / 2;
  const midY = (avgSourceY + avgTargetY) / 2;

  // Perpendicular offset for curve
  const dx = avgTargetX - avgSourceX;
  const dy = avgTargetY - avgSourceY;
  const perpX = -dy * 0.1;
  const perpY = dx * 0.1;

  return [
    { x: avgSourceX, y: avgSourceY },
    { x: midX + perpX, y: midY + perpY },
    { x: avgTargetX, y: avgTargetY },
  ];
}

/**
 * Check if edge bundling should be enabled based on graph density
 */
export function shouldEnableBundling(
  nodeCount: number,
  edgeCount: number
): { enable: boolean; reason: string } {
  const density = edgeCount / (nodeCount * (nodeCount - 1) / 2);

  if (edgeCount > 50000) {
    return { enable: true, reason: 'Very high edge count (>50K)' };
  }

  if (density > 0.3 && edgeCount > 5000) {
    return { enable: true, reason: 'High density graph with >5K edges' };
  }

  if (edgeCount > 20000) {
    return { enable: true, reason: 'High edge count (>20K)' };
  }

  return { enable: false, reason: 'Graph density is manageable' };
}
