/// A minimal URDF reader and forward-kinematics solver (J8 Replay W3).
///
/// **Why not `urdf-loader`.** The plan named it, and it is the obvious pick
/// until you look at what it actually buys here. It builds a `THREE.Object3D`
/// graph and its value is loading the meshes a URDF points at — through three's
/// `LoadingManager`, i.e. plain XHR. Every outbound request in this app goes
/// through the proxy-aware forge IPC instead (`state/forge.ts`), and
/// `readForgeBlob` decodes UTF-8, so binary STL/DAE is not reachable by that
/// route at all. Meshes are therefore out of round 1 — which leaves the joint
/// tree, and that is this file. The second reason is verification: this surface
/// cannot be looked at, and `URDFLoader.parse` needs a DOM, so its arm of the
/// pipeline would be untestable under `node --test`. Everything here is pure,
/// so the geometry is asserted rather than eyeballed
/// (`feedback_pure_layout_module_is_testable_eyes`).
///
/// What this renders is a **kinematic skeleton**: link frames and the segments
/// between them, driven by joint angles. Not the robot's shell. That is a real
/// limitation, stated in the panel rather than hidden.

export type Vec3 = [number, number, number];

/// Row-major 4x4. Row-major because these get read in tests; three.js's
/// `Matrix4.set` also takes its arguments row-major, so handing one over is a
/// spread, not a transpose.
export type Mat4 = number[];

export type JointType = 'revolute' | 'continuous' | 'prismatic' | 'fixed' | 'floating' | 'planar';

const JOINT_TYPES: readonly string[] = ['revolute', 'continuous', 'prismatic', 'fixed', 'floating', 'planar'];

export interface UrdfJoint {
  name: string;
  type: JointType;
  parent: string;
  child: string;
  /// The joint origin in the parent link's frame.
  xyz: Vec3;
  rpy: Vec3;
  /// Rotation (or translation) axis in the joint frame. The URDF default is
  /// (1,0,0) — not (0,0,1), and not "whatever was in the file".
  axis: Vec3;
  /// Limits in radians (revolute) or metres (prismatic). Both 0 means the file
  /// declared none, which is legal for `continuous` and `fixed`.
  lower: number;
  upper: number;
}

export interface UrdfModel {
  name: string;
  /// The link no joint names as a child.
  root: string;
  links: string[];
  joints: UrdfJoint[];
  /// Structural oddities that did not stop the parse. A robot that draws with a
  /// caveat beats one that refuses to draw.
  warnings: string[];
}

/// Element budget. A URDF is a few hundred elements; a robot description with
/// millions is either not a robot description or not something to hold in a
/// renderer. The standing "no uncapped reads" anchor, applied to a parse.
const MAX_ELEMENTS = 200_000;
const MAX_DEPTH = 64;

// ── a very small XML reader ──────────────────────────────────────────────────
// Deliberately not `DOMParser`: that exists in the renderer but not under
// `node --test`, and a parser only half the pipeline can exercise is a parser
// whose bugs ship. This understands the subset URDF uses and REFUSES the rest,
// because a silently mis-parsed robot is a plausible-looking wrong pose.

export interface XmlElement {
  tag: string;
  attrs: Record<string, string>;
  children: XmlElement[];
}

const NAMED_ENTITIES: Record<string, string> = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
};

function decodeEntities(s: string): string {
  if (!s.includes('&')) return s;
  return s.replace(/&(#[Xx]?[0-9A-Fa-f]+|[A-Za-z]+);/g, (whole: string, body: string) => {
    if (body.startsWith('#')) {
      const hex = body[1] === 'x' || body[1] === 'X';
      const code = Number.parseInt(hex ? body.slice(2) : body.slice(1), hex ? 16 : 10);
      // An out-of-range code point would throw out of `fromCodePoint`; leaving
      // the text as written is the recoverable answer.
      if (!Number.isFinite(code) || code <= 0 || code > 0x10ffff) return whole;
      return String.fromCodePoint(code);
    }
    return NAMED_ENTITIES[body] ?? whole;
  });
}

function isSpace(c: string): boolean {
  return c === ' ' || c === '\t' || c === '\n' || c === '\r';
}

/// Parse an XML document into its single root element. Throws with a message
/// naming the problem — every caller renders that message, so it has to read
/// like a sentence about the file rather than like a stack frame.
export function parseXml(src: string): XmlElement {
  const stack: XmlElement[] = [];
  let root: XmlElement | null = null;
  let count = 0;
  let i = 0;

  while (i < src.length) {
    const lt = src.indexOf('<', i);
    if (lt < 0) break;
    i = lt;

    if (src.startsWith('<!--', i)) {
      const end = src.indexOf('-->', i + 4);
      if (end < 0) throw new Error('unterminated XML comment');
      i = end + 3;
      continue;
    }
    if (src.startsWith('<?', i)) {
      const end = src.indexOf('?>', i + 2);
      if (end < 0) throw new Error('unterminated XML declaration');
      i = end + 2;
      continue;
    }
    if (src.startsWith('<!', i)) {
      // <!DOCTYPE …>. URDF has no meaningful text content, so CDATA cannot
      // carry anything this reader needs either.
      const end = src.indexOf('>', i + 2);
      if (end < 0) throw new Error('unterminated XML directive');
      i = end + 1;
      continue;
    }
    if (src.startsWith('</', i)) {
      const end = src.indexOf('>', i + 2);
      if (end < 0) throw new Error('unterminated closing tag');
      const name = src.slice(i + 2, end).trim();
      const open = stack.pop();
      if (open === undefined) throw new Error(`closing tag </${name}> with nothing open`);
      if (open.tag !== name) throw new Error(`closing tag </${name}> does not match <${open.tag}>`);
      i = end + 1;
      continue;
    }

    // An opening tag. The end cannot be found with indexOf('>') because an
    // attribute value may legally contain one.
    let j = i + 1;
    while (j < src.length && !isSpace(src[j]) && src[j] !== '/' && src[j] !== '>') j += 1;
    const tag = src.slice(i + 1, j);
    if (tag === '') throw new Error('empty tag name');

    const attrs: Record<string, string> = {};
    let selfClose = false;
    let closed = false;
    while (j < src.length) {
      while (j < src.length && isSpace(src[j])) j += 1;
      if (src[j] === '>') {
        j += 1;
        closed = true;
        break;
      }
      if (src.startsWith('/>', j)) {
        selfClose = true;
        closed = true;
        j += 2;
        break;
      }
      const nameStart = j;
      while (j < src.length && !isSpace(src[j]) && src[j] !== '=' && src[j] !== '/' && src[j] !== '>') j += 1;
      const name = src.slice(nameStart, j);
      if (name === '') throw new Error(`malformed attribute in <${tag}>`);
      while (j < src.length && isSpace(src[j])) j += 1;
      if (src[j] !== '=') throw new Error(`attribute ${name} of <${tag}> has no value`);
      j += 1;
      while (j < src.length && isSpace(src[j])) j += 1;
      const quote = src[j];
      if (quote !== '"' && quote !== "'") throw new Error(`attribute ${name} of <${tag}> is not quoted`);
      const close = src.indexOf(quote, j + 1);
      if (close < 0) throw new Error(`unterminated value for ${name} of <${tag}>`);
      attrs[name] = decodeEntities(src.slice(j + 1, close));
      j = close + 1;
    }
    if (!closed) throw new Error(`unterminated tag <${tag}>`);

    count += 1;
    if (count > MAX_ELEMENTS) throw new Error(`XML has more than ${MAX_ELEMENTS} elements`);

    const el: XmlElement = { tag, attrs, children: [] };
    const parent = stack[stack.length - 1];
    if (parent !== undefined) parent.children.push(el);
    else if (root === null) root = el;
    else throw new Error('XML has more than one root element');

    if (!selfClose) {
      stack.push(el);
      if (stack.length > MAX_DEPTH) throw new Error(`XML nested deeper than ${MAX_DEPTH}`);
    }
    i = j;
  }

  if (stack.length > 0) throw new Error(`unclosed tag <${stack[stack.length - 1].tag}>`);
  if (root === null) throw new Error('no XML element found');
  return root;
}

function child(el: XmlElement, tag: string): XmlElement | undefined {
  return el.children.find((c) => c.tag === tag);
}

/// Read a whitespace-separated triple. URDF writes these as `xyz="0 -0.045 0.16"`,
/// and a missing or unreadable one falls back rather than throwing — the spec
/// gives every one of them a default.
export function parseVec3(s: string | undefined, fallback: Vec3): Vec3 {
  if (s === undefined) return fallback;
  const parts = s.trim().split(/\s+/).filter((p) => p !== '');
  if (parts.length !== 3) return fallback;
  const out: Vec3 = [0, 0, 0];
  for (let k = 0; k < 3; k += 1) {
    const v = Number(parts[k]);
    if (!Number.isFinite(v)) return fallback;
    out[k] = v;
  }
  return out;
}

function parseNum(s: string | undefined, fallback: number): number {
  if (s === undefined) return fallback;
  const v = Number(s);
  return Number.isFinite(v) ? v : fallback;
}

// ── URDF ─────────────────────────────────────────────────────────────────────

/// Parse a URDF document into links and joints.
///
/// Structural problems that make the result meaningless throw; ones that leave
/// a drawable robot are collected as warnings. The line between them is whether
/// a caller could be misled: a joint naming a link that does not exist would
/// silently drop part of the arm, so it throws.
export function parseUrdf(xml: string): UrdfModel {
  const robot = parseXml(xml);
  if (robot.tag !== 'robot') throw new Error(`expected a <robot> root element, found <${robot.tag}>`);

  const warnings: string[] = [];
  const links: string[] = [];
  const seen = new Set<string>();
  for (const el of robot.children) {
    if (el.tag !== 'link') continue;
    const name = el.attrs.name;
    if (name === undefined || name === '') throw new Error('a <link> has no name');
    if (seen.has(name)) throw new Error(`duplicate link "${name}"`);
    seen.add(name);
    links.push(name);
  }
  if (links.length === 0) throw new Error('the URDF declares no links');

  const joints: UrdfJoint[] = [];
  const jointNames = new Set<string>();
  const childOf = new Set<string>();
  for (const el of robot.children) {
    if (el.tag !== 'joint') continue;
    const name = el.attrs.name;
    if (name === undefined || name === '') throw new Error('a <joint> has no name');
    if (jointNames.has(name)) throw new Error(`duplicate joint "${name}"`);
    jointNames.add(name);

    const type = el.attrs.type ?? '';
    if (!JOINT_TYPES.includes(type)) throw new Error(`joint "${name}" has unsupported type "${type}"`);

    const parent = child(el, 'parent')?.attrs.link ?? '';
    const childLink = child(el, 'child')?.attrs.link ?? '';
    if (parent === '') throw new Error(`joint "${name}" has no parent link`);
    if (childLink === '') throw new Error(`joint "${name}" has no child link`);
    if (!seen.has(parent)) throw new Error(`joint "${name}" names an undeclared parent link "${parent}"`);
    if (!seen.has(childLink)) throw new Error(`joint "${name}" names an undeclared child link "${childLink}"`);
    // Two joints claiming one child is not a tree, and the second would silently
    // win the walk below.
    if (childOf.has(childLink)) throw new Error(`link "${childLink}" is the child of more than one joint`);
    childOf.add(childLink);

    const origin = child(el, 'origin');
    const limit = child(el, 'limit');
    joints.push({
      name,
      type: type as JointType,
      parent,
      child: childLink,
      xyz: parseVec3(origin?.attrs.xyz, [0, 0, 0]),
      rpy: parseVec3(origin?.attrs.rpy, [0, 0, 0]),
      axis: parseVec3(child(el, 'axis')?.attrs.xyz, [1, 0, 0]),
      lower: parseNum(limit?.attrs.lower, 0),
      upper: parseNum(limit?.attrs.upper, 0),
    });
  }

  const roots = links.filter((l) => !childOf.has(l));
  if (roots.length === 0) throw new Error('every link is a child — the URDF has a cycle, not a tree');
  let root = roots[0];
  if (roots.length > 1) {
    // Detached links are common in hand-edited descriptions. Drawing the
    // largest tree and saying so beats refusing a robot that is mostly fine.
    root = largestTreeRoot(roots, joints);
    warnings.push(`the URDF has ${roots.length} unconnected roots (${roots.join(', ')}); drawing the tree under "${root}"`);
  }

  return { name: robot.attrs.name ?? '', root, links, joints, warnings };
}

function largestTreeRoot(roots: string[], joints: UrdfJoint[]): string {
  const kids = new Map<string, string[]>();
  for (const j of joints) {
    const list = kids.get(j.parent);
    if (list === undefined) kids.set(j.parent, [j.child]);
    else list.push(j.child);
  }
  let best = roots[0];
  let bestSize = -1;
  for (const r of roots) {
    let size = 0;
    const queue = [r];
    const visited = new Set<string>();
    while (queue.length > 0) {
      const n = queue.pop() as string;
      if (visited.has(n)) continue;
      visited.add(n);
      size += 1;
      for (const c of kids.get(n) ?? []) queue.push(c);
    }
    if (size > bestSize) {
      bestSize = size;
      best = r;
    }
  }
  return best;
}

/// The joints a state vector can drive. `fixed`/`floating`/`planar` are not
/// single-value joints, so they are never candidates for a channel.
export function movableJoints(model: UrdfModel): UrdfJoint[] {
  return model.joints.filter((j) => j.type === 'revolute' || j.type === 'continuous' || j.type === 'prismatic');
}

// ── matrices ─────────────────────────────────────────────────────────────────

export function identityMat4(): Mat4 {
  return [1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1];
}

export function multiplyMat4(a: Mat4, b: Mat4): Mat4 {
  const out = new Array<number>(16).fill(0);
  for (let r = 0; r < 4; r += 1) {
    for (let c = 0; c < 4; c += 1) {
      let sum = 0;
      for (let k = 0; k < 4; k += 1) sum += a[r * 4 + k] * b[k * 4 + c];
      out[r * 4 + c] = sum;
    }
  }
  return out;
}

/// URDF's `rpy` is a **fixed-axis** roll-pitch-yaw, i.e. R = Rz(yaw)·Ry(pitch)·Rx(roll).
/// Getting the order backwards produces a robot that is subtly, confidently
/// wrong — which is the failure mode this whole panel has to avoid.
export function matFromXyzRpy(xyz: Vec3, rpy: Vec3): Mat4 {
  const [cr, sr] = [Math.cos(rpy[0]), Math.sin(rpy[0])];
  const [cp, sp] = [Math.cos(rpy[1]), Math.sin(rpy[1])];
  const [cy, sy] = [Math.cos(rpy[2]), Math.sin(rpy[2])];
  return [
    cy * cp, cy * sp * sr - sy * cr, cy * sp * cr + sy * sr, xyz[0],
    sy * cp, sy * sp * sr + cy * cr, sy * sp * cr - cy * sr, xyz[1],
    -sp, cp * sr, cp * cr, xyz[2],
    0, 0, 0, 1,
  ];
}

/// Rodrigues' rotation about an arbitrary axis. A zero-length axis is a broken
/// joint declaration; it yields identity rather than NaN everywhere downstream.
export function matFromAxisAngle(axis: Vec3, angle: number): Mat4 {
  const len = Math.hypot(axis[0], axis[1], axis[2]);
  if (!Number.isFinite(len) || len === 0 || !Number.isFinite(angle)) return identityMat4();
  const [x, y, z] = [axis[0] / len, axis[1] / len, axis[2] / len];
  const c = Math.cos(angle);
  const s = Math.sin(angle);
  const t = 1 - c;
  return [
    t * x * x + c, t * x * y - s * z, t * x * z + s * y, 0,
    t * x * y + s * z, t * y * y + c, t * y * z - s * x, 0,
    t * x * z - s * y, t * y * z + s * x, t * z * z + c, 0,
    0, 0, 0, 1,
  ];
}

function matFromTranslation(v: Vec3): Mat4 {
  return [1, 0, 0, v[0], 0, 1, 0, v[1], 0, 0, 1, v[2], 0, 0, 0, 1];
}

export function originOf(m: Mat4): Vec3 {
  return [m[3], m[7], m[11]];
}

// ── forward kinematics ───────────────────────────────────────────────────────

export interface PoseLink {
  name: string;
  matrix: Mat4;
  position: Vec3;
}

export interface PoseSegment {
  joint: string;
  type: JointType;
  from: Vec3;
  to: Vec3;
}

export interface Pose {
  links: PoseLink[];
  segments: PoseSegment[];
  /// Bounding-box centre and the radius that contains every link origin. The
  /// renderer frames the camera from these instead of guessing a scale — a
  /// 0.4 m arm and a 1.8 m humanoid are the same picture, differently zoomed.
  center: Vec3;
  radius: number;
  /// Joints whose requested value fell outside the URDF limit and was clamped.
  /// Real LeRobot channels do exceed their nominal range, so this is a normal
  /// state to report, not an error to swallow.
  clamped: string[];
}

/// Place every link in the robot's base frame for one set of joint values.
///
/// Values are radians for revolute/continuous joints and metres for prismatic
/// ones — the URDF's own units. Converting a dataset's channels into them is
/// `robotManifest.ts`'s job, because that conversion is a property of the robot
/// the data came from, not of the description file.
export function solvePose(model: UrdfModel, values: Record<string, number>): Pose {
  const kids = new Map<string, UrdfJoint[]>();
  for (const j of model.joints) {
    const list = kids.get(j.parent);
    if (list === undefined) kids.set(j.parent, [j]);
    else list.push(j);
  }

  const links: PoseLink[] = [];
  const segments: PoseSegment[] = [];
  const clamped: string[] = [];
  const visited = new Set<string>();

  const queue: Array<{ link: string; matrix: Mat4 }> = [{ link: model.root, matrix: identityMat4() }];
  while (queue.length > 0) {
    const node = queue.shift() as { link: string; matrix: Mat4 };
    if (visited.has(node.link)) continue;
    visited.add(node.link);
    links.push({ name: node.link, matrix: node.matrix, position: originOf(node.matrix) });

    for (const j of kids.get(node.link) ?? []) {
      const raw = values[j.name];
      let value = Number.isFinite(raw) ? (raw as number) : 0;
      // `continuous` has no limits by definition, and `lower === upper` means
      // the file declared none — clamping to that would pin every joint at 0.
      if (j.type !== 'continuous' && j.upper > j.lower) {
        const fixed = Math.min(j.upper, Math.max(j.lower, value));
        if (fixed !== value) {
          clamped.push(j.name);
          value = fixed;
        }
      }
      const local =
        j.type === 'prismatic'
          ? matFromTranslation([j.axis[0] * value, j.axis[1] * value, j.axis[2] * value])
          : j.type === 'revolute' || j.type === 'continuous'
            ? matFromAxisAngle(j.axis, value)
            : identityMat4();
      const matrix = multiplyMat4(multiplyMat4(node.matrix, matFromXyzRpy(j.xyz, j.rpy)), local);
      segments.push({ joint: j.name, type: j.type, from: originOf(node.matrix), to: originOf(matrix) });
      queue.push({ link: j.child, matrix });
    }
  }

  return { links, segments, ...extentOf(links), clamped };
}

function extentOf(links: PoseLink[]): { center: Vec3; radius: number } {
  if (links.length === 0) return { center: [0, 0, 0], radius: 0 };
  const min: Vec3 = [Infinity, Infinity, Infinity];
  const max: Vec3 = [-Infinity, -Infinity, -Infinity];
  for (const l of links) {
    for (let k = 0; k < 3; k += 1) {
      if (l.position[k] < min[k]) min[k] = l.position[k];
      if (l.position[k] > max[k]) max[k] = l.position[k];
    }
  }
  const center: Vec3 = [(min[0] + max[0]) / 2, (min[1] + max[1]) / 2, (min[2] + max[2]) / 2];
  let radius = 0;
  for (const l of links) {
    const d = Math.hypot(l.position[0] - center[0], l.position[1] - center[1], l.position[2] - center[2]);
    if (d > radius) radius = d;
  }
  return { center, radius };
}
