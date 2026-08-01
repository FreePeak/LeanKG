/**
 * Backend contract — mirrors src/web/handlers.rs JSON shapes.
 * - /api/graph/layout3d  -> ApiEnvelope<Layout3DResponse>
 * - /api/graph/data      -> ApiEnvelope<GraphData>
 * - /api/graph/clusters  -> ApiEnvelope<GraphData> (cluster nodes)
 */

export interface ApiEnvelope<T> {
  success: boolean;
  data: T | null;
  error: string | null;
}

/** GET /api/graph/layout3d */
export interface Layout3DNode {
  node_id: string;
  x: number;
  y: number;
  z: number;
}

export interface Layout3DBounds {
  min_x: number;
  max_x: number;
  min_y: number;
  max_y: number;
  min_z: number;
  max_z: number;
}

export interface Layout3DResponse {
  nodes: Layout3DNode[];
  bounds: Layout3DBounds;
}

/** GET /api/graph/data + /api/graph/clusters */
export interface NodeProperties {
  name: string;
  filePath: string;
  elementType: string;
}

export interface GraphNode {
  id: string;
  label: string;
  properties: NodeProperties;
}

export interface GraphRelationship {
  id: string;
  sourceId: string;
  targetId: string;
  type: string;
  confidenceLabel: string;
}

export interface GraphData {
  nodes: GraphNode[];
  relationships: GraphRelationship[];
  filtered: { testsFiltered: number; message: string } | null;
  hasMore: boolean;
}
