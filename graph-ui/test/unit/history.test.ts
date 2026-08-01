/**
 * FR-E36 — history/undo/redo state machine.
 */
import { describe, expect, it } from 'vitest';
import {
  canRedo,
  canUndo,
  createHistory,
  pushHistory,
  redoHistory,
  undoHistory,
} from '../../src/lib/history';

describe('history (FR-E36)', () => {
  it('starts empty with no undo/redo', () => {
    const h = createHistory<string | null>(null);
    expect(h.past).toEqual([]);
    expect(h.future).toEqual([]);
    expect(canUndo(h)).toBe(false);
    expect(canRedo(h)).toBe(false);
  });

  it('pushHistory moves present into past', () => {
    const h = pushHistory(createHistory<string | null>(null), 'a');
    expect(h.present).toBe('a');
    expect(h.past).toEqual([null]);
  });

  it('undo returns previous and fills future', () => {
    let h = pushHistory(createHistory<string | null>(null), 'a');
    h = pushHistory(h, 'b');
    const u = undoHistory(h);
    expect(u?.present).toBe('a');
    expect(u?.future).toEqual(['b']);
    expect(canRedo(u!)).toBe(true);
  });

  it('undo at the start returns null', () => {
    expect(undoHistory(createHistory<string | null>(null))).toBeNull();
  });

  it('redo replays the future', () => {
    let h = pushHistory(createHistory<string | null>(null), 'a');
    h = pushHistory(h, 'b');
    const u = undoHistory(h)!;
    const r = redoHistory(u);
    expect(r?.present).toBe('b');
    expect(r?.past).toContain('a');
  });

  it('pushHistory truncates to the limit', () => {
    let h = createHistory<string | null>(null, 2);
    for (const v of ['a', 'b', 'c']) h = pushHistory(h, v);
    expect(h.past).toHaveLength(2);
    expect(h.present).toBe('c');
  });
});
