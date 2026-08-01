/**
 * FR-E41 — keyboard shortcuts. Central mapping so the app and tests share
 * one table. `eventTargetMatches` ignores keystrokes inside inputs/textarea
 * so typing never triggers a shortcut.
 */
export const SHORTCUTS = {
  toggleFilters: 'f',
  toggleSettings: 's',
  toggleLegend: 'l',
  toggleHistory: 'h',
  openSearch: '/',
  export: 'e',
  undo: 'z',
  redo: 'y',
  closePanel: 'Escape',
} as const;

export type ShortcutAction = keyof typeof SHORTCUTS;

export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tag = target.tagName.toLowerCase();
  return tag === 'input' || tag === 'textarea' || tag === 'select' || target.isContentEditable;
}

export interface KeyEventLike {
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  target?: EventTarget | null;
}

export function matchShortcut(event: KeyEventLike): ShortcutAction | null {
  if (isEditableTarget(event.target ?? null)) return null;
  const key = event.key.toLowerCase();
  // Undo/redo require a modifier; everything else is a bare key.
  if ((event.ctrlKey || event.metaKey) && key === 'z') return 'undo';
  if ((event.ctrlKey || event.metaKey) && key === 'y') return 'redo';
  if (event.ctrlKey || event.metaKey) return null;

  for (const [action, shortcut] of Object.entries(SHORTCUTS) as [ShortcutAction, string][]) {
    if (action === 'undo' || action === 'redo') continue;
    if (key === shortcut.toLowerCase()) return action;
  }
  return null;
}
