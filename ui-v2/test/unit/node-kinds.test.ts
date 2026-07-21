import { describe, expect, it } from 'vitest';
import {
  isContainerNode,
  isContentBearingNode,
  nodeElementType,
} from '../../src/lib/node-kinds';

describe('node-kinds (FR-UI2-12)', () => {
  it('treats Service/Folder/Directory as containers', () => {
    expect(
      isContainerNode({
        id: 'service:svc-a',
        label: 'Service',
        properties: { elementType: 'Service', name: 'svc-a', filePath: '/workspace-other/svc-a' },
      }),
    ).toBe(true);
    expect(
      isContainerNode({
        id: 'folder:src',
        properties: { elementType: 'Folder', name: 'src', filePath: 'src/' },
      }),
    ).toBe(true);
    expect(
      isContainerNode({
        properties: { elementType: 'Directory', name: 'pkg', filePath: 'pkg/' },
      }),
    ).toBe(true);
  });

  it('does not treat File/Function as containers', () => {
    expect(
      isContainerNode({
        properties: { elementType: 'File', name: 'main.rs', filePath: 'src/main.rs' },
      }),
    ).toBe(false);
    expect(
      isContentBearingNode({
        properties: { elementType: 'Function', name: 'main', filePath: 'src/main.rs' },
      }),
    ).toBe(true);
  });

  it('content-bearing excludes containers even if type casing varies', () => {
    expect(
      isContentBearingNode({
        properties: { elementType: 'service', name: 'x', filePath: '/x' },
      }),
    ).toBe(false);
    expect(nodeElementType({ properties: { elementType: 'Method' } })).toBe('method');
  });
});
