import type {
  GraphNode,
  GraphRelationship,
  KnowledgeGraph,
} from '../core/graph/types';

/** Map API type strings (`function`, `property`, `File`) to PascalCase labels. */
export function toPascalType(raw: unknown): string {
  const t = String(raw ?? '').trim();
  if (!t) return 'CodeElement';
  if (t.startsWith('Cluster[')) return t;
  if (/^[A-Z][A-Za-z0-9]*$/.test(t)) return t;
  return t.charAt(0).toUpperCase() + t.slice(1).toLowerCase();
}

/** Normalize LeanKG API payloads (camelCase or snake_case) into KnowledgeGraph. */
export function normalizeGraphPayload(raw: {
  nodes?: unknown[];
  relationships?: unknown[];
}): KnowledgeGraph {
  const nodes: GraphNode[] = (raw.nodes ?? []).map((n) => {
    const node = n as Record<string, unknown>;
    const props = (node.properties ?? {}) as Record<string, unknown>;
    const elementType = toPascalType(
      props.elementType ?? props.element_type ?? node.label ?? 'CodeElement',
    );
    return {
      id: String(node.id),
      label: elementType,
      properties: {
        ...props,
        name: String(props.name ?? node.id ?? ''),
        filePath: String(props.filePath ?? props.file_path ?? ''),
        elementType,
        startLine:
          typeof props.startLine === 'number'
            ? props.startLine
            : typeof props.start_line === 'number'
              ? props.start_line
              : undefined,
        endLine:
          typeof props.endLine === 'number'
            ? props.endLine
            : typeof props.end_line === 'number'
              ? props.end_line
              : undefined,
      },
    };
  });

  const relationships: GraphRelationship[] = (raw.relationships ?? []).map((e, i) => {
    const edge = e as Record<string, unknown>;
    return {
      id: String(edge.id ?? `e-${i}`),
      sourceId: String(edge.sourceId ?? edge.source_id ?? ''),
      targetId: String(edge.targetId ?? edge.target_id ?? ''),
      type: String(edge.type ?? edge.rel_type ?? 'REFERENCES').toUpperCase(),
    };
  });

  return {
    nodes,
    relationships,
    nodeCount: nodes.length,
    relationshipCount: relationships.length,
  };
}

export interface ApiEnvelope<T> {
  success: boolean;
  data: T | null;
  error: string | null;
}

export function unwrapEnvelope<T>(json: ApiEnvelope<T>, fallbackMsg: string): T {
  if (!json.success || json.data == null) {
    throw new Error(json.error || fallbackMsg);
  }
  return json.data;
}
