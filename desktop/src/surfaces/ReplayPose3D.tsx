import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useT } from '../i18n';
import { readForgeBlob } from '../state/forge';
import { frameDistance, orbitPosition, parseUrdf, solvePose, type Pose, type UrdfModel } from '../state/urdf';
import {
  ROBOT_DESCRIPTIONS,
  describeById,
  matchRobot,
  resolveJointValues,
  type JointResolution,
  type RobotDescription,
} from '../state/robotManifest';
import type { FeatureView } from '../state/replaySeries';

/// The 3D pose panel (J8 Replay W3b): the robot's articulated pose at the
/// timeline cursor, driven by the same state channels the plots draw.
///
/// **What this draws is a kinematic skeleton, not the robot's shell.** The
/// description's meshes are not fetched — see `state/urdf.ts` for why — so a
/// link is a segment between two joint origins. The panel says so rather than
/// letting a wireframe pass for a render.
///
/// Everything that can be wrong without looking wrong lives in the pure modules
/// (`urdf.ts`, `robotManifest.ts`), which is where it is asserted. What is left
/// here is a WebGL scene, a fetch, and pointer handling.

const FOV = 45;
const MIN_ZOOM = 0.3;
const MAX_ZOOM = 4;

interface SceneHandle {
  dispose: () => void;
  setPose: (pose: Pose | null) => void;
  setCamera: (target: [number, number, number], distance: number, azimuth: number, elevation: number) => void;
  resize: () => void;
}

export function ReplayPose3D({
  robotType,
  feature,
  cursorIndex,
}: {
  robotType: string;
  /// The feature whose channels drive the pose — `observation.state` where a
  /// dataset has one. Null when the episode has no numeric channels at all.
  feature: FeatureView | null;
  /// Index into the decimated series, or -1 when the cursor is off the plots.
  cursorIndex: number;
}): JSX.Element {
  const t = useT();
  const [pickedId, setPickedId] = useState<string | null>(null);
  const [azimuth, setAzimuth] = useState(-Math.PI / 4);
  const [elevation, setElevation] = useState(0.4);
  const [zoom, setZoom] = useState(1);
  const [glError, setGlError] = useState(false);

  const auto = matchRobot(robotType);
  const desc: RobotDescription | null = pickedId !== null ? describeById(pickedId) : auto;

  const urdfQ = useQuery({
    queryKey: ['urdf', desc?.repo ?? '', desc?.ref ?? '', desc?.urdfPath ?? ''],
    enabled: desc !== null,
    // Content at a pinned commit cannot change, so this never needs refetching
    // — and the forge budget is 60 unauthenticated calls an hour.
    staleTime: Infinity,
    gcTime: Infinity,
    retry: false,
    queryFn: async (): Promise<UrdfModel> => {
      const d = desc as RobotDescription;
      const text = await readForgeBlob({ id: d.repo, ref: d.ref, sha: d.ref }, 'github', d.urdfPath);
      return parseUrdf(text);
    },
  });

  const solved = useMemo((): { pose: Pose; resolution: JointResolution } | null => {
    const model = urdfQ.data;
    if (model === undefined || desc === null) return null;
    // No cursor yet means the episode's first frame, not a home pose: an arm
    // parked at all-zeros is a claim about the recording that is almost never
    // true.
    const at = cursorIndex >= 0 ? cursorIndex : 0;
    const channels = feature?.channels ?? [];
    const resolution = resolveJointValues(
      model,
      desc,
      channels.map((c) => c.name),
      channels.map((c) => c.values[at] ?? Number.NaN),
    );
    return { pose: solvePose(model, resolution.values), resolution };
  }, [urdfQ.data, desc, feature, cursorIndex]);

  // ── the WebGL scene ────────────────────────────────────────────────────────
  const hostRef = useRef<HTMLDivElement>(null);
  const sceneRef = useRef<SceneHandle | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const host = hostRef.current;
    if (host === null) return;
    let cancelled = false;
    let handle: SceneHandle | null = null;

    // three is ~1 MB and only this panel wants it; a static import would drag
    // it into the boot bundle for every job in the app.
    void (async () => {
      try {
        const THREE = await import('three');
        if (cancelled) return;
        handle = buildScene(THREE, host);
        sceneRef.current = handle;
        setReady(true);
      } catch {
        // A machine with no working WebGL context, or a chunk that failed to
        // load. Either way the panel says so instead of showing a blank box.
        if (!cancelled) setGlError(true);
      }
    })();

    return () => {
      cancelled = true;
      handle?.dispose();
      sceneRef.current = null;
      setReady(false);
    };
  }, []);

  useEffect(() => {
    if (!ready) return;
    sceneRef.current?.setPose(solved?.pose ?? null);
  }, [ready, solved]);

  useEffect(() => {
    if (!ready || solved === null) return;
    const { pose } = solved;
    sceneRef.current?.setCamera(pose.center, frameDistance(pose, FOV) / zoom, azimuth, elevation);
  }, [ready, solved, azimuth, elevation, zoom]);

  useEffect(() => {
    const host = hostRef.current;
    if (host === null || !ready) return;
    const ro = new ResizeObserver(() => sceneRef.current?.resize());
    ro.observe(host);
    return () => ro.disconnect();
  }, [ready]);

  // ── orbit ──────────────────────────────────────────────────────────────────
  const drag = useRef<{ x: number; y: number } | null>(null);

  const onPointerDown = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    // Pointer capture, so a drag that leaves the canvas keeps orbiting instead
    // of stopping dead at the edge.
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY };
  }, []);

  const onPointerMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    const from = drag.current;
    if (from === null) return;
    const dx = e.clientX - from.x;
    const dy = e.clientY - from.y;
    drag.current = { x: e.clientX, y: e.clientY };
    setAzimuth((a) => a - dx * 0.01);
    setElevation((el) => el + dy * 0.01);
  }, []);

  const onPointerUp = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
    drag.current = null;
  }, []);

  // ── states ─────────────────────────────────────────────────────────────────
  if (desc === null) {
    return (
      <div className="replay-pose">
        <div className="replay-pose-head small">
          <span>{t('replay.pose.title')}</span>
        </div>
        <div className="replay-pose-pick small">
          <span className="muted">
            {robotType.trim() === '' || robotType.trim().toLowerCase() === 'unknown'
              ? t('replay.pose.noRobotType')
              : t('replay.pose.unknownRobot').replace('{type}', robotType)}
          </span>
          <div className="replay-pose-choices">
            {ROBOT_DESCRIPTIONS.map((d) => (
              <button key={d.id} type="button" className="replay-toggle" onClick={() => setPickedId(d.id)}>
                {d.label}
              </button>
            ))}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="replay-pose">
      <div className="replay-pose-head small">
        <span>{t('replay.pose.title')}</span>
        <span className="muted mono">{desc.label}</span>
        <span className="muted">{desc.license}</span>
        <span className="spacer" />
        {pickedId !== null && auto?.id !== pickedId && (
          <button type="button" className="link-btn small" onClick={() => setPickedId(null)}>
            {t('replay.pose.reset')}
          </button>
        )}
        <button type="button" className="link-btn small" onClick={() => setZoom(1)}>
          {t('replay.pose.recenter')}
        </button>
      </div>

      {urdfQ.isPending && <div className="muted small region-pad">{t('replay.pose.loading')}</div>}
      {urdfQ.isError && (
        <div className="replay-error small">
          {t('replay.pose.failed')} {urdfQ.error instanceof Error ? urdfQ.error.message : String(urdfQ.error)}
        </div>
      )}
      {glError && <div className="replay-error small">{t('replay.pose.noWebgl')}</div>}

      {!urdfQ.isPending && !urdfQ.isError && !glError && (
        <>
          <div
            ref={hostRef}
            className="replay-pose-canvas"
            role="img"
            aria-label={t('replay.pose.title')}
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onPointerCancel={onPointerUp}
            onWheel={(e) => setZoom((z) => Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, z * (e.deltaY < 0 ? 1.1 : 1 / 1.1))))}
          />
          <div className="replay-pose-notes small muted">
            {/* Stated every time, not on hover: a skeleton that silently passes
                for a render is the failure mode of this whole panel. */}
            <div>{t('replay.pose.skeleton')}</div>
            {desc.angleUnit === 'normalized' && <div>{t('replay.pose.approximate')}</div>}
            {solved !== null && solved.resolution.strategy === 'position' && (
              <div>{t('replay.pose.positional')}</div>
            )}
            {solved !== null && solved.resolution.strategy === 'none' && <div>{t('replay.pose.nomatch')}</div>}
            {solved !== null && solved.resolution.unmapped.length > 0 && (
              <div>
                {t('replay.pose.unmapped')
                  .replace('{n}', String(solved.resolution.unmapped.length))
                  .replace('{names}', solved.resolution.unmapped.slice(0, 4).join(', '))}
              </div>
            )}
            {solved !== null && solved.pose.clamped.length > 0 && (
              <div>{t('replay.pose.clamped').replace('{names}', [...new Set(solved.pose.clamped)].join(', '))}</div>
            )}
            {(urdfQ.data?.warnings ?? []).map((w) => (
              <div key={w}>{w}</div>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

/// Colours by chain depth, generated rather than tokenized — the same argument
/// the plot traces make: N *distinguishable* colours are needed where N is the
/// robot's joint count, and the token palette is a fixed set of semantic roles
/// with no `--joint-4` in it.
function segmentHue(i: number): number {
  return ((i * 137.508) % 360) / 360;
}

/// Build the scene once. Everything after this is mutation of what it returns,
/// because a WebGL context is expensive to create and React will re-render this
/// panel on every cursor move.
function buildScene(THREE: typeof import('three'), host: HTMLElement): SceneHandle {
  const renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  host.appendChild(renderer.domElement);

  const scene = new THREE.Scene();
  const camera = new THREE.PerspectiveCamera(FOV, 1, 0.001, 1000);
  // URDF is Z-up and three.js defaults to Y-up. Without this the robot lies on
  // its side, which reads as a broken model rather than as a wrong camera.
  camera.up.set(0, 0, 1);

  const group = new THREE.Group();
  scene.add(group);

  const grid = new THREE.GridHelper(1, 10, 0x4a5162, 0x2a2f3a);
  grid.rotation.x = Math.PI / 2; // GridHelper is XZ; the robot's floor is XY
  scene.add(grid);

  const owned: Array<{ dispose: () => void }> = [];

  function clearGroup(): void {
    for (const o of owned.splice(0)) o.dispose();
    group.clear();
  }

  function render(): void {
    renderer.render(scene, camera);
  }

  function resize(): void {
    const w = Math.max(1, host.clientWidth);
    const h = Math.max(1, host.clientHeight);
    renderer.setSize(w, h, false);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
    render();
  }

  function setPose(pose: Pose | null): void {
    clearGroup();
    if (pose === null) {
      render();
      return;
    }

    const positions: number[] = [];
    const colors: number[] = [];
    const colour = new THREE.Color();
    pose.segments.forEach((s, i) => {
      positions.push(s.from[0], s.from[1], s.from[2], s.to[0], s.to[1], s.to[2]);
      colour.setHSL(segmentHue(i), 0.65, 0.55);
      // Both ends of a segment carry its colour, so a link reads as one limb
      // rather than as a gradient between two joints.
      colors.push(colour.r, colour.g, colour.b, colour.r, colour.g, colour.b);
    });

    if (positions.length > 0) {
      const geo = new THREE.BufferGeometry();
      geo.setAttribute('position', new THREE.Float32BufferAttribute(positions, 3));
      geo.setAttribute('color', new THREE.Float32BufferAttribute(colors, 3));
      const mat = new THREE.LineBasicMaterial({ vertexColors: true });
      group.add(new THREE.LineSegments(geo, mat));
      owned.push(geo, mat);
    }

    // A dot at every link origin, sized against the robot so a 0.3 m arm and a
    // 1.8 m humanoid get proportionate markers rather than the same pixels.
    const joints: number[] = [];
    for (const l of pose.links) joints.push(l.position[0], l.position[1], l.position[2]);
    if (joints.length > 0) {
      const geo = new THREE.BufferGeometry();
      geo.setAttribute('position', new THREE.Float32BufferAttribute(joints, 3));
      const mat = new THREE.PointsMaterial({ color: 0xd6dae4, size: Math.max(pose.radius, 0.05) * 0.07 });
      group.add(new THREE.Points(geo, mat));
      owned.push(geo, mat);
    }

    const axes = new THREE.AxesHelper(Math.max(pose.radius, 0.05) * 0.4);
    group.add(axes);
    owned.push({ dispose: () => axes.dispose() });

    const span = Math.max(pose.radius, 0.05) * 4;
    grid.scale.setScalar(span);
    render();
  }

  function setCamera(
    target: [number, number, number],
    distance: number,
    azimuth: number,
    elevation: number,
  ): void {
    const p = orbitPosition(target, distance, azimuth, elevation);
    camera.position.set(p[0], p[1], p[2]);
    camera.lookAt(target[0], target[1], target[2]);
    camera.near = Math.max(distance / 1000, 0.001);
    camera.far = distance * 10;
    camera.updateProjectionMatrix();
    render();
  }

  resize();

  return {
    setPose,
    setCamera,
    resize,
    dispose: () => {
      clearGroup();
      grid.geometry.dispose();
      (Array.isArray(grid.material) ? grid.material : [grid.material]).forEach((m) => m.dispose());
      renderer.dispose();
      // Without this the GPU context survives until GC, and a browser caps how
      // many are live at once — open a few episodes and later panels go blank.
      renderer.forceContextLoss();
      renderer.domElement.remove();
    },
  };
}
