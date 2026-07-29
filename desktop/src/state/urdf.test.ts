import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  matFromAxisAngle,
  matFromXyzRpy,
  movableJoints,
  multiplyMat4,
  originOf,
  parseUrdf,
  parseVec3,
  parseXml,
  solvePose,
  type Vec3,
} from './urdf.ts';

const HERE = path.dirname(fileURLToPath(import.meta.url));
function fixture(name: string): string {
  return readFileSync(path.join(HERE, 'testdata', name), 'utf8');
}

const SO100 = fixture('so100.urdf');
const SO101 = fixture('so101_new_calib.urdf');
const PANDA = fixture('panda.urdf');

// ── the XML subset ───────────────────────────────────────────────────────────

test('the prolog, comments and a doctype are skipped', () => {
  const el = parseXml(`<?xml version='1.0'?>\n<!-- lead -->\n<!DOCTYPE robot>\n<robot name="r"><link name="a"/></robot>`);
  assert.equal(el.tag, 'robot');
  assert.equal(el.attrs.name, 'r');
  assert.equal(el.children.length, 1);
});

test('attribute values may contain the characters that delimit tags', () => {
  // The reason the opening tag cannot be found with indexOf('>'): a value is
  // allowed to hold one, and scanning to the first '>' would truncate the tag.
  const el = parseXml(`<robot note="a > b" other='say "hi"'><link name="x"/></robot>`);
  assert.equal(el.attrs.note, 'a > b');
  assert.equal(el.attrs.other, 'say "hi"');
});

test('entities decode, and an unknown one is left as written', () => {
  const el = parseXml(`<robot a="&lt;&amp;&gt;&quot;&apos;" b="&#65;&#x42;" c="&nosuch;"/>`);
  assert.equal(el.attrs.a, `<&>"'`);
  assert.equal(el.attrs.b, 'AB');
  assert.equal(el.attrs.c, '&nosuch;');
});

test('a malformed document is refused, not half-read', () => {
  // Each of these could produce a plausible partial robot. A partial robot is a
  // wrong pose that looks right, which is the one outcome this panel must not
  // have.
  assert.throws(() => parseXml('<robot><link name="a"></robot>'), /does not match/);
  assert.throws(() => parseXml('<robot><link name="a"/>'), /unclosed tag <robot>/);
  assert.throws(() => parseXml('<robot/><other/>'), /more than one root/);
  assert.throws(() => parseXml('<robot name=unquoted/>'), /not quoted/);
  assert.throws(() => parseXml('<robot name/>'), /has no value/);
  assert.throws(() => parseXml('<robot><!-- never ends </robot>'), /unterminated XML comment/);
  assert.throws(() => parseXml('   '), /no XML element found/);
});

test('a triple falls back rather than throwing, per the URDF defaults', () => {
  assert.deepEqual(parseVec3('1 2 3', [0, 0, 0]), [1, 2, 3]);
  assert.deepEqual(parseVec3('  1   -2.5\n3 ', [0, 0, 0]), [1, -2.5, 3]);
  assert.deepEqual(parseVec3(undefined, [1, 0, 0]), [1, 0, 0]);
  assert.deepEqual(parseVec3('1 2', [1, 0, 0]), [1, 0, 0]);
  assert.deepEqual(parseVec3('1 2 nope', [1, 0, 0]), [1, 0, 0]);
});

// ── the real descriptions ────────────────────────────────────────────────────

test('the SO-100 description reads as the arm it is', () => {
  const m = parseUrdf(SO100);
  assert.equal(m.name, 'so_arm100');
  assert.equal(m.root, 'base');
  assert.deepEqual(m.links, ['base', 'shoulder', 'upper_arm', 'lower_arm', 'wrist', 'gripper', 'jaw']);
  assert.deepEqual(m.warnings, []);
  assert.deepEqual(
    m.joints.map((j) => j.name),
    ['shoulder_pan', 'shoulder_lift', 'elbow_flex', 'wrist_flex', 'wrist_roll', 'gripper'],
  );

  const pan = m.joints[0];
  assert.equal(pan.type, 'revolute');
  assert.equal(pan.parent, 'base');
  assert.equal(pan.child, 'shoulder');
  assert.deepEqual(pan.xyz, [0, -0.0452, 0.0165]);
  assert.deepEqual(pan.rpy, [1.57079, 0, 0]);
  assert.deepEqual(pan.axis, [0, 1, 0]);
  assert.equal(pan.lower, -2);
  assert.equal(pan.upper, 2);
});

test('SO-101 declares the same joints in the opposite order', () => {
  // This is why the manifest carries an explicit jointOrder: a positional
  // fallback that trusted file order would drive this arm backwards — gripper
  // where the shoulder should be — and still look like a moving robot.
  const m = parseUrdf(SO101);
  const movable = movableJoints(m).map((j) => j.name);
  assert.deepEqual(movable, ['gripper', 'wrist_roll', 'wrist_flex', 'elbow_flex', 'shoulder_lift', 'shoulder_pan']);
  const so100 = movableJoints(parseUrdf(SO100)).map((j) => j.name);
  assert.deepEqual([...movable].reverse(), so100);
});

test('a second vendor parses too — fixed and prismatic joints, an xacro namespace', () => {
  const m = parseUrdf(PANDA);
  assert.equal(m.name, 'panda');
  assert.equal(m.root, 'panda_link0');
  assert.equal(m.links.length, 13);
  assert.equal(m.joints.length, 12);
  // Only these three types are drivable; the fixed frames must not become
  // channels or a 7-DoF arm would look like a 12-DoF one.
  assert.deepEqual(
    movableJoints(m).map((j) => j.name),
    [
      'panda_joint1',
      'panda_joint2',
      'panda_joint3',
      'panda_joint4',
      'panda_joint5',
      'panda_joint6',
      'panda_joint7',
      'panda_finger_joint1',
      'panda_finger_joint2',
    ],
  );
  const finger = m.joints.find((j) => j.name === 'panda_finger_joint1');
  assert.equal(finger?.type, 'prismatic');
  assert.equal(finger?.upper, 0.04); // metres, not radians
});

test('structural nonsense throws with a sentence about the file', () => {
  assert.throws(() => parseUrdf('<thing/>'), /expected a <robot> root element/);
  assert.throws(() => parseUrdf('<robot/>'), /declares no links/);
  assert.throws(
    () => parseUrdf('<robot><link name="a"/><joint name="j" type="revolute"><parent link="a"/><child link="ghost"/></joint></robot>'),
    /undeclared child link "ghost"/,
  );
  assert.throws(
    () => parseUrdf('<robot><link name="a"/><link name="b"/><joint name="j" type="wobbly"><parent link="a"/><child link="b"/></joint></robot>'),
    /unsupported type "wobbly"/,
  );
  assert.throws(
    () =>
      parseUrdf(
        '<robot><link name="a"/><link name="b"/><link name="c"/>' +
          '<joint name="j1" type="fixed"><parent link="a"/><child link="c"/></joint>' +
          '<joint name="j2" type="fixed"><parent link="b"/><child link="c"/></joint></robot>',
      ),
    /child of more than one joint/,
  );
});

test('a detached link is drawn with a warning, not refused', () => {
  const m = parseUrdf(
    '<robot><link name="a"/><link name="b"/><link name="stray"/>' +
      '<joint name="j" type="fixed"><parent link="a"/><child link="b"/></joint></robot>',
  );
  assert.equal(m.root, 'a'); // the larger tree wins, not the first listed
  assert.equal(m.warnings.length, 1);
  assert.match(m.warnings[0], /2 unconnected roots/);
});

// ── matrices ─────────────────────────────────────────────────────────────────

test('rpy composes as fixed-axis Rz·Ry·Rx', () => {
  // Composed backwards this is still a rotation matrix and still draws a robot
  // — just the wrong one. Pinned against a hand-multiplied case: a 90° roll
  // then a 90° yaw sends +Y to +Z under Rz·Ry·Rx, and to -X under Rx·Ry·Rz.
  const m = matFromXyzRpy([0, 0, 0], [Math.PI / 2, 0, Math.PI / 2]);
  const y: Vec3 = [0, 1, 0];
  const got: Vec3 = [
    m[0] * y[0] + m[1] * y[1] + m[2] * y[2],
    m[4] * y[0] + m[5] * y[1] + m[6] * y[2],
    m[8] * y[0] + m[9] * y[1] + m[10] * y[2],
  ];
  close(got, [0, 0, 1]);
});

test('axis-angle turns about the axis it is given, right-handed', () => {
  const m = matFromAxisAngle([0, 0, 1], Math.PI / 2);
  close([m[0], m[4], m[8]], [0, 1, 0]); // +X goes to +Y
  // A degenerate axis is a broken joint declaration; identity beats NaN.
  assert.deepEqual(matFromAxisAngle([0, 0, 0], 1), matFromAxisAngle([0, 0, 1], 0));
});

test('translation rides through a product in the parent frame', () => {
  const parent = matFromXyzRpy([1, 0, 0], [0, 0, Math.PI / 2]);
  const localOffset = matFromXyzRpy([2, 0, 0], [0, 0, 0]);
  // The child is 2 along the parent's own +X, which now points along world +Y.
  close(originOf(multiplyMat4(parent, localOffset)), [1, 2, 0]);
});

// ── forward kinematics, against an independent reference ─────────────────────
//
// The expected numbers below were produced by a separate implementation (a
// short Python FK written from the URDF spec, not a port of this file) reading
// the same fixture. That is what makes them a check rather than a restatement
// — an A==B assertion between two copies of the same code catches nothing they
// both get wrong (`feedback_equivalence_test_blind_spot`). It bounds the claim
// honestly: shared *model* errors would survive, transcription, row/column and
// multiplication-order errors would not.

const SO100_ZERO: Array<[string, Vec3]> = [
  ['base', [0, 0, 0]],
  ['shoulder', [0, -0.0452, 0.0165]],
  ['upper_arm', [0, -0.0757993515, 0.1190001936]],
  ['lower_arm', [0, 0.0401883457, 0.1206910536]],
  ['wrist', [0, -0.0900017969, 0.1564062713]],
  ['gripper', [0, -0.1466296144, 0.1362742]],
  ['jaw', [-0.0000001278, -0.1763864627, 0.1471337569]],
];

const SO100_BENT: Array<[string, Vec3]> = [
  ['base', [0, 0, 0]],
  ['shoulder', [0, -0.0452, 0.0165]],
  ['upper_arm', [0.0146704215, -0.0720533779, 0.1190001699]],
  ['lower_arm', [-0.0146919895, -0.0183052218, 0.2175140747]],
  ['wrist', [0.0430787276, -0.124053425, 0.278382061]],
  ['gripper', [0.0691282654, -0.1717369467, 0.2526971756]],
  ['jaw', [0.0714494352, -0.2031293137, 0.2562371568]],
];

test('the zero pose matches the reference solver link for link', () => {
  const pose = solvePose(parseUrdf(SO100), {});
  for (const [name, want] of SO100_ZERO) {
    const got = pose.links.find((l) => l.name === name);
    assert.ok(got !== undefined, `${name} missing from the pose`);
    close(got.position, want, 1e-9, name);
  }
  assert.deepEqual(pose.clamped, []);
});

test('a bent pose matches the reference solver link for link', () => {
  const angles = {
    shoulder_pan: 0.5,
    shoulder_lift: 1.0,
    elbow_flex: -1.2,
    wrist_flex: 0.3,
    wrist_roll: -0.7,
    gripper: 0.4,
  };
  const pose = solvePose(parseUrdf(SO100), angles);
  for (const [name, want] of SO100_BENT) {
    const got = pose.links.find((l) => l.name === name);
    assert.ok(got !== undefined, `${name} missing from the pose`);
    close(got.position, want, 1e-9, name);
  }
  assert.deepEqual(pose.clamped, []);
});

/// SO-101, whose joint origins carry genuinely **multi-axis** rpy triples
/// (`shoulder_lift` is [-1.5708, -1.5708, 0]; `wrist_roll` is
/// [1.5708, 0.0486795, 3.14159]).
///
/// This case exists because SO-100 structurally cannot catch a wrong rpy
/// composition order: every one of its joint origins rotates about a single
/// axis, and a single-axis rotation is identical under Rz·Ry·Rx and Rx·Ry·Rz.
/// Six exact-looking assertions passed against a deliberately reversed
/// composition — the fixture agreed with the bug. Real fixtures are necessary,
/// not sufficient (`feedback_validate_negative_scan_harness`).
const SO101_BENT: Array<[string, Vec3]> = [
  ['base_link', [0, 0, 0]],
  ['shoulder_link', [0.0388353, -0.000000009, 0.0624]],
  ['upper_arm_link', [0.0597171829, -0.0286730721, 0.1165999239]],
  ['lower_arm_link', [-0.0054705019, -0.0011128911, 0.2085075247]],
  ['wrist_link', [0.1172557345, -0.0529997387, 0.1868032405]],
  ['gripper_link', [0.174978632, -0.0577528898, 0.1602270358]],
  ['gripper_frame_link', [0.2555788008, -0.0868359988, 0.1117581822]],
  ['moving_jaw_so101_v1_link', [0.2064667598, -0.0680496334, 0.1747705869]],
];

test('a multi-axis rpy chain matches the reference solver link for link', () => {
  const pose = solvePose(parseUrdf(SO101), {
    shoulder_pan: 0.4,
    shoulder_lift: -0.9,
    elbow_flex: 1.1,
    wrist_flex: 0.25,
    wrist_roll: -0.6,
    gripper: 0.35,
  });
  assert.equal(pose.links.length, SO101_BENT.length);
  for (const [name, want] of SO101_BENT) {
    const got = pose.links.find((l) => l.name === name);
    assert.ok(got !== undefined, `${name} missing from the pose`);
    close(got.position, want, 1e-9, name);
  }
});

test('an omitted <axis> is +X, which is the URDF default and not +Z', () => {
  // Every shipped description in testdata/ declares its axis explicitly, so
  // nothing else here can see this constant at all — a wrong default survived
  // the rest of the suite untouched.
  const m = parseUrdf(
    '<robot><link name="a"/><link name="b"/>' +
      '<joint name="j" type="revolute"><parent link="a"/><child link="b"/>' +
      '<origin xyz="0 1 0"/><limit lower="-3" upper="3"/></joint></robot>',
  );
  assert.deepEqual(m.joints[0].axis, [1, 0, 0]);
  // A quarter turn about +X carries the child's own offset from +Y to +Z; about
  // +Z it would have gone to -X.
  const child = solvePose(m, { j: Math.PI / 2 }).links[1];
  close(child.position, [0, 1, 0]); // the origin itself does not move…
  const tip: Vec3 = [
    child.matrix[0] * 0 + child.matrix[1] * 1 + child.matrix[2] * 0 + child.matrix[3],
    child.matrix[4] * 0 + child.matrix[5] * 1 + child.matrix[6] * 0 + child.matrix[7],
    child.matrix[8] * 0 + child.matrix[9] * 1 + child.matrix[10] * 0 + child.matrix[11],
  ];
  close(tip, [0, 1, 1]); // …but a point along its +Y swings to +Z
});

test('the arm actually moves, and stays the size of a desktop arm', () => {
  // A guard against the whole solver silently collapsing to identity, which
  // every "close to the reference" assertion above would still pass if the
  // reference were wrong in the same way.
  const m = parseUrdf(SO100);
  const zero = solvePose(m, {});
  const bent = solvePose(m, { shoulder_lift: 1.0, elbow_flex: -1.2 });
  const a = zero.links.find((l) => l.name === 'jaw')!.position;
  const b = bent.links.find((l) => l.name === 'jaw')!.position;
  assert.ok(Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]) > 0.05, 'the jaw barely moved');
  // SO-100 is a ~0.3 m desktop arm. Metres, not millimetres, not radians.
  assert.ok(zero.radius > 0.05 && zero.radius < 0.4, `radius ${zero.radius}`);
});

test('a segment spans the two link origins its joint connects', () => {
  const pose = solvePose(parseUrdf(SO100), {});
  assert.equal(pose.segments.length, 6);
  const first = pose.segments[0];
  assert.equal(first.joint, 'shoulder_pan');
  close(first.from, [0, 0, 0]);
  close(first.to, [0, -0.0452, 0.0165]);
});

test('an out-of-range value is clamped and named', () => {
  // Real LeRobot channels DO exceed their nominal range — one shipped SO-101
  // dataset peaks at 123 on a [-100,100] channel — so this is a normal state
  // to report rather than an error to swallow.
  const m = parseUrdf(SO100);
  const pose = solvePose(m, { shoulder_pan: 99, elbow_flex: -99 });
  assert.deepEqual(pose.clamped.sort(), ['elbow_flex', 'shoulder_pan']);
  const atLimit = solvePose(m, { shoulder_pan: 2, elbow_flex: -3.14158 });
  assert.deepEqual(atLimit.clamped, []); // exactly at the limit is inside it
});

test('a joint with no declared limits is not clamped to zero', () => {
  // lower === upper === 0 means the file declared none. Clamping to that pins
  // every such joint at its home position, which reads as a frozen robot.
  const m = parseUrdf(
    '<robot><link name="a"/><link name="b"/>' +
      '<joint name="spin" type="continuous"><parent link="a"/><child link="b"/>' +
      '<axis xyz="0 0 1"/><origin xyz="1 0 0"/></joint></robot>',
  );
  const pose = solvePose(m, { spin: 10 });
  assert.deepEqual(pose.clamped, []);
  close(pose.links[1].position, [1, 0, 0]); // rotation about its own origin
});

test('a NaN channel parks the joint rather than poisoning every descendant', () => {
  // A gap in the series is null on the wire and NaN here. One NaN multiplied
  // through a chain makes every downstream link NaN, and a robot drawn at NaN
  // is not drawn at all.
  const pose = solvePose(parseUrdf(SO100), { shoulder_lift: Number.NaN });
  for (const l of pose.links) {
    for (const c of l.position) assert.ok(Number.isFinite(c), `${l.name} is not finite`);
  }
});

function close(got: Vec3 | number[], want: Vec3 | number[], eps = 1e-9, label = ''): void {
  for (let i = 0; i < want.length; i += 1) {
    assert.ok(
      Math.abs(got[i] - want[i]) < eps,
      `${label} component ${i}: ${got[i]} vs ${want[i]} (Δ ${Math.abs(got[i] - want[i])})`,
    );
  }
}
