import { useRef, useEffect, useCallback, useState } from 'react';
import Sigma from 'sigma';
import Graph from 'graphology';
import FA2Layout from 'graphology-layout-forceatlas2/worker';
import forceAtlas2 from 'graphology-layout-forceatlas2';
import noverlap from 'graphology-layout-noverlap';
import EdgeCurveProgram from '@sigma/edge-curve';
import type { SigmaNodeAttributes, SigmaEdgeAttributes } from '../lib/graph-adapter';
import type { EdgeType } from '../lib/constants';

// Helper: Parse hex color to RGB
const hexToRgb = (hex: string): { r: number; g: number; b: number } => {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  return result
    ? { r: parseInt(result[1], 16), g: parseInt(result[2], 16), b: parseInt(result[3], 16) }
    : { r: 100, g: 100, b: 100 };
};

// Helper: RGB to hex
const rgbToHex = (r: number, g: number, b: number): string => {
  return '#' + [r, g, b].map((x) => {
    const hex = Math.max(0, Math.min(255, Math.round(x))).toString(16);
    return hex.length === 1 ? '0' + hex : hex;
  }).join('');
};

const dimColor = (hex: string, amount: number): string => {
  const validHex = typeof hex === 'string' && hex.startsWith('#') ? hex : '#646464';
  const rgb = hexToRgb(validHex);
  const darkBg = { r: 18, g: 18, b: 28 };
  return rgbToHex(
    darkBg.r + (rgb.r - darkBg.r) * amount,
    darkBg.g + (rgb.g - darkBg.g) * amount,
    darkBg.b + (rgb.b - darkBg.b) * amount,
  );
};

const brightenColor = (hex: string, factor: number): string => {
  const validHex = typeof hex === 'string' && hex.startsWith('#') ? hex : '#646464';
  const rgb = hexToRgb(validHex);
  return rgbToHex(
    rgb.r + ((255 - rgb.r) * (factor - 1)) / factor,
    rgb.g + ((255 - rgb.g) * (factor - 1)) / factor,
    rgb.b + ((255 - rgb.b) * (factor - 1)) / factor,
  );
};

interface UseSigmaOptions {
  onNodeClick?: (nodeId: string) => boolean | void;
  onNodeDoubleClick?: (nodeId: string) => void;
  onNodeHover?: (nodeId: string | null) => void;
  onStageClick?: () => void;
  visibleEdgeTypes?: EdgeType[];
  searchTerm?: string;
}

interface UseSigmaReturn {
  containerRef: React.RefObject<HTMLDivElement | null>;
  sigmaRef: React.RefObject<Sigma | null>;
  setGraph: (graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>, skipAnimation?: boolean) => void;
  zoomIn: () => void;
  zoomOut: () => void;
  resetZoom: () => void;
  focusNode: (nodeId: string) => void;
  isLayoutRunning: boolean;
  startLayout: () => void;
  stopLayout: () => void;
  selectedNode: string | null;
  setSelectedNode: (nodeId: string | null) => void;
}

const NOVERLAP_SETTINGS = {
  maxIterations: 300,
  ratio: 0.05,
  margin: 80,
  expansion: 3.0,
};

const getFA2Settings = (nodeCount: number) => {
  const isTiny = nodeCount < 100;
  const isSmall = nodeCount >= 100 && nodeCount < 500;
  const isMedium = nodeCount >= 500 && nodeCount < 2000;
  const isLarge = nodeCount >= 2000 && nodeCount < 10000;

  return {
    gravity: isTiny ? 5 : isSmall ? 3 : isMedium ? 2 : isLarge ? 1 : 0.5,
    scalingRatio: isTiny ? 5 : isSmall ? 20 : isMedium ? 60 : isLarge ? 120 : 200,
    slowDown: isTiny ? 30 : isSmall ? 25 : isMedium ? 15 : isLarge ? 10 : 8,
    barnesHutOptimize: nodeCount > 100,
    barnesHutTheta: 0.5,
    strongGravityMode: true,
    outboundAttractionDistribution: false,
    linLogMode: true,
    adjustSizes: true,
    edgeWeightInfluence: 0.1,
    jitterTolerance: 0.01,
    spaceBetweenIterations: 200,
  };
};

const getLayoutDuration = (nodeCount: number): number => {
  if (nodeCount < 100) return 8000;
  if (nodeCount < 500) return 15000;
  if (nodeCount > 10000) return 45000;
  if (nodeCount > 5000) return 35000;
  if (nodeCount > 2000) return 30000;
  if (nodeCount > 1000) return 30000;
  if (nodeCount > 500) return 25000;
  return 20000;
};

export const useSigma = (options: UseSigmaOptions = {}): UseSigmaReturn => {
  const containerRef = useRef<HTMLDivElement>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph<SigmaNodeAttributes, SigmaEdgeAttributes> | null>(null);
  const layoutRef = useRef<FA2Layout | null>(null);
  const selectedNodeRef = useRef<string | null>(null);
  const visibleEdgeTypesRef = useRef<EdgeType[] | null>(null);
  const searchTermRef = useRef<string>('');
  const layoutTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const clickTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [isLayoutRunning, setIsLayoutRunning] = useState(false);
  const [selectedNode, setSelectedNodeState] = useState<string | null>(null);

  useEffect(() => {
    visibleEdgeTypesRef.current = options.visibleEdgeTypes || null;
    searchTermRef.current = options.searchTerm?.toLowerCase() || '';
    sigmaRef.current?.refresh();
  }, [options.visibleEdgeTypes, options.searchTerm]);

  const setSelectedNode = useCallback((nodeId: string | null) => {
    selectedNodeRef.current = nodeId;
    setSelectedNodeState(nodeId);
    // Note: sigma.refresh() is handled by the useEffect in GraphViewer
    // to avoid double-refresh on every node click
  }, []);

  useEffect(() => {
    if (!containerRef.current) return;
    const graph = new Graph<SigmaNodeAttributes, SigmaEdgeAttributes>();
    graphRef.current = graph;

    const sigma = new Sigma(graph, containerRef.current, {
      renderLabels: true,
      labelFont: 'JetBrains Mono, monospace',
      labelSize: 12,
      labelWeight: '500',
      labelColor: { color: '#e4e4ed' },
      labelRenderedSizeThreshold: 12,
      labelDensity: 0.07,
      labelGridCellSize: 120,
      defaultNodeColor: '#6b7280',
      defaultEdgeColor: '#2a2a3a',
      defaultEdgeType: 'curved',
      edgeProgramClasses: {
        curved: EdgeCurveProgram,
      },
      defaultDrawNodeHover: (context: CanvasRenderingContext2D, data: any, settings: any) => {
        const baseLabel = typeof data.label === 'string' ? data.label : String(data.label || '');
        if (!baseLabel) return;
        const nodeType = typeof data.nodeType === 'string' ? data.nodeType : '';
        const label = nodeType ? `[${nodeType}] ${baseLabel}` : baseLabel;

        const size = settings.labelSize || 11;
        const font = settings.labelFont || 'JetBrains Mono, monospace';
        const weight = settings.labelWeight || '500';

        context.font = `${weight} ${size}px ${font}`;
        const textWidth = context.measureText(label).width;

        const nodeSize = data.size || 8;
        const x = data.x;
        const y = data.y - nodeSize - 10;
        const paddingX = 8;
        const paddingY = 5;
        const height = size + paddingY * 2;
        const width = textWidth + paddingX * 2;
        const radius = 4;

        context.fillStyle = '#12121c';
        context.beginPath();
        context.roundRect(x - width / 2, y - height / 2, width, height, radius);
        context.fill();

        context.strokeStyle = data.color || '#6366f1';
        context.lineWidth = 2;
        context.stroke();

        context.fillStyle = '#f5f5f7';
        context.textAlign = 'center';
        context.textBaseline = 'middle';
        context.fillText(label, x, y);

        context.beginPath();
        context.arc(data.x, data.y, nodeSize + 4, 0, Math.PI * 2);
        context.strokeStyle = data.color || '#6366f1';
        context.lineWidth = 2;
        context.globalAlpha = 0.5;
        context.stroke();
        context.globalAlpha = 1;
      },
      nodeReducer: (node: string, data: any) => {
        const res = { ...data };
        if (data.hidden) {
          res.hidden = true; return res;
        }

        // Ensure color is always a valid string
        const nodeColor = typeof data.color === 'string' && data.color.startsWith('#') ? data.color : '#9ca3af';
        res.color = nodeColor;

        const searchTerm = searchTermRef.current;
        const labelStr = typeof data.label === 'string' ? data.label : String(data.label || '');
        const matchesSearch = !searchTerm || (labelStr && labelStr.toLowerCase().includes(searchTerm));
        if (!matchesSearch) {
          res.color = dimColor(nodeColor, 0.1);
          res.size = (data.size || 8) * 0.4;
          res.zIndex = 0;
          res.label = null;
          return res;
        }

        // Check if this is a service node
        const isServiceNode = node.startsWith('service:') || data.nodeType === 'Service';

        // Elevate structural nodes visually above functions when exploring
        const baseZIndex = (data.size || 8) >= 10 ? 1 : 0;
        res.zIndex = baseZIndex;

        const currentSelected = selectedNodeRef.current;
        if (currentSelected) {
          const g = graphRef.current;
          if (g) {
            const isSelected = node === currentSelected;
            const isNeighbor = g.hasEdge(node, currentSelected) || g.hasEdge(currentSelected, node);
            if (isSelected) {
              res.color = nodeColor;
              res.size = (data.size || 8) * 1.8;
              res.zIndex = 3;
              res.highlighted = true;
              // Show label for selected node
            } else if (isNeighbor) {
              res.color = nodeColor;
              res.size = (data.size || 8) * 1.3;
              res.zIndex = 2;
              // Show label for neighbor nodes
            } else {
              res.color = dimColor(nodeColor, 0.25);
              res.size = (data.size || 8) * 0.6;
              res.zIndex = 0;
              // Hide labels for non-relevant nodes when something is selected
              if (isServiceNode) {
                // Keep service node labels visible but dimmed
                res.label = labelStr;
              } else {
                res.label = null;
              }
            }
          }
        } else if (searchTerm) {
          // Extra highlight if it's the only thing matching
          res.color = nodeColor;
          res.size = (data.size || 8) * 1.5;
          res.zIndex = 2;
        } else {
          // No selection, no search - show all labels
          res.label = labelStr;
        }
        return res;
      },
      edgeReducer: (edge: string, data: any) => {
        const res = { ...data };
        // Check edge type visibility first
        const visibleTypes = visibleEdgeTypesRef.current;
        if (visibleTypes && visibleTypes.length > 0 && data.relationType) {
          if (!visibleTypes.includes(data.relationType as EdgeType)) {
            res.hidden = true;
            return res;
          }
        }

        const searchTerm = searchTermRef.current;
        const currentSelected = selectedNodeRef.current;
        const graph = graphRef.current;

        if (searchTerm && graph) {
          const [source, target] = graph.extremities(edge);
          const sourceAttrs = graph.getNodeAttributes(source);
          const targetAttrs = graph.getNodeAttributes(target);
          const sourceLabel = typeof sourceAttrs.label === 'string' ? sourceAttrs.label : String(sourceAttrs.label || '');
          const targetLabel = typeof targetAttrs.label === 'string' ? targetAttrs.label : String(targetAttrs.label || '');
          const sourceMatch = sourceLabel && sourceLabel.toLowerCase().includes(searchTerm);
          const targetMatch = targetLabel && targetLabel.toLowerCase().includes(searchTerm);
          if (!sourceMatch && !targetMatch) {
            res.color = dimColor(typeof data.color === 'string' ? data.color : '#2a2a3a', 0.05);
            res.size = 0.1;
            res.zIndex = 0;
            return res;
          }
        }

        if (currentSelected && graph) {
          const [source, target] = graph.extremities(edge);
          const isConnected = source === currentSelected || target === currentSelected;
          if (isConnected) {
            res.color = brightenColor(typeof data.color === 'string' ? data.color : '#2a2a3a', 1.5);
            res.size = Math.max(3, (data.size || 1) * 4);
            res.zIndex = 2;
          } else {
            res.color = dimColor(typeof data.color === 'string' ? data.color : '#2a2a3a', 0.1);
            res.size = 0.3;
            res.zIndex = 0;
          }
        }
        return res;
      },
    });

    sigmaRef.current = sigma;
    if (typeof window !== 'undefined') {
      (window as any).sig = sigma;
    }

    sigma.on('clickNode', ({ node }) => {
      clickTimeoutRef.current = setTimeout(() => {
        const shouldSelect = options.onNodeClick?.(node);
        if (shouldSelect !== false) {
          setSelectedNode(node);
        }
      }, 250);
    });

    sigma.on('doubleClickNode', ({ node }) => {
      if (clickTimeoutRef.current) {
        clearTimeout(clickTimeoutRef.current);
        clickTimeoutRef.current = null;
      }
      options.onNodeDoubleClick?.(node);
    });

    sigma.on('clickStage', () => {
      setSelectedNode(null);
      options.onStageClick?.();
    });

    sigma.on('enterNode', ({ node }) => {
      options.onNodeHover?.(node);
      if (containerRef.current) containerRef.current.style.cursor = 'pointer';
    });

    sigma.on('leaveNode', () => {
      options.onNodeHover?.(null);
      if (containerRef.current) containerRef.current.style.cursor = 'grab';
    });

    return () => {
      if (layoutTimeoutRef.current) clearTimeout(layoutTimeoutRef.current);
      if (clickTimeoutRef.current) clearTimeout(clickTimeoutRef.current);
      layoutRef.current?.kill();
      sigma.kill();
      sigmaRef.current = null;
      graphRef.current = null;
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const runLayout = useCallback((graph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>, skipAnimation: boolean = false) => {
    const nodeCount = graph.order;
    if (nodeCount === 0) return;

    if (layoutRef.current) {
      layoutRef.current.kill();
      layoutRef.current = null;
    }
    if (layoutTimeoutRef.current) clearTimeout(layoutTimeoutRef.current);

    if (skipAnimation) {
      // Skip layout animation for initial load
      // For massive graphs, also skip noverlap as it can cause Set overflow errors
      if (nodeCount < 10000) {
        noverlap.assign(graph, NOVERLAP_SETTINGS);
      }
      const sigma = sigmaRef.current;
      if (sigma) {
        // Calculate graph bounds and fit camera synchronously
        let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
        graph.forEachNode((_n: string, attrs: SigmaNodeAttributes) => {
          if (attrs.x < minX) minX = attrs.x;
          if (attrs.x > maxX) maxX = attrs.x;
          if (attrs.y < minY) minY = attrs.y;
          if (attrs.y > maxY) maxY = attrs.y;
        });
        const cx = (minX + maxX) / 2;
        const cy = (minY + maxY) / 2;
        const graphW = maxX - minX || 1;
        const graphH = maxY - minY || 1;
        const container = sigma.getContainer();
        const ratio = Math.max(graphW / (container?.clientWidth || 800), graphH / (container?.clientHeight || 600)) * 1.2;
        sigma.getCamera().setState({ x: cx, y: cy, angle: 0, ratio });
      }
      sigmaRef.current?.refresh();
      return;
    }

    const inferredSettings = forceAtlas2.inferSettings(graph);
    const customSettings = getFA2Settings(nodeCount);
    const settings = { ...inferredSettings, ...customSettings };

    const layout = new FA2Layout(graph, { settings });
    layoutRef.current = layout;
    layout.start();
    setIsLayoutRunning(true);

    const duration = getLayoutDuration(nodeCount);
    layoutTimeoutRef.current = setTimeout(() => {
      if (layoutRef.current) {
        layoutRef.current.stop();
        layoutRef.current = null;
        // Skip noverlap for massive graphs to avoid Set overflow
        if (nodeCount < 10000) {
          noverlap.assign(graph, NOVERLAP_SETTINGS);
        }
        sigmaRef.current?.refresh();
        sigmaRef.current?.getCamera().animatedReset({ duration: 800 });
        setIsLayoutRunning(false);
      }
    }, duration);
  }, []);

  const setGraph = useCallback((newGraph: Graph<SigmaNodeAttributes, SigmaEdgeAttributes>, skipAnimation: boolean = false) => {
    const sigma = sigmaRef.current;
    if (!sigma) return;

    if (layoutRef.current) { layoutRef.current.kill(); layoutRef.current = null; }
    if (layoutTimeoutRef.current) { clearTimeout(layoutTimeoutRef.current); layoutTimeoutRef.current = null; }

    graphRef.current = newGraph;
    sigma.setGraph(newGraph);
    setSelectedNode(null);

    runLayout(newGraph, skipAnimation);
    // Always ensure camera is properly fitted to graph bounds, even on initial load
    sigma.getCamera().animatedReset({ duration: skipAnimation ? 0 : 500 });
  }, [runLayout, setSelectedNode]);

  const focusNode = useCallback((nodeId: string) => {
    const sigma = sigmaRef.current;
    const graph = graphRef.current;
    if (!sigma || !graph || !graph.hasNode(nodeId)) return;

    const alreadySelected = selectedNodeRef.current === nodeId;
    selectedNodeRef.current = nodeId;
    setSelectedNodeState(nodeId);

    if (!alreadySelected) {
      const nodeAttrs = graph.getNodeAttributes(nodeId);
      sigma.getCamera().animate({ x: nodeAttrs.x, y: nodeAttrs.y, ratio: 0.15 }, { duration: 400 });
    }
    sigma.refresh();
  }, []);

  const zoomIn = useCallback(() => sigmaRef.current?.getCamera().animatedZoom({ duration: 200 }), []);
  const zoomOut = useCallback(() => sigmaRef.current?.getCamera().animatedUnzoom({ duration: 200 }), []);
  const resetZoom = useCallback(() => {
    sigmaRef.current?.getCamera().animatedReset({ duration: 300 });
    setSelectedNode(null);
  }, [setSelectedNode]);

  const startLayout = useCallback(() => {
    const graph = graphRef.current;
    if (!graph || graph.order === 0) return;
    runLayout(graph);
  }, [runLayout]);

  const stopLayout = useCallback(() => {
    if (layoutTimeoutRef.current) { clearTimeout(layoutTimeoutRef.current); layoutTimeoutRef.current = null; }
    if (layoutRef.current) {
      layoutRef.current.stop();
      layoutRef.current = null;
      const graph = graphRef.current;
      if (graph) {
        if (graph.order < 10000) {
          noverlap.assign(graph, NOVERLAP_SETTINGS);
        }
        sigmaRef.current?.refresh();
      }
      setIsLayoutRunning(false);
    }
  }, []);

  return {
    containerRef, sigmaRef, setGraph, zoomIn, zoomOut, resetZoom, focusNode,
    isLayoutRunning, startLayout, stopLayout, selectedNode, setSelectedNode,
  };
};
