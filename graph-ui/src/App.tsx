import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Canvas } from '@react-three/fiber';
import { OrbitControls, Stars } from '@react-three/drei';
import { LodNodeScene } from './components/LodNodeScene';
import { DEFAULT_LOD_CONFIG, batchNodes } from './lod/lod';
import { degreeByNode, fetchGraphData, fetchLayout3d, type GraphNode3D } from './services/graphData';

/** Scene-level LOD demo — FR-E20..E28. Loads the server 3D layout (PR-50). */
export default function App() {
  const [error, setError] = useState<string | null>(null);
  const [stats, setStats] = useState({ fps: 0, visible: 0, total: 0 });
  const [loaded, setLoaded] = useState(0);
  const allNodes = useRef<GraphNode3D[]>([]);
  const degreeMap = useRef<Map<string, number>>(new Map());

  useEffect(() => {
    let cancelled = false;
    // FR-E27: importance from degree (backend god score not exposed per node).
    fetchGraphData()
      .then((data) => {
        if (cancelled) return;
        degreeMap.current = degreeByNode(data.edges);
      })
      .catch(() => {
        // Layout still renders with default degree 1.
      });
    fetchLayout3d('/api/graph/layout3d', (all) => {
      if (cancelled) return;
      allNodes.current = all;
      // FR-E22: progressive loading — stream in batches.
      const batches = batchNodes(all.length, DEFAULT_LOD_CONFIG.batchSize, DEFAULT_LOD_CONFIG);
      setLoaded(batches[0] ?? 0);
      let i = 1;
      const timer = setInterval(() => {
        if (i >= batches.length) {
          clearInterval(timer);
          return;
        }
        setLoaded((l) => l + batches[i]);
        i += 1;
      }, 120);
    })
      .then(() => undefined)
      .catch((e: unknown) => {
        if (!cancelled) setError(String(e));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const visibleNodes = useMemo(
    () => allNodes.current.slice(0, loaded),
    [loaded],
  );

  // FR-E27: importance = degree (backend god score not exposed per node).
  const degrees = useMemo(() => {
    const d = new Float32Array(visibleNodes.length);
    const deg = degreeMap.current;
    visibleNodes.forEach((n, i) => {
      d[i] = deg.get(n.node_id) ?? 1;
    });
    return d;
  }, [visibleNodes, degreeMap]);

  const frameStats = useCallback(
    (s: { fps: number; visible: number; total: number }) => setStats(s),
    [],
  );

  return (
    <div style={{ width: '100vw', height: '100vh', background: '#0a0c18' }}>
      <div
        style={{
          position: 'absolute',
          top: 12,
          left: 12,
          zIndex: 10,
          color: '#9aa4c8',
          font: '12px monospace',
          background: 'rgba(10,12,24,0.7)',
          padding: '8px 12px',
          borderRadius: 8,
        }}
      >
        <div>fps {stats.fps.toFixed(0)}</div>
        <div>visible {stats.visible} / {stats.total}</div>
        <div>loaded {loaded} / {allNodes.current.length}</div>
      </div>
      {error && (
        <div
          style={{
            position: 'absolute',
            top: 12,
            right: 12,
            zIndex: 10,
            color: '#f7768e',
            font: '12px monospace',
            background: 'rgba(10,12,24,0.7)',
            padding: 8,
            borderRadius: 8,
          }}
        >
          {error}
        </div>
      )}
      <Canvas camera={{ position: [0, 0, 320], fov: 60 }}>
        <color attach="background" args={['#0a0c18']} />
        <ambientLight intensity={0.5} />
        <pointLight position={[200, 200, 300]} intensity={1.2} />
        <Stars radius={600} depth={80} count={4000} factor={4} fade speed={0.5} />
        <LodNodeScene
          nodes={visibleNodes}
          degrees={degrees}
          onFrameStats={frameStats}
        />
        <OrbitControls makeDefault enableDamping />
      </Canvas>
    </div>
  );
}
