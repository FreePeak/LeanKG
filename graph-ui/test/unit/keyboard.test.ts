/**
 * FR-E41 — keyboard shortcuts.
 */
import { describe, expect, it } from 'vitest';
import { matchShortcut } from '../../src/lib/keyboard';

describe('keyboard shortcuts (FR-E41)', () => {
  it('bare keys map to actions', () => {
    expect(matchShortcut({ key: 'f' })).toBe('toggleFilters');
    expect(matchShortcut({ key: 'F' })).toBe('toggleFilters');
    expect(matchShortcut({ key: 's' })).toBe('toggleSettings');
    expect(matchShortcut({ key: 'l' })).toBe('toggleLegend');
    expect(matchShortcut({ key: 'h' })).toBe('toggleHistory');
    expect(matchShortcut({ key: '/' })).toBe('openSearch');
    expect(matchShortcut({ key: 'e' })).toBe('export');
    expect(matchShortcut({ key: 'Escape' })).toBe('closePanel');
  });

  it('ctrl/cmd+z and ctrl/cmd+y map to undo/redo', () => {
    expect(matchShortcut({ key: 'z', ctrlKey: true })).toBe('undo');
    expect(matchShortcut({ key: 'z', metaKey: true })).toBe('undo');
    expect(matchShortcut({ key: 'y', ctrlKey: true })).toBe('redo');
  });

  it('unknown keys return null', () => {
    expect(matchShortcut({ key: 'x' })).toBeNull();
    expect(matchShortcut({ key: 'z' })).toBeNull();
    expect(matchShortcut({ key: 'a', ctrlKey: true })).toBeNull();
  });

  it('ignores keystrokes inside editable targets', () => {
    const input = document.createElement('input');
    const textarea = document.createElement('textarea');
    expect(matchShortcut({ key: 'f', target: input })).toBeNull();
    expect(matchShortcut({ key: 's', target: textarea })).toBeNull();
    expect(matchShortcut({ key: 'f', target: null })).toBe('toggleFilters');
  });
});
