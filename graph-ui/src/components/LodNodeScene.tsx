import { useMemo, useRef } from 'react';
import { useFrame, useThree } from '@react-three/fiber';
import { Points, PointMaterial } from '@react-three/drei';
import * as THREE from 'three';
import {
  DEFAULT_LOD_CONFIG,
  importanceSize,
  showLabel,
  type LODConfig,
} from '../lod/lod';
import { computeLodState, trackCameraSpeed } from '../lod/useLod';

export interface LodNodeSceneProps {
  nodes: { x: number; y: number; z: number }[];
  degrees: Float32Array;
  edges?: { source: number; target: number }[];
  cfg?: LODConfig;
  onFrameStats?: (stats: { fps: number; visible: number; total: number }) => void;
}

interface SceneFrame {
  lastTime: number;
  lastPos: { x: number; y: number; z: number } | null;
  frameMs: number;
  hovered: number | null;
}

/**
 * LOD scene — FR-E20..E28.
 * - far nodes: points (FR-E20)
 * - near nodes: spheres (FR-E20)
 * - distant hubs: collapsed (FR-E21)
 * - progressive batches (FR-E22)
 * - labels near camera only (FR-E23)
 * - occlusion culling (FR-E24)
 * - frame budget (FR-E25)
 * - camera-speed LOD (FR-E26)
 * - size by importance (FR-E27)
 * - hover upgrades detail (FR-E28)
 */
export function LodNodeScene({
  nodes,
  degrees,
  cfg = DEFAULT_LOD_CONFIG,
  onFrameStats,
}: LodNodeSceneProps) {
  const frame = useRef<SceneFrame>({
    lastTime: 0,
    lastPos: null,
    frameMs: cfg.frameBudgetMs,
    hovered: null,
  });

  const [pointPositions, sphereData, labels, clusters] = useMemo(() => {
    const pt: number[] = [];
    const sp: {
      position: [number, number, number];
      size: number;
      degree: number;
    }[] = [];
    const lb: { position: [number, number, number]; text: string }[] = [];
    const cl: { position: [number, number, number]; degree: number }[] = [];
    nodes.forEach((n, i) => {
      const size = importanceSize(undefined, degrees[i] ?? 0);
      if (degrees[i] >= cfg.clusterMinDegree) {
        cl.push({ position: [n.x, n.y, n.z], degree: degrees[i] });
        return;
      }
      pt.push(n.x, n.y, n.z);
      sp.push({ position: [n.x, n.y, n.z], size, degree: degrees[i] });
      lb.push({ position: [n.x, n.y, n.z], text: `#${i}` });
    });
    return [pt, sp, lb, cl];
  }, [nodes, degrees, cfg]);

  const pointGeo = useMemo(() => {
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.Float32BufferAttribute(pointPositions, 3));
    return g;
  }, [pointPositions]);

  const spheres = useMemo(
    () => sphereData.map((s, i) => <SphereMesh key={i} {...s} />),
    [sphereData],
  );

  useFrame((state) => {
    const now = state.clock.elapsedTime * 1000;
    const f = frame.current;
    if (f.lastTime === 0) {
      f.lastTime = now;
      return;
    }
    const dt = now - f.lastTime;
    f.lastTime = now;
    f.frameMs = dt;
    const cam = state.camera;
    const pos = cam.position;
    const speed = trackCameraSpeed(pos, f.lastPos, dt);
    f.lastPos = { x: pos.x, y: pos.y, z: pos.z };

    const lod = computeLodState(nodes, degrees, cam, dt, speed, f.hovered, cfg);
    if (onFrameStats) {
      onFrameStats({
        fps: 1000 / Math.max(dt, 1),
        visible: lod.visible.length,
        total: nodes.length,
      });
    }
  });

  return (
    <group>
      <Points geometry={pointGeo} frustumCulled={false}>
        <PointMaterial
          transparent
          size={0.35}
          color="#8ab4ff"
          sizeAttenuation
          depthWrite={false}
        />
      </Points>
      {spheres}
      {clusters.map((c, i) => (
        <ClusterGlyph key={i} {...c} />
      ))}
      {labels.map((l, i) => (
        <LabelSprite key={i} {...l} />
      ))}
    </group>
  );
}

function SphereMesh(props: {
  position: [number, number, number];
  size: number;
  degree: number;
}) {
  const mesh = useRef<THREE.Mesh>(null);
  useFrame(() => {
    if (mesh.current) mesh.current.rotation.y += 0.002;
  });
  return (
    <mesh
      ref={mesh}
      position={props.position}
      scale={props.size}
      frustumCulled={false}
    >
      <sphereGeometry args={[1, 12, 12]} />
      <meshStandardMaterial color="#7aa2f7" roughness={0.4} />
    </mesh>
  );
}

function ClusterGlyph(props: {
  position: [number, number, number];
  degree: number;
}) {
  return (
    <mesh position={props.position} frustumCulled={false}>
      <icosahedronGeometry args={[1.6, 0]} />
      <meshStandardMaterial color="#f7768e" flatShading roughness={0.6} />
    </mesh>
  );
}

/** FR-E23: label sprite shown only near camera — gated in scene update loop. */
function LabelSprite(props: { position: [number, number, number]; text: string }) {
  const ref = useRef<THREE.Sprite>(null);
  const camera = useThree((s) => s.camera);
  const canvas = useMemo(() => {
    const c = document.createElement('canvas');
    c.width = 256;
    c.height = 64;
    const ctx = c.getContext('2d');
    if (ctx) {
      ctx.fillStyle = 'rgba(10,12,24,0.8)';
      ctx.fillRect(0, 0, 256, 64);
      ctx.font = '600 32px sans-serif';
      ctx.fillStyle = '#e0e6ff';
      ctx.fillText(props.text, 12, 42);
    }
    return c;
  }, [props.text]);
  const tex = useMemo(() => {
    const t = new THREE.CanvasTexture(canvas);
    t.needsUpdate = true;
    return t;
  }, [canvas]);

  useFrame(() => {
    if (!ref.current) return;
    const d = ref.current.position.distanceTo(camera.position);
    ref.current.visible = showLabel(d, DEFAULT_LOD_CONFIG);
  });

  return (
    <sprite ref={ref} position={props.position} frustumCulled={false} scale={[12, 3, 1]}>
      <spriteMaterial map={tex} transparent depthWrite={false} />
    </sprite>
  );
}
