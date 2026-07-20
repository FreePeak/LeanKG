import Graph from 'graphology';
import { NODE_COLORS, NODE_SIZES, EDGE_STYLES } from './constants';
import type { KnowledgeGraph } from '../core/graph/types';
import { calculateTreeLayout } from './tree-layout';
import { calculateCirclesLayout } from './circles-layout';

export interface SigmaNodeAttributes {
  x: number;
  y: number;
  size: number;
  color: string;
  label: string;
  nodeType: string;
  filePath: string;
  startLine?: number;
  endLine?: number;
  hidden?: boolean;
  zIndex?: number;
  highlighted?: boolean;
  mass?: number;
  community?: number;
  treeLayer?: number;
  circlesRing?: number;
}

export interface SigmaEdgeAttributes {
  size: number;
  color: string;
  relationType: string;
  type?: string;
  curvature?: number;
  zIndex?: number;
  weight?: number;
  hidden?: boolean;
}

export interface KGNode {
  id: string;
  label: string;
  properties?: Record<string, unknown>;
}

export interface KGEdge {
  source_id?: string;
  sourceId?: string;
  target_id?: string;
  targetId?: string;
  type?: string;
  rel_type?: string;
}

const getScaledNodeSize = (baseSize: number, nodeCount: number): number => {
  if (nodeCount > 50000) return Math.max(1, baseSize * 0.4);
  if (nodeCount > 20000) return Math.max(1.5, baseSize * 0.5);
  if (nodeCount > 5000) return Math.max(2, baseSize * 0.65);
  if (nodeCount > 1000) return Math.max(2.5, baseSize * 0.8);
  return baseSize;
};

const getNodeMass = (nodeType: string, nodeCount: number): number => {
  const baseMassMultiplier = nodeCount > 5000 ? 2 : nodeCount > 1000 ? 1.5 : 1;

  // Handle null/undefined/empty
  if (!nodeType) {
    return 1;
  }

  // Handle Cluster types (e.g., "Cluster[14 files]")
  if (nodeType.startsWith('Cluster[')) {
    return 20 * baseMassMultiplier;
  }

  switch (nodeType) {
    case 'Service': return 25 * baseMassMultiplier;
    case 'Folder': return 15 * baseMassMultiplier;
    case 'File': return 3 * baseMassMultiplier;
    case 'Class':
    case 'Interface': return 5 * baseMassMultiplier;
    case 'Function':
    case 'Method': return 2 * baseMassMultiplier;
    default: return 1;
  }
};

const getNodeColor = (type: string): string => {
  // Handle null/undefined/empty
  if (!type) {
    return '#9ca3af';
  }

  // Check exact match first
  let color = NODE_COLORS[type];
  if (color && typeof color === 'string' && color.startsWith('#')) {
    return color;
  }

  // Handle Cluster[N files] pattern - extract base type
  if (type.startsWith('Cluster[')) {
    return '#f59e0b'; // Use amber for clusters
  }

  // Handle lowercase types
  if (type.length > 0) {
    color = NODE_COLORS[type.charAt(0).toUpperCase() + type.slice(1).toLowerCase()];
    if (color && typeof color === 'string' && color.startsWith('#')) {
      return color;
    }
  }

  // Default gray
  return '#9ca3af';
};

const addNodeToGraph = (
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  node: KGNode,
  nodeCount: number,
  x?: number,
  y?: number,
): void => {
  if (graph.hasNode(node.id)) return;

  const rawType = String(node.properties?.elementType || node.label || 'unknown') || 'unknown';

  // Handle Cluster[N files] pattern - use 'Cluster' as base type
  let type: string;
  let effectiveType: string;
  if (rawType.startsWith('Cluster[')) {
    type = 'Cluster';
    effectiveType = rawType; // Store full cluster type for filtering
  } else {
    // Normalize lowercase API types (function → Function, property → Property)
    const pascal =
      /^[A-Z]/.test(rawType) ? rawType : rawType.charAt(0).toUpperCase() + rawType.slice(1).toLowerCase();
    type = pascal;
    effectiveType = pascal;
  }

  const baseSize = NODE_SIZES[effectiveType] || NODE_SIZES[type] || 8;

  graph.addNode(node.id, {
    x: x ?? (Math.random() - 0.5) * 2000,
    y: y ?? (Math.random() - 0.5) * 2000,
    size: getScaledNodeSize(baseSize, nodeCount),
    color: getNodeColor(effectiveType),
    label: String(node.properties?.name || node.label || String(node.id).split('::').pop()),
    nodeType: effectiveType, // Use effectiveType for filtering (e.g., 'Cluster[14 files]')
    filePath: String((node.properties?.filePath || node.properties?.file_path || '') as string),
    startLine: (node.properties?.startLine ?? node.properties?.start_line) as number | undefined,
    endLine: (node.properties?.endLine ?? node.properties?.end_line) as number | undefined,
    hidden: false,
    mass: getNodeMass(type, nodeCount),
  });
};

export const createSigmaGraph = (
  kgNodes: KGNode[],
  kgEdges: KGEdge[]
): Graph<SigmaNodeAttributes, SigmaEdgeAttributes> => {
  const graph = new Graph<SigmaNodeAttributes, SigmaEdgeAttributes>();
  const nodeCount = kgNodes.length;

  const nodeMap = new Map(kgNodes.map((n) => [n.id, n]));

  const parentToChildren = new Map<string, string[]>();
  const childToParent = new Map<string, string>();

  kgEdges.forEach((rel) => {
    const sourceId = rel.source_id || rel.sourceId;
    const targetId = rel.target_id || rel.targetId;
    if (!sourceId || !targetId) return;

    const relType = (rel.type || rel.rel_type || 'UNKNOWN').toUpperCase();
    if (relType === 'CONTAINS' || relType === 'DEFINES' || relType === 'DECLARES') {
      if (!parentToChildren.has(sourceId)) {
        parentToChildren.set(sourceId, []);
      }
      parentToChildren.get(sourceId)!.push(targetId);
      if (!childToParent.has(targetId)) {
        childToParent.set(targetId, sourceId);
      }
    }
  });

  const rootNodes = kgNodes.filter((n) => !childToParent.has(n.id));

  const structuralSpread = Math.max(10, Math.sqrt(nodeCount) * 20);
  const nodePositions = new Map<string, { x: number; y: number }>();

  rootNodes.forEach((node) => {
    nodePositions.set(node.id, { x: 0, y: 0 });
  });

  const addNodeWithPosition = (nodeId: string, depth: number) => {
    if (graph.hasNode(nodeId)) return;
    const node = nodeMap.get(nodeId);
    if (!node) return;

    let x: number, y: number;
    const parentId = childToParent.get(nodeId);
    const parentPos = parentId ? nodePositions.get(parentId) : null;

    if (parentPos) {
      const childJitter = Math.max(50, structuralSpread / (depth + 1));
      x = parentPos.x + (Math.random() - 0.5) * childJitter;
      y = parentPos.y + (Math.random() - 0.5) * childJitter;
    } else {
      x = (Math.random() - 0.5) * structuralSpread;
      y = (Math.random() - 0.5) * structuralSpread;
    }
    nodePositions.set(nodeId, { x, y });

    addNodeToGraph(graph, node, nodeCount, x, y);
  };

  const queue: { id: string; depth: number }[] = rootNodes.map((n) => ({
    id: n.id,
    depth: 0,
  }));
  const visited = new Set<string>();

  while (queue.length > 0) {
    const { id: currentId, depth } = queue.shift()!;
    if (visited.has(currentId)) continue;
    visited.add(currentId);

    addNodeWithPosition(currentId, depth);

    const children = parentToChildren.get(currentId) || [];
    for (const childId of children) {
      if (!visited.has(childId)) {
        queue.push({ id: childId, depth: depth + 1 });
      }
    }
  }

  kgNodes.forEach((node) => {
    if (!graph.hasNode(node.id)) {
      addNodeToGraph(graph, node, nodeCount);
    }
  });

  const edgeBaseSize = nodeCount > 20000 ? 0.4 : nodeCount > 5000 ? 0.6 : 1.0;

  kgEdges.forEach((rel) => {
    // Handle both snake_case (API) and camelCase (interface) field names
    const sourceId = rel.source_id || rel.sourceId;
    const targetId = rel.target_id || rel.targetId;
    if (!sourceId || !targetId) return;

    if (graph.hasNode(sourceId) && graph.hasNode(targetId)) {
      if (!graph.hasEdge(sourceId, targetId)) {
        const relType = (rel.type || rel.rel_type || 'UNKNOWN').toUpperCase();
        const style = EDGE_STYLES[relType] || { color: '#4a4a5a', sizeMultiplier: 0.5 };
        const curvature = 0.12 + Math.random() * 0.08;
        const edgeColor =
          typeof style.color === 'string' && style.color.startsWith('#')
            ? style.color
            : '#4a4a5a';

        graph.addEdge(sourceId, targetId, {
          size: edgeBaseSize * style.sizeMultiplier,
          color: edgeColor,
          relationType: relType,
          type: 'curved',
          curvature,
          weight: relType === 'CONTAINS' ? 2.5 : 0.5,
        });
      }
    }
  });

  return graph;
};

export const filterGraphByLabels = (
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  visibleLabels: string[],
): void => {
  const normalizedVisible = new Set(visibleLabels);
  if (normalizedVisible.has('Folder')) normalizedVisible.add('Directory');
  if (normalizedVisible.has('Directory')) normalizedVisible.add('Folder');

  graph.forEachNode((nodeId, attributes) => {
    const nodeType = attributes.nodeType;
    let isVisible = normalizedVisible.has(nodeType);

    if (!isVisible && nodeType.startsWith('Cluster[')) {
      isVisible = normalizedVisible.has('Cluster') ||
        visibleLabels.some(label => nodeType === label);
    }

    graph.setNodeAttribute(nodeId, 'hidden', !isVisible);
  });
};

export const getNodesWithinHops = (
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  startNodeId: string,
  maxHops: number,
): Set<string> => {
  const visited = new Set<string>();
  const queue: { nodeId: string; depth: number }[] = [
    { nodeId: startNodeId, depth: 0 },
  ];

  while (queue.length > 0) {
    const { nodeId, depth } = queue.shift()!;
    if (visited.has(nodeId)) continue;
    visited.add(nodeId);

    if (depth < maxHops) {
      graph.forEachNeighbor(nodeId, (neighborId) => {
        if (!visited.has(neighborId)) {
          queue.push({ nodeId: neighborId, depth: depth + 1 });
        }
      });
    }
  }
  return visited;
};

export const filterGraphByDepth = (
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  selectedNodeId: string | null,
  maxHops: number | null,
  visibleLabels: string[],
): void => {
  if (maxHops === null) {
    filterGraphByLabels(graph, visibleLabels);
    return;
  }
  if (selectedNodeId === null || !graph.hasNode(selectedNodeId)) {
    filterGraphByLabels(graph, visibleLabels);
    return;
  }
  const nodesInRange = getNodesWithinHops(graph, selectedNodeId, maxHops);
  graph.forEachNode((nodeId, attributes) => {
    const isLabelVisible = visibleLabels.includes(attributes.nodeType);
    const isInRange = nodesInRange.has(nodeId);
    graph.setNodeAttribute(nodeId, 'hidden', !isLabelVisible || !isInRange);
  });
};

export const setNodeExpanded = (
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  nodeId: string,
  expanded: boolean,
): void => {
  if (!graph.hasNode(nodeId)) return;
  graph.forEachNeighbor(nodeId, (neighborId) => {
    graph.setNodeAttribute(neighborId, 'hidden', !expanded);
  });
};

export const getFileFunctions = (
  kgNodes: KGNode[],
  kgEdges: KGEdge[],
  fileId: string
): KGNode[] => {
  const functionIds = new Set<string>();
  kgEdges.forEach((rel) => {
    const sourceId = rel.source_id || rel.sourceId;
    const targetId = rel.target_id || rel.targetId;
    if (!sourceId || !targetId) return;
    if (sourceId === fileId && (rel.type || rel.rel_type || '').toUpperCase() === 'DEFINES') {
      functionIds.add(targetId);
    }
  });
  return kgNodes.filter((node) => functionIds.has(node.id));
};

export const getNodeRelationships = (
  kgNodes: KGNode[],
  kgEdges: KGEdge[],
  nodeId: string
): {
  defines: KGNode[];
  callsFrom: KGEdge[];
  callsTo: KGEdge[];
  imports: KGEdge[];
} => {
  const defines: KGNode[] = [];
  const callsFrom: KGEdge[] = [];
  const callsTo: KGEdge[] = [];
  const imports: KGEdge[] = [];

  const fileFunctionIds = new Set<string>();
  kgEdges.forEach((rel) => {
    const sourceId = rel.source_id || rel.sourceId;
    const targetId = rel.target_id || rel.targetId;
    if (!sourceId || !targetId) return;
    if (sourceId === nodeId && (rel.type || rel.rel_type || '').toUpperCase() === 'DEFINES') {
      fileFunctionIds.add(targetId);
    }
  });

  kgEdges.forEach((rel) => {
    const sourceId = rel.source_id || rel.sourceId;
    const targetId = rel.target_id || rel.targetId;
    if (!sourceId || !targetId) return;

    const relType = (rel.type || rel.rel_type || '').toUpperCase();
    if (relType === 'CALLS') {
      if (sourceId === nodeId || fileFunctionIds.has(sourceId)) {
        callsFrom.push(rel);
      }
      if (targetId === nodeId || fileFunctionIds.has(targetId)) {
        callsTo.push(rel);
      }
    } else if (relType === 'IMPORTS') {
      if (sourceId === nodeId || fileFunctionIds.has(sourceId)) {
        imports.push(rel);
      }
    }
  });

  kgNodes.forEach((node) => {
    if (fileFunctionIds.has(node.id)) {
      defines.push(node);
    }
  });

  return { defines, callsFrom, callsTo, imports };
};

export type LayoutMode = 'force' | 'tree' | 'circles';

function kgEdgesFromKnowledgeGraph(kg: KnowledgeGraph): KGEdge[] {
  return kg.relationships.map((r) => ({
    sourceId: r.sourceId,
    targetId: r.targetId,
    type: r.type,
  }));
}

function kgNodesFromKnowledgeGraph(kg: KnowledgeGraph): KGNode[] {
  return kg.nodes.map((n) => ({
    id: n.id,
    label: n.label,
    properties: n.properties as Record<string, unknown>,
  }));
}

/** Build graphology graph for the selected layout mode. */
export function buildLayoutGraph(
  kg: KnowledgeGraph,
  mode: LayoutMode,
): Graph<SigmaNodeAttributes, SigmaEdgeAttributes> {
  if (mode === 'force') {
    return createSigmaGraph(kgNodesFromKnowledgeGraph(kg), kgEdgesFromKnowledgeGraph(kg));
  }

  const positions =
    mode === 'tree' ? calculateTreeLayout(kg) : calculateCirclesLayout(kg);
  const graph = new Graph<SigmaNodeAttributes, SigmaEdgeAttributes>();
  const nodeCount = kg.nodes.length;

  for (const node of kg.nodes) {
    if (graph.hasNode(node.id)) continue;
    const pos = positions.get(node.id);
    const rawType = String(node.properties?.elementType || node.label || 'unknown');
    const type = /^[A-Z]/.test(rawType)
      ? rawType
      : rawType.charAt(0).toUpperCase() + rawType.slice(1).toLowerCase();
    const baseSize = NODE_SIZES[type] || (pos?.size ?? 6);
    graph.addNode(node.id, {
      x: pos?.x ?? Math.random() * 100,
      y: pos?.y ?? Math.random() * 100,
      size: getScaledNodeSize(baseSize, nodeCount),
      color: NODE_COLORS[type] || '#9ca3af',
      label: String(node.properties?.name || node.label || node.id),
      nodeType: type,
      filePath: String(node.properties?.filePath || ''),
      startLine: node.properties?.startLine as number | undefined,
      endLine: node.properties?.endLine as number | undefined,
      hidden: false,
      mass: 1,
      treeLayer: mode === 'tree' ? (pos as { depth?: number } | undefined)?.depth : undefined,
      circlesRing: mode === 'circles' ? (pos as { ring?: number } | undefined)?.ring : undefined,
    });
  }

  for (const rel of kg.relationships) {
    if (!graph.hasNode(rel.sourceId) || !graph.hasNode(rel.targetId)) continue;
    if (graph.hasEdge(rel.sourceId, rel.targetId)) continue;
    const style = EDGE_STYLES[rel.type] || { color: '#4a4a5a', sizeMultiplier: 0.5 };
    graph.addEdge(rel.sourceId, rel.targetId, {
      size: 1.5 * style.sizeMultiplier,
      color: style.color,
      relationType: rel.type,
      type: 'curved',
      curvature: 0.2,
      weight: 1,
    });
  }

  return graph;
}
