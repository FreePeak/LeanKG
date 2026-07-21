/** Helpers for UI v2 node-kind gating (US-UI2-03 / FR-UI2-12). */

const CONTAINER_TYPES = new Set(['service', 'folder', 'directory']);

const CONTENT_TYPES = new Set([
  'file',
  'function',
  'method',
  'class',
  'struct',
  'interface',
  'enum',
  'module',
  'property',
  'constructor',
  'decorator',
]);

export function nodeElementType(node: {
  id?: string;
  label?: string;
  properties?: { elementType?: string; [key: string]: unknown };
}): string {
  const raw = String(node.properties?.elementType || node.label || '');
  return raw.trim().toLowerCase();
}

/** Service / Folder / Directory — expand-service targets, not /api/file. */
export function isContainerNode(node: {
  id?: string;
  label?: string;
  properties?: { elementType?: string; [key: string]: unknown };
}): boolean {
  const t = nodeElementType(node);
  if (CONTAINER_TYPES.has(t)) return true;
  const id = String(node.id || '');
  return id.startsWith('service:') || id.startsWith('folder:');
}

/** Nodes whose filePath may be read via /api/file. */
export function isContentBearingNode(node: {
  id?: string;
  label?: string;
  properties?: { elementType?: string; [key: string]: unknown };
}): boolean {
  if (isContainerNode(node)) return false;
  const t = nodeElementType(node);
  if (!t) return false;
  return CONTENT_TYPES.has(t);
}
