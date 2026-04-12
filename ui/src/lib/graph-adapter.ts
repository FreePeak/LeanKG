import Graph from 'graphology';
import louvain from 'graphology-communities-louvain';
import { NODE_COLORS, NODE_SIZES, EDGE_STYLES } from './constants';

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
}

export interface SigmaEdgeAttributes {
  size: number;
  color: string;
  relationType: string;
  type?: string;
  curvature?: number;
  zIndex?: number;
  weight?: number;
}

export interface KGNode {
  id: string;
  label: string;
  properties?: any;
}

export interface KGEdge {
  sourceId: string;
  targetId: string;
  type: string;
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
  switch (nodeType) {
    case 'Project': return 50 * baseMassMultiplier;
    case 'Package': return 30 * baseMassMultiplier;
    case 'Module': return 20 * baseMassMultiplier;
    case 'Folder': return 15 * baseMassMultiplier;
    case 'File': return 3 * baseMassMultiplier;
    case 'Class':
    case 'Interface': return 5 * baseMassMultiplier;
    case 'Function':
    case 'Method': return 2 * baseMassMultiplier;
    default: return 1;
  }
};

export const createSigmaGraph = (
  kgNodes: KGNode[],
  kgEdges: KGEdge[]
): Graph<SigmaNodeAttributes, SigmaEdgeAttributes> => {
  const graph = new Graph<SigmaNodeAttributes, SigmaEdgeAttributes>();
  const nodeCount = kgNodes.length;

  const parentToChildren = new Map<string, string[]>();
  const childToParent = new Map<string, string>();
  // Remove IMPORTS, keep structural edges only
  const hierarchyRelations = new Set(['CONTAINS', 'DEFINES', 'DECLARES']);

  kgEdges.forEach((rel) => {
    const relType = rel.type.toUpperCase();
    if (hierarchyRelations.has(relType)) {
      if (!parentToChildren.has(rel.sourceId)) {
        parentToChildren.set(rel.sourceId, []);
      }
      parentToChildren.get(rel.sourceId)!.push(rel.targetId);
      if (!childToParent.has(rel.targetId)) {
        childToParent.set(rel.targetId, rel.sourceId);
      }
    }
  });

  const nodeMap = new Map(kgNodes.map((n) => [n.id, n]));
  const rootNodes = kgNodes.filter((n) => !childToParent.has(n.id));

  const structuralSpread = Math.max(10, Math.sqrt(nodeCount) * 80);
  const nodePositions = new Map<string, { x: number; y: number }>();

  // Place structural root nodes strictly at the center 
  // ForceAtlas2 will naturally and accurately repel them. If we spread them mathematically,
  // empty outlier roots create invisible bounds that skews the camera off-center.
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
      // Place children mathematically farther out to instantly fill the screen symmetrically
      // Use higher dispersion for a pre-balanced look natively
      const childJitter = Math.max(50, structuralSpread / (depth + 1));
      x = parentPos.x + (Math.random() - 0.5) * childJitter;
      y = parentPos.y + (Math.random() - 0.5) * childJitter;
    } else if (!nodePositions.has(nodeId)) {
      x = (Math.random() - 0.5) * structuralSpread;
      y = (Math.random() - 0.5) * structuralSpread;
      nodePositions.set(nodeId, { x, y });
    } else {
      const pos = nodePositions.get(nodeId)!;
      x = pos.x; 
      y = pos.y;
    }

    if (!nodePositions.has(nodeId)) nodePositions.set(nodeId, { x, y });

    const rawType = String(node.properties?.elementType || node.label || 'unknown') || 'unknown';
    const type = (rawType.charAt(0).toUpperCase() + rawType.slice(1)) || 'unknown';
    const baseSize = NODE_SIZES[type] || 8;

    // Ensure color is always a valid hex string
    let nodeColor = NODE_COLORS[type];
    if (!nodeColor || typeof nodeColor !== 'string' || !nodeColor.startsWith('#')) {
      nodeColor = '#9ca3af';
    }

    graph.addNode(nodeId, {
      x, y,
      size: getScaledNodeSize(baseSize, nodeCount),
      color: nodeColor,
      label: node.properties?.name || node.label || String(nodeId).split('::').pop(),
      nodeType: type,
      filePath: node.properties?.filePath || node.properties?.file_path || '',
      startLine: node.properties?.startLine || node.properties?.start_line,
      endLine: node.properties?.endLine || node.properties?.end_line,
      hidden: false,
      mass: getNodeMass(type, nodeCount),
    });
  };

  const queue: { id: string; depth: number }[] = rootNodes.map((n) => ({ id: n.id, depth: 0 }));
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
    if (!graph.hasNode(node.id)) addNodeWithPosition(node.id, 0);
  });

  const edgeBaseSize = nodeCount > 20000 ? 0.4 : nodeCount > 5000 ? 0.6 : 1.0;

  kgEdges.forEach((rel) => {
    if (graph.hasNode(rel.sourceId) && graph.hasNode(rel.targetId)) {
      if (!graph.hasEdge(rel.sourceId, rel.targetId)) {
        const relType = rel.type.toUpperCase();
        const style = EDGE_STYLES[relType] || { color: '#4a4a5a', sizeMultiplier: 0.5 };
        const curvature = 0.12 + Math.random() * 0.08;
        const edgeColor = typeof style.color === 'string' && style.color.startsWith('#') ? style.color : '#4a4a5a';

        graph.addEdge(rel.sourceId, rel.targetId, {
          size: edgeBaseSize * style.sizeMultiplier,
          color: edgeColor,
          relationType: relType,
          type: 'curved',
          curvature: curvature,
          weight: relType === 'CONTAINS' ? 2.5 : 0.5,
        });
      }
    }
  });

  // Run Louvain community detection locally to provide community metadata
  // This assigns a `community` integer to every node based on connected links.
  try {
    louvain.assign(graph, {
      resolution: 1.2,
      randomWalk: true,
    });
  } catch (err) {
    console.warn('Louvain community clustering error:', err);
  }

  return graph;
};

export const filterGraphByLabels = (
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  visibleLabels: string[],
): void => {
  graph.forEachNode((nodeId, attributes) => {
    const isVisible = visibleLabels.includes(attributes.nodeType);
    graph.setNodeAttribute(nodeId, 'hidden', !isVisible);
  });
};

export const getNodesWithinHops = (
  graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>,
  startNodeId: string,
  maxHops: number,
): Set<string> => {
  const visited = new Set<string>();
  const queue: { nodeId: string; depth: number }[] = [{ nodeId: startNodeId, depth: 0 }];

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
