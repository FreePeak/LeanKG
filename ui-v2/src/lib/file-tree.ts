import type { GraphNode } from '../core/graph/types';
import { isContainerNode, nodeElementType } from './node-kinds';

export type ExplorerKind = 'folder' | 'file';

export interface ExplorerEntry {
  /** Graph node id when known; synthetic folders use `folder:<path>`. */
  id: string;
  name: string;
  path: string;
  kind: ExplorerKind;
  children: ExplorerEntry[];
}

const KNOWN_MOUNTS = ['/workspace', '/workspace-other', '/workspace-freepeak'];

/** Normalize `./src/foo` or `/workspace/src/foo` → `src/foo`. */
export function normalizeTreePath(raw: string, projectRoot?: string): string {
  let p = String(raw || '').trim().replace(/\\/g, '/');
  if (p === '.' || p === './') return '';
  p = p.replace(/^\.\//, '').replace(/\/+$/, '');

  const roots = [
    ...(projectRoot ? [projectRoot.replace(/\/$/, '')] : []),
    ...KNOWN_MOUNTS,
  ];
  for (const root of roots) {
    if (!root) continue;
    if (p === root) return '';
    if (p.startsWith(`${root}/`)) {
      p = p.slice(root.length + 1);
      break;
    }
  }
  return p.replace(/^\.\//, '').replace(/\/+$/, '');
}

export function preferPathScore(name: string): number {
  const n = name.toLowerCase();
  if (n === 'src' || n === 'lib' || n === 'app') return 0;
  if (n === 'ui-v2' || n === 'ui') return 1;
  if (n === 'examples' || n === 'benches' || n === 'benchmark' || n === 'e2e') return 9;
  return 5;
}

function compareEntries(a: ExplorerEntry, b: ExplorerEntry): number {
  if (a.kind !== b.kind) return a.kind === 'folder' ? -1 : 1;
  const ps = preferPathScore(a.name) - preferPathScore(b.name);
  if (ps !== 0) return ps;
  return a.name.localeCompare(b.name);
}

function sortTree(entries: ExplorerEntry[]): ExplorerEntry[] {
  entries.sort(compareEntries);
  for (const e of entries) {
    if (e.children.length) e.children = sortTree(e.children);
  }
  return entries;
}

function looksLikeFilePath(fp: string, elementType: string): boolean {
  if (elementType === 'file' || elementType === 'config_file') return true;
  const base = fp.includes('/') ? fp.slice(fp.lastIndexOf('/') + 1) : fp;
  return /\.[a-zA-Z0-9]+$/.test(base);
}

/**
 * Collect top-level folder paths that should start expanded (prefer src/ui).
 */
export function defaultExpandedPaths(tree: ExplorerEntry[]): string[] {
  const out: string[] = [];
  for (const e of tree) {
    if (e.kind !== 'folder') continue;
    if (preferPathScore(e.name) <= 1) {
      out.push(e.path);
      // one level under preferred roots (e.g. src/graph)
      for (const c of e.children) {
        if (c.kind === 'folder') out.push(c.path);
      }
    }
  }
  return out;
}

/**
 * Hierarchical folder+file explorer from loaded graph nodes.
 * Uses filePath from **all** element types (File, Function, Method, …) so Load more
 * pages that add symbols still grow the sidebar tree.
 */
export function buildExplorerTree(
  nodes: GraphNode[],
  opts?: { projectRoot?: string; maxFiles?: number },
): ExplorerEntry[] {
  const projectRoot = opts?.projectRoot;
  const maxFiles = opts?.maxFiles ?? 2000;

  type Mutable = ExplorerEntry & { childMap?: Map<string, Mutable> };
  const root: Mutable = {
    id: 'folder:',
    name: '',
    path: '',
    kind: 'folder',
    children: [],
    childMap: new Map(),
  };

  const ensureFolder = (absPath: string, graphId?: string): Mutable => {
    const parts = normalizeTreePath(absPath, projectRoot).split('/').filter(Boolean);
    let cur = root;
    let acc = '';
    for (const part of parts) {
      acc = acc ? `${acc}/${part}` : part;
      if (!cur.childMap) cur.childMap = new Map();
      let next = cur.childMap.get(part);
      if (!next) {
        next = {
          id: graphId && acc === normalizeTreePath(absPath, projectRoot) ? graphId : `folder:${acc}`,
          name: part,
          path: acc,
          kind: 'folder',
          children: [],
          childMap: new Map(),
        };
        cur.childMap.set(part, next);
        cur.children.push(next);
      } else if (graphId && acc === normalizeTreePath(absPath, projectRoot)) {
        next.id = graphId;
      }
      cur = next;
    }
    return cur;
  };

  const ensureFile = (fp: string, graphId: string, displayName: string) => {
    const norm = normalizeTreePath(fp, projectRoot);
    if (!norm) return;
    const slash = norm.lastIndexOf('/');
    const parent = slash >= 0 ? norm.slice(0, slash) : '';
    const name = slash >= 0 ? norm.slice(slash + 1) : norm;
    const parentNode = parent ? ensureFolder(parent) : root;
    if (!parentNode.childMap) parentNode.childMap = new Map();
    const existing = parentNode.childMap.get(name);
    if (existing) {
      if (existing.kind === 'file' && existing.id.startsWith('file:') && !graphId.startsWith('file:')) {
        existing.id = graphId;
        existing.name = displayName || name;
      }
      return;
    }
    const fileEntry: Mutable = {
      id: graphId,
      name: displayName || name,
      path: norm,
      kind: 'file',
      children: [],
    };
    parentNode.childMap.set(name, fileEntry);
    parentNode.children.push(fileEntry);
  };

  let fileCount = 0;
  for (const n of nodes) {
    const t = nodeElementType(n);
    const rawFp = String(n.properties.filePath || '');
    const fp = normalizeTreePath(rawFp, projectRoot);

    if (isContainerNode(n) || t === 'directory' || t === 'folder' || t === 'service') {
      if (!fp) {
        const name = String(n.properties.name || n.id);
        if (name && name !== '.' && !name.startsWith('/')) {
          ensureFolder(name, n.id);
        }
        continue;
      }
      ensureFolder(fp, n.id);
      continue;
    }

    if (!fp) continue;

    // File node or any symbol whose path points at a source file → folder + file leaf
    if (looksLikeFilePath(fp, t) || t === 'file') {
      if (fileCount < maxFiles) {
        const base = fp.includes('/') ? fp.slice(fp.lastIndexOf('/') + 1) : fp;
        const display =
          t === 'file' || t === 'config_file'
            ? String(n.properties.name || base)
            : base;
        const id = t === 'file' || t === 'config_file' ? n.id : `file:${fp}`;
        ensureFile(fp, id, display);
        fileCount += 1;
      } else {
        // Still create parent folders even when file cap hit
        const slash = fp.lastIndexOf('/');
        if (slash > 0) ensureFolder(fp.slice(0, slash));
      }
      continue;
    }

    // Non-file symbols without extension: still create parent folder chain
    const slash = fp.lastIndexOf('/');
    if (slash > 0) ensureFolder(fp.slice(0, slash));
    else if (fp) ensureFolder(fp);
  }

  const strip = (e: Mutable): ExplorerEntry => ({
    id: e.id,
    name: e.name,
    path: e.path,
    kind: e.kind,
    children: e.children.map((c) => strip(c as Mutable)),
  });

  return sortTree(root.children.map((c) => strip(c as Mutable)));
}
