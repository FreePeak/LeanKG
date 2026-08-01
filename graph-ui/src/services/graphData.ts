/** Graph scene data from `GET /api/graph/layout3d` (PR-50 FR-E10..E14). */
export interface GraphNode3D {
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
  nodes: GraphNode3D[];
  bounds: Layout3DBounds;
}

export interface ApiResponse<T> {
  success: boolean;
  data: T | null;
  error: string | null;
}

/** Edge list used to derive degrees when the backend lacks god scores (FR-E27). */
export interface GraphEdge {
  source: string;
  target: string;
}

export interface GraphDataNode {
  id: string;
}

export interface GraphDataResponse {
  nodes: GraphDataNode[];
  edges: { source: string; target: string }[];
}

/** Fetch node+edge graph data (source of truth for degrees). */
export async function fetchGraphData(
  url = '/api/graph/data',
): Promise<GraphDataResponse> {
  const res = await fetch(url);
  const json = (await res.json()) as ApiResponse<GraphDataResponse>;
  if (!json.success || !json.data) {
    throw new Error(json.error ?? 'graph data request failed');
  }
  return json.data;
}

/** Fetch the 3D layout. `onBatch` streams progressive batches (FR-E22). */
export async function fetchLayout3d(
  url = '/api/graph/layout3d',
  onBatch?: (nodes: GraphNode3D[]) => void,
): Promise<GraphNode3D[]> {
  const res = await fetch(url);
  const json = (await res.json()) as ApiResponse<Layout3DResponse>;
  if (!json.success || !json.data) {
    throw new Error(json.error ?? 'layout3d request failed');
  }
  const nodes = json.data.nodes;
  if (onBatch) onBatch(nodes);
  return nodes;
}

/** Degree per node_id from edge list. */
export function degreeByNode(edges: GraphEdge[]): Map<string, number> {
  const degree = new Map<string, number>();
  for (const e of edges) {
    degree.set(e.source, (degree.get(e.source) ?? 0) + 1);
    degree.set(e.target, (degree.get(e.target) ?? 0) + 1);
  }
  return degree;
}
