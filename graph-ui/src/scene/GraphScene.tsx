import { Canvas, useFrame } from '@react-three/fiber';
import { OrbitControls, Stars } from '@react-three/drei';
import { useMemo, useRef } from 'react';
import * as THREE from 'three';
import type { Mesh, Points } from 'three';
import type { GraphNode, Layout3DNode } from '../lib/types';
import { clusterColorOf, clusterIdOf } from '../lib/color';

interface SceneNodeProps {
  pos: [number, number, number];
  color: string;
  size: number;
  selected: boolean;
  onSelect: () => void;
}

function SceneNode({ pos, color, size, selected, onSelect }: SceneNodeProps) {
  const ref = useRef<Mesh>(null);
  useFrame((_, delta) => {
    const m = ref.current;
    if (!m) return;
    const s = selected ? size * 1.5 : size;
    m.scale.lerp(new THREE.Vector3(s, s, s), Math.min(delta * 6, 1));
  });
  return (
    <mesh
      ref={ref}
      position={pos}
      scale={size}
      onClick={(e) => {
        e.stopPropagation();
        onSelect();
      }}
    >
      <sphereGeometry args={[1, 20, 20]} />
      <meshStandardMaterial color={color} emissive={color} emissiveIntensity={selected ? 0.5 : 0.08} />
    </mesh>
  );
}

/** FR-E01 — 3D scene: nodes as spheres (layout3d positions), edges as lines. */
export default function GraphScene({
  layoutNodes,
  nodes,
  edges,
  selectedId,
  onSelect,
}: {
  layoutNodes: Layout3DNode[];
  nodes: GraphNode[];
  edges: Array<[string, string]>;
  selectedId: string | null;
  onSelect: (id: string | null) => void;
}) {
  const posById = useMemo(
    () => new Map(layoutNodes.map((n) => [n.node_id, n as Layout3DNode])),
    [layoutNodes],
  );

  const sceneNodes = useMemo(
    () =>
      layoutNodes.map((l) => {
        const meta = nodes.find((n) => n.id === l.node_id);
        const clusterId = clusterIdOf(meta?.properties.filePath ?? '');
        return {
          id: l.node_id,
          pos: [l.x, l.y, l.z] as const,
          color: clusterColorOf(clusterId),
          size: meta?.properties.elementType === 'Class' ? 1.4 : 1,
          selected: selectedId === l.node_id,
          meta: meta ?? null,
        };
      }),
    [layoutNodes, nodes, selectedId],
  );

  const edgePositions = useMemo(() => {
    const pairs = edges
      .map(([s, t]) => [posById.get(s), posById.get(t)] as const)
      .filter(([a, b]) => a != null && b != null);
    const arr = new Float32Array(pairs.length * 6);
    pairs.forEach(([a, b], i) => {
      arr[i * 6] = a.x;
      arr[i * 6 + 1] = a.y;
      arr[i * 6 + 2] = a.z;
      arr[i * 6 + 3] = b.x;
      arr[i * 6 + 4] = b.y;
      arr[i * 6 + 5] = b.z;
    });
    return arr;
  }, [edges, posById]);

  const { count, positions } = useMemo(() => {
    const arr = new Float32Array(sceneNodes.length * 3);
    sceneNodes.forEach((n, i) => {
      arr[i * 3] = n.pos[0];
      arr[i * 3 + 1] = n.pos[1];
      arr[i * 3 + 2] = n.pos[2];
    });
    return { count: sceneNodes.length, positions: arr };
  }, [sceneNodes]);

  const pointsRef = useRef<Points>(null);

  return (
    <Canvas camera={{ position: [0, 0, 260], fov: 55, near: 0.1, far: 5000 }}>
      <ambientLight intensity={0.5} />
      <directionalLight position={[80, 120, 100]} intensity={1} />
      <Stars radius={400} depth={60} count={1500} factor={3} saturation={0} fade speed={0.6} />
      {/* FR-E01 — edges as lines (shared vertex buffer from layout positions) */}
      <lineSegments>
        <bufferGeometry>
          <bufferAttribute attach="attributes-position" args={[edgePositions, 3]} count={edgePositions.length / 3} />
        </bufferGeometry>
        <lineBasicMaterial color="#64748b" transparent opacity={0.45} />
      </lineSegments>
      <points ref={pointsRef}>
        <bufferGeometry>
          <bufferAttribute attach="attributes-position" args={[positions, 3]} count={count} />
        </bufferGeometry>
        <pointsMaterial size={0.6} color="#cbd5e1" sizeAttenuation transparent opacity={0.9} />
      </points>
      {sceneNodes.map((n) => (
        <SceneNode
          key={n.id}
          pos={[n.pos[0], n.pos[1], n.pos[2]]}
          color={n.color}
          size={n.size}
          selected={n.selected}
          onSelect={() => onSelect(n.id)}
        />
      ))}
      {/* FR-E02 — orbit camera controls */}
      <OrbitControls enableDamping dampingFactor={0.08} />
    </Canvas>
  );
}
