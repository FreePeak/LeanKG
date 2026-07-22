/** LeanKG graph node label (string; mirrors CodeElement.element_type + Service/Folder). */
export type NodeLabel = string;

export interface GraphNodeProperties {
  name: string;
  filePath?: string;
  elementType?: string;
  startLine?: number;
  endLine?: number;
  [key: string]: unknown;
}

export interface GraphNode {
  id: string;
  label: string;
  properties: GraphNodeProperties;
}

export interface GraphRelationship {
  id?: string;
  sourceId: string;
  targetId: string;
  type: string;
  confidenceLabel?: string;
}

export interface KnowledgeGraph {
  nodes: GraphNode[];
  relationships: GraphRelationship[];
  nodeCount: number;
  relationshipCount: number;
}

export function toKnowledgeGraph(
  nodes: GraphNode[],
  relationships: GraphRelationship[],
): KnowledgeGraph {
  return {
    nodes,
    relationships,
    nodeCount: nodes.length,
    relationshipCount: relationships.length,
  };
}
