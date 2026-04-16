export const NODE_COLORS: Record<string, string> = {
  Service: '#ef4444',
  Folder: '#6366f1',
  Directory: '#6366f1',
  File: '#3b82f6',
  Class: '#f59e0b',
  Function: '#10b981',
  Method: '#14b8a6',
  Interface: '#ec4899',
  Enum: '#f97316',
  Struct: '#f59e0b',
  Module: '#8b5cf6',
  Constructor: '#06b6d4',
  Property: '#a78bfa',
  Decorator: '#f472b6',
  Config: '#64748b',
};

export const NODE_SIZES: Record<string, number> = {
  Service: 18,
  Folder: 10,
  Directory: 10,
  File: 6,
  Class: 8,
  Function: 4,
  Method: 3,
  Interface: 7,
  Enum: 5,
  Struct: 8,
  Module: 9,
  Constructor: 4,
  Property: 3,
  Decorator: 5,
};

export const EDGE_STYLES: Record<string, { color: string; sizeMultiplier: number }> = {
  CONTAINS: { color: '#2d5a3d', sizeMultiplier: 0.4 },
  DEFINES: { color: '#0e7490', sizeMultiplier: 0.5 },
  IMPORTS: { color: '#1d4ed8', sizeMultiplier: 0.6 },
  CALLS: { color: '#7c3aed', sizeMultiplier: 0.8 },
  SERVICE_CALLS: { color: '#ef4444', sizeMultiplier: 1.2 },
  EXTENDS: { color: '#c2410c', sizeMultiplier: 1.0 },
  IMPLEMENTS: { color: '#be185d', sizeMultiplier: 0.9 },
  REFERENCES: { color: '#0ea5e9', sizeMultiplier: 0.5 },
  DOCUMENTED_BY: { color: '#84cc16', sizeMultiplier: 0.3 },
  TESTED_BY: { color: '#22d3ee', sizeMultiplier: 0.4 },
};

export type EdgeType = keyof typeof EDGE_STYLES | string;

export const DEFAULT_NODE_TYPE_ORDER = [
  'Service',
  'Folder',
  'Directory',
  'File',
  'Module',
  'Class',
  'Struct',
  'Interface',
  'Enum',
  'Function',
  'Method',
  'Constructor',
  'Property',
  'Decorator',
];

export const DEFAULT_VISIBLE_LABELS = [
  'Service',
  'Folder',
  'File',
  'Function',
];
