import { describe, expect, it } from 'vitest';
import {
  buildExplorerTree,
  defaultExpandedPaths,
  normalizeTreePath,
} from '../../src/lib/file-tree';
import { mergeKnowledgeGraphs } from '../../src/lib/graph-merge';
import type { GraphNode, KnowledgeGraph } from '../../src/core/graph/types';

function node(
  id: string,
  elementType: string,
  filePath: string,
  name?: string,
): GraphNode {
  return {
    id,
    label: elementType,
    properties: {
      name: name ?? id.split('::').pop() ?? id,
      filePath,
      elementType,
    },
  };
}

function kg(nodes: GraphNode[]): KnowledgeGraph {
  return {
    nodes,
    relationships: [],
    nodeCount: nodes.length,
    relationshipCount: 0,
  };
}

describe('buildExplorerTree', () => {
  it('shows folders and files, prefers src over examples', () => {
    const nodes = [
      node('f1', 'File', './examples/demo/main.go', 'main.go'),
      node('f2', 'File', './src/lib.rs', 'lib.rs'),
      node('d1', 'Directory', './examples', 'examples'),
      node('d2', 'Directory', './src', 'src'),
    ];
    const tree = buildExplorerTree(nodes);
    expect(tree.map((e) => e.name)).toEqual(['src', 'examples']);
    expect(tree[0].kind).toBe('folder');
    expect(tree[0].children.some((c) => c.name === 'lib.rs')).toBe(true);
  });

  it('synthesizes parent folders from file paths', () => {
    const tree = buildExplorerTree([
      node('f', 'File', './src/graph/query.rs', 'query.rs'),
    ]);
    expect(tree[0].name).toBe('src');
    expect(tree[0].children[0].name).toBe('graph');
    expect(tree[0].children[0].children[0].name).toBe('query.rs');
  });

  it('builds folders from Function paths (load-more pages)', () => {
    const page1 = kg([
      node('ex', 'function', './examples/demo/main.go', 'main'),
    ]);
    const page2 = kg([
      node('fn', 'function', './src/graph/query.rs', 'get_elements'),
      node('fn2', 'method', './src/mcp/handler.rs', 'handle'),
    ]);
    const merged = mergeKnowledgeGraphs(page1, page2);
    const tree = buildExplorerTree(merged.nodes);
    expect(tree.map((e) => e.name)).toEqual(['src', 'examples']);
    const src = tree.find((e) => e.name === 'src')!;
    expect(src.children.map((c) => c.name).sort()).toEqual(['graph', 'mcp']);
    expect(defaultExpandedPaths(tree)).toContain('src');
    expect(defaultExpandedPaths(tree)).toContain('src/graph');
  });

  it('strips project mount from absolute paths', () => {
    expect(normalizeTreePath('/workspace/src/lib.rs', '/workspace')).toBe('src/lib.rs');
    expect(normalizeTreePath('/workspace', '/workspace')).toBe('');
    expect(normalizeTreePath('./src/')).toBe('src');
  });
});
