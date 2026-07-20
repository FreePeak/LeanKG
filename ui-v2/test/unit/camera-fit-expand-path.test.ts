import { describe, expect, it } from 'vitest';
import { normalizeExpandPath } from '../../src/lib/camera-fit';

describe('normalizeExpandPath', () => {
  it('maps project root absolute path to "."', () => {
    expect(normalizeExpandPath('/workspace', '/workspace')).toBe('.');
    expect(normalizeExpandPath('/workspace/', '/workspace')).toBe('.');
  });

  it('strips project prefix for nested folders', () => {
    expect(normalizeExpandPath('/workspace/src/cli', '/workspace')).toBe('./src/cli');
  });

  it('keeps relative paths', () => {
    expect(normalizeExpandPath('src/cli')).toBe('src/cli');
    expect(normalizeExpandPath('.')).toBe('.');
  });
});
