import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseUrdf } from './urdf.ts';
import {
  ROBOT_DESCRIPTIONS,
  channelJointName,
  describeById,
  isPlaceholderChannel,
  matchRobot,
  normalizeRobotType,
  resolveJointValues,
} from './robotManifest.ts';

const HERE = path.dirname(fileURLToPath(import.meta.url));
function model(name: string) {
  return parseUrdf(readFileSync(path.join(HERE, 'testdata', name), 'utf8'));
}

const SO100 = model('so100.urdf');
const SO101 = model('so101_new_calib.urdf');
const PANDA = model('panda.urdf');

// The channel names below are copied from live `meta/info.json` files:
// `lerobot/svla_so101_pickplace` (named, `.pos`-suffixed), and
// `maximilienroberti/so100_test` (an SO-100 recording that declares
// robot_type "unknown" and names its channels motor_0…motor_5).
const SO_NAMED = ['shoulder_pan.pos', 'shoulder_lift.pos', 'elbow_flex.pos', 'wrist_flex.pos', 'wrist_roll.pos', 'gripper.pos'];
const SO_PLACEHOLDER = ['motor_0', 'motor_1', 'motor_2', 'motor_3', 'motor_4', 'motor_5'];

test('every manifest entry points at a description we have actually read', () => {
  // The rule this file is here to keep: an entry is added by fetching the file
  // and reading its joints, never by trusting a robot's documentation.
  for (const d of ROBOT_DESCRIPTIONS) {
    assert.ok(d.repo.includes('/'), `${d.id}: repo is not owner/name`);
    assert.match(d.ref, /^[0-9a-f]{40}$/, `${d.id}: ref must be a pinned commit SHA, not a branch`);
    assert.ok(d.license !== '', `${d.id}: no licence recorded`);
    assert.ok(d.jointOrder.length > 0, `${d.id}: no joint order`);
    assert.equal(describeById(d.id)?.id, d.id);
  }
  const ids = ROBOT_DESCRIPTIONS.map((d) => d.id);
  assert.equal(new Set(ids).size, ids.length, 'duplicate description id');
  const keys = ROBOT_DESCRIPTIONS.flatMap((d) => d.robotTypes);
  assert.equal(new Set(keys).size, keys.length, 'two descriptions claim the same robot_type');
});

test("the manifest's joint order names joints the description really has", () => {
  // A typo here is invisible at runtime — the joint simply never moves — and
  // silently downgrades the whole arm to a partial match.
  const models: Record<string, ReturnType<typeof model>> = {
    so_arm100: SO100,
    so_arm101: SO101,
    panda: PANDA,
  };
  for (const d of ROBOT_DESCRIPTIONS) {
    const m = models[d.id];
    assert.ok(m !== undefined, `${d.id}: no fixture to check against`);
    const names = new Set(m.joints.map((j) => j.name));
    for (const j of d.jointOrder) assert.ok(names.has(j), `${d.id}: jointOrder names "${j}", which the URDF does not`);
    for (const j of Object.keys(d.normalizedRanges ?? {})) {
      assert.ok(names.has(j), `${d.id}: normalizedRanges names "${j}", which the URDF does not`);
    }
  }
});

test('a robot_type folds to a match key', () => {
  assert.equal(normalizeRobotType('so100_follower'), 'so100');
  assert.equal(normalizeRobotType('  so100_leader '), 'so100');
  // Separators fold to '_' rather than being deleted, so the hyphenated human
  // spelling is a DIFFERENT key from the bare one. Both are enumerated on the
  // entry instead of being smoothed together here — spelling out the aliases a
  // description answers to is easier to audit than a normalizer that guesses.
  assert.equal(normalizeRobotType('SO-101'), 'so_101');
  assert.equal(matchRobot('SO-101')?.id, 'so_arm101');
  assert.equal(normalizeRobotType('Franka Panda'), 'franka_panda');
  assert.equal(normalizeRobotType(''), '');
});

test('the common real robot_type values select the right arm', () => {
  assert.equal(matchRobot('so100')?.id, 'so_arm100');
  assert.equal(matchRobot('so100_follower')?.id, 'so_arm100');
  assert.equal(matchRobot('SO-ARM100')?.id, 'so_arm100');
  assert.equal(matchRobot('so101')?.id, 'so_arm101');
  assert.equal(matchRobot('franka')?.id, 'panda');
  // "unknown" is a value, not an absence, and it is the single most common one
  // in the wild — including on datasets that plainly are an SO-100. It must
  // resolve to "pick one yourself", never to a guess.
  assert.equal(matchRobot('unknown'), null);
  assert.equal(matchRobot(''), null);
  assert.equal(matchRobot('some_arm_we_have_never_seen'), null);
});

test('a mislabelled dataset matches what it declared, not what it is', () => {
  // `lerobot/svla_so101_pickplace` is an SO-101 recording whose info.json says
  // robot_type "so100_follower". The manifest follows the declaration — the
  // two arms share every joint name, so the pose is right either way, and
  // second-guessing a declared field on the strength of a repo NAME is how you
  // get a confidently wrong robot.
  assert.equal(matchRobot('so100_follower')?.id, 'so_arm100');
});

test('a measurement suffix is not part of the joint name', () => {
  assert.equal(channelJointName('shoulder_pan.pos'), 'shoulder_pan');
  assert.equal(channelJointName('Wrist_Roll.POSITION'), 'wrist_roll');
  assert.equal(channelJointName('gripper.vel'), 'gripper');
  assert.equal(channelJointName('elbow_flex'), 'elbow_flex');
  // Not a suffix this strips — a dotted name that means something else stays.
  assert.equal(channelJointName('observation.state'), 'observation.state');
});

test('a placeholder channel name is treated as no name at all', () => {
  for (const c of ['motor_0', 'motor_5', 'joint_1', 'axis_2', '3', '', '  ']) {
    assert.ok(isPlaceholderChannel(c), `${JSON.stringify(c)} should be a placeholder`);
  }
  for (const c of ['shoulder_pan', 'gripper.pos', 'left_waist', 'panda_joint1']) {
    assert.ok(!isPlaceholderChannel(c), `${c} should not be a placeholder`);
  }
});

test('named channels drive the joints they name', () => {
  const r = resolveJointValues(SO100, describeById('so_arm100')!, SO_NAMED, [0, 0, 0, 0, 0, 50]);
  assert.equal(r.strategy, 'name');
  assert.equal(r.matched, 6);
  assert.deepEqual(r.unmapped, []);
  assert.deepEqual(Object.keys(r.values).sort(), ['elbow_flex', 'gripper', 'shoulder_lift', 'shoulder_pan', 'wrist_flex', 'wrist_roll']);
});

test('placeholder channels fall back to the manifest order, not the file order', () => {
  // SO-101 declares its joints gripper-first. Falling back to file order would
  // put motor_0 — the shoulder — on the gripper, and every subsequent channel
  // one joint further wrong, while still animating convincingly.
  const r = resolveJointValues(SO101, describeById('so_arm101')!, SO_PLACEHOLDER, [100, 0, 0, 0, 0, 0]);
  assert.equal(r.strategy, 'position');
  assert.equal(r.matched, 6);
  const pan = SO101.joints.find((j) => j.name === 'shoulder_pan')!;
  assert.equal(r.values.shoulder_pan, pan.upper); // +100 lands on the upper limit
  assert.notEqual(r.values.gripper, SO101.joints.find((j) => j.name === 'gripper')!.upper);
});

test('normalized channels map onto the joint\'s own limits', () => {
  const desc = describeById('so_arm100')!;
  const pan = SO100.joints.find((j) => j.name === 'shoulder_pan')!;
  const ends = resolveJointValues(SO100, desc, ['shoulder_pan.pos'], [-100]).values.shoulder_pan;
  assert.equal(ends, pan.lower);
  assert.equal(resolveJointValues(SO100, desc, ['shoulder_pan.pos'], [100]).values.shoulder_pan, pan.upper);
  assert.equal(resolveJointValues(SO100, desc, ['shoulder_pan.pos'], [0]).values.shoulder_pan, 0);
  // elbow_flex is [-3.14158, 0] — an asymmetric joint, so a zero channel is NOT
  // a zero angle. Assuming otherwise would fold every asymmetric joint straight.
  const elbow = SO100.joints.find((j) => j.name === 'elbow_flex')!;
  const mid = resolveJointValues(SO100, desc, ['elbow_flex.pos'], [0]).values.elbow_flex;
  assert.ok(Math.abs(mid - (elbow.lower + elbow.upper) / 2) < 1e-12, `${mid}`);
});

test('the gripper is 0..100, not -100..100', () => {
  // Verified against lerobot/robots/so_follower/so_follower.py, whose motor
  // table gives the gripper RANGE_0_100 and every body joint RANGE_M100_100 —
  // and against two shipped datasets whose gripper channel never goes negative.
  // Read as symmetric, a fully closed gripper would draw half open.
  const desc = describeById('so_arm100')!;
  const jaw = SO100.joints.find((j) => j.name === 'gripper')!;
  assert.equal(resolveJointValues(SO100, desc, ['gripper.pos'], [0]).values.gripper, jaw.lower);
  assert.equal(resolveJointValues(SO100, desc, ['gripper.pos'], [100]).values.gripper, jaw.upper);
});

test('an out-of-range channel is left out of range here', () => {
  // One shipped SO-101 dataset peaks at 123 on a nominally [-100,100] channel.
  // Clamping in the conversion would hide it; solvePose clamps against the real
  // limit and names the joint, which is where a caller can say so.
  const r = resolveJointValues(SO100, describeById('so_arm100')!, ['shoulder_pan.pos'], [123]);
  const pan = SO100.joints.find((j) => j.name === 'shoulder_pan')!;
  assert.ok(r.values.shoulder_pan > pan.upper, `${r.values.shoulder_pan}`);
});

test('radian channels pass through untouched', () => {
  const r = resolveJointValues(PANDA, describeById('panda')!, ['panda_joint1', 'panda_joint2'], [0.75, -0.5]);
  assert.equal(r.strategy, 'name');
  assert.equal(r.values.panda_joint1, 0.75);
  assert.equal(r.values.panda_joint2, -0.5);
});

test('channels with nowhere to go are reported, not dropped', () => {
  // A 14-channel bimanual ALOHA recording opened against a 6-joint arm. Half a
  // robot drawn in silence is the failure this prevents.
  const aloha = ['left_waist', 'left_shoulder', 'left_elbow', 'left_gripper', 'shoulder_pan', 'gripper'];
  const r = resolveJointValues(SO100, describeById('so_arm100')!, aloha, [0, 0, 0, 0, 0, 0]);
  assert.equal(r.strategy, 'name'); // some names DID match, so this is not a positional case
  assert.equal(r.matched, 2);
  assert.deepEqual(r.unmapped, ['left_waist', 'left_shoulder', 'left_elbow', 'left_gripper']);
});

test('no match at all is its own state', () => {
  const r = resolveJointValues(SO100, { ...describeById('so_arm100')!, jointOrder: ['nope'] }, SO_PLACEHOLDER, [1, 2, 3, 4, 5, 6]);
  assert.equal(r.strategy, 'none');
  assert.equal(r.matched, 0);
  assert.equal(r.unmapped.length, 6);
});

test('a NaN sample becomes a parked joint, not a NaN pose', () => {
  const r = resolveJointValues(SO100, describeById('so_arm100')!, ['shoulder_pan.pos'], [Number.NaN]);
  assert.equal(r.values.shoulder_pan, 0);
});
