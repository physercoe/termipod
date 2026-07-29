import type { UrdfModel } from './urdf.ts';
import { movableJoints } from './urdf.ts';

/// The robot-description manifest (J8 Replay W3).
///
/// **A manifest, not a bundle** (plan §6): a registry row names where a robot's
/// URDF lives and under what licence, and the description is fetched through the
/// forge machinery the Inspect tree already uses. Bundling descriptions would
/// bloat the installer and freeze someone else's licensing into ours.
///
/// Every entry here was fetched and parsed before it was written down — the
/// joint names below are the ones in the file, not the ones the robot's docs
/// claim. Adding an entry means doing the same.
///
/// This is also the embodiment registry's first real consumer: a dataset row
/// already carries `env_ref = "lerobot:<robot_type>"` from the W1 digest fold.

export type AngleUnit = 'radian' | 'degree' | 'normalized';

export interface RobotDescription {
  id: string;
  label: string;
  /// `info.json` `robot_type` values that select this entry, already run
  /// through `normalizeRobotType`.
  robotTypes: string[];
  /// `owner/name` on GitHub.
  repo: string;
  /// A commit SHA, never a branch. The forge roots in Inspect pin a SHA for the
  /// same reason: a moving ref means two people looking at "the same" robot see
  /// different arms.
  ref: string;
  urdfPath: string;
  license: string;
  /// The unit the *dataset's* channels are in — a property of the recording
  /// stack, not of the URDF, which is always radians and metres.
  angleUnit: AngleUnit;
  /// Joint names in the order a state vector reports them.
  ///
  /// Not derivable from the URDF: `so101_new_calib.urdf` declares its joints
  /// gripper-first, exactly reversed from the order LeRobot's motor table uses.
  /// So file order is not channel order, and this list is the only thing that
  /// makes a positional fallback safe.
  jointOrder: string[];
  /// Per-joint input span for `normalized` channels. Anything not listed uses
  /// `DEFAULT_NORMALIZED_RANGE`.
  normalizedRanges?: Record<string, readonly [number, number]>;
}

/// LeRobot's `RANGE_M100_100`. Verified twice over: in
/// `lerobot/robots/so_follower/so_follower.py` (the motor table), and against
/// `meta/stats.json` of two real SO-ARM datasets, whose body channels swing
/// symmetrically about zero while the gripper never goes negative.
export const DEFAULT_NORMALIZED_RANGE: readonly [number, number] = [-100, 100];

/// The gripper is `RANGE_0_100` in the same motor table — the one joint whose
/// zero means "closed" rather than "centred". Mapping it as if it were
/// symmetric would draw an open gripper for every closed one.
const SO_ARM_RANGES: Record<string, readonly [number, number]> = { gripper: [0, 100] };

const SO_ARM_JOINTS = ['shoulder_pan', 'shoulder_lift', 'elbow_flex', 'wrist_flex', 'wrist_roll', 'gripper'];

export const ROBOT_DESCRIPTIONS: readonly RobotDescription[] = [
  {
    id: 'so_arm100',
    label: 'SO-ARM100',
    robotTypes: ['so100', 'so_arm100', 'soarm100', 'so_100'],
    repo: 'TheRobotStudio/SO-ARM100',
    ref: 'fda892cba81032c46c40976a48c9ceadbf40a9ca',
    urdfPath: 'Simulation/SO100/so100.urdf',
    license: 'Apache-2.0',
    angleUnit: 'normalized',
    jointOrder: SO_ARM_JOINTS,
    normalizedRanges: SO_ARM_RANGES,
  },
  {
    id: 'so_arm101',
    label: 'SO-ARM101',
    robotTypes: ['so101', 'so_arm101', 'soarm101', 'so_101'],
    repo: 'TheRobotStudio/SO-ARM100',
    ref: 'fda892cba81032c46c40976a48c9ceadbf40a9ca',
    urdfPath: 'Simulation/SO101/so101_new_calib.urdf',
    license: 'Apache-2.0',
    angleUnit: 'normalized',
    jointOrder: SO_ARM_JOINTS,
    normalizedRanges: SO_ARM_RANGES,
  },
  {
    id: 'panda',
    label: 'Franka Panda',
    robotTypes: ['franka', 'panda', 'franka_panda', 'franka_emika_panda'],
    repo: 'Gepetto/example-robot-data',
    ref: '6249cab1cdffa4fadb9a53dda964a50d79c5eaaf',
    urdfPath: 'robots/panda_description/urdf/panda.urdf',
    license: 'BSD-3-Clause',
    // Franka stacks report joint angles in radians, and the file's own limits
    // (±2.9 rad on joint 1) are the units the data already speaks.
    angleUnit: 'radian',
    jointOrder: [
      'panda_joint1',
      'panda_joint2',
      'panda_joint3',
      'panda_joint4',
      'panda_joint5',
      'panda_joint6',
      'panda_joint7',
      'panda_finger_joint1',
    ],
  },
];

/// Fold a `robot_type` to its match key.
///
/// Real datasets write `so100_follower`, `SO-100`, and `so_100` for one arm, and
/// a great many write the literal `unknown` for an SO-ARM they simply did not
/// declare. The suffixes are a teleoperation role, not a different robot: a
/// leader and a follower are the same kinematics.
export function normalizeRobotType(robotType: string): string {
  let s = robotType.trim().toLowerCase().replace(/[\s-]+/g, '_');
  for (const suffix of ['_follower', '_leader']) {
    if (s.endsWith(suffix)) s = s.slice(0, -suffix.length);
  }
  return s;
}

/// The description for a `robot_type`, or null when nothing matches.
///
/// Null is a normal outcome, not a failure: `unknown` is the single most common
/// `robot_type` in the wild — `maximilienroberti/so100_test` is an SO-100
/// recording that declares it — so the panel has to offer a manual pick rather
/// than treat an unmatched dataset as unsupported.
export function matchRobot(robotType: string): RobotDescription | null {
  const key = normalizeRobotType(robotType);
  if (key === '' || key === 'unknown') return null;
  return ROBOT_DESCRIPTIONS.find((d) => d.robotTypes.includes(key)) ?? null;
}

export function describeById(id: string): RobotDescription | null {
  return ROBOT_DESCRIPTIONS.find((d) => d.id === id) ?? null;
}

/// Strip the measurement suffix LeRobot appends to a channel name.
///
/// v3.0 SO-ARM datasets name their channels `shoulder_pan.pos`, which is the
/// same joint as the URDF's `shoulder_pan`. Without this every name match fails
/// and the whole arm silently falls back to positional.
export function channelJointName(channel: string): string {
  return channel.trim().replace(/\.(pos|position|vel|velocity|eff|effort|torque)$/i, '').toLowerCase();
}

/// Whether a channel name identifies anything.
///
/// `motor_0`…`motor_5` is what LeRobot writes when the recording stack had no
/// names to give — it is a placeholder, and matching on it would be matching on
/// nothing. Treating these as unnamed is what lets the positional fallback take
/// over instead of producing zero matches and an unmoving robot.
export function isPlaceholderChannel(channel: string): boolean {
  const n = channelJointName(channel);
  return n === '' || /^(motor|joint|axis|dof|channel|state|action)?_?\d+$/.test(n);
}

export interface JointResolution {
  /// Joint name → value in the URDF's own units (radians / metres).
  values: Record<string, number>;
  /// How the channels were matched to joints.
  strategy: 'name' | 'position' | 'none';
  /// Channels that drove no joint. Surfaced, not swallowed — a 14-channel
  /// bimanual dataset against a 6-joint arm should say so.
  unmapped: string[];
  matched: number;
}

/// Turn one frame of a dataset's channels into URDF joint values.
///
/// Name matching first, positional second. Positional is only reached when
/// names genuinely carry no information, because a *wrong* name match is worse
/// than no match: it moves the wrong joint and the arm looks alive while being
/// wrong. A partial name match is kept as a partial match rather than being
/// upgraded to positional, since the names that did match are evidence about
/// the ones that did not.
export function resolveJointValues(
  model: UrdfModel,
  desc: RobotDescription,
  channels: string[],
  values: number[],
): JointResolution {
  const movable = movableJoints(model);
  const byName = new Map(movable.map((j) => [j.name.toLowerCase(), j]));

  const out: Record<string, number> = {};
  const unmapped: string[] = [];
  let matched = 0;

  const named = channels.filter((c) => !isPlaceholderChannel(c));
  const useNames = named.length > 0 && named.some((c) => byName.has(channelJointName(c)));

  if (useNames) {
    for (let i = 0; i < channels.length; i += 1) {
      const joint = byName.get(channelJointName(channels[i]));
      if (joint === undefined) {
        unmapped.push(channels[i]);
        continue;
      }
      out[joint.name] = convert(values[i], joint.lower, joint.upper, joint.type, desc, joint.name);
      matched += 1;
    }
    return { values: out, strategy: 'name', unmapped, matched };
  }

  // Positional: the manifest's declared order, not the file's declaration
  // order, which is reversed in at least one shipped SO-ARM description.
  const order = desc.jointOrder.length > 0 ? desc.jointOrder : movable.map((j) => j.name);
  for (let i = 0; i < channels.length; i += 1) {
    const name = order[i];
    const joint = name === undefined ? undefined : byName.get(name.toLowerCase());
    if (joint === undefined) {
      unmapped.push(channels[i]);
      continue;
    }
    out[joint.name] = convert(values[i], joint.lower, joint.upper, joint.type, desc, joint.name);
    matched += 1;
  }
  return { values: out, strategy: matched > 0 ? 'position' : 'none', unmapped, matched };
}

/// A joint with no declared limits still needs a span to map a normalized value
/// onto. A full turn is the only defensible assumption for a continuous joint,
/// and it is an assumption — which is why the panel labels a normalized pose as
/// approximate rather than presenting it as measured truth.
const ASSUMED_SPAN: readonly [number, number] = [-Math.PI, Math.PI];

function convert(
  value: number,
  lower: number,
  upper: number,
  type: string,
  desc: RobotDescription,
  jointName: string,
): number {
  if (!Number.isFinite(value)) return 0;
  if (desc.angleUnit === 'radian') return value;
  if (desc.angleUnit === 'degree') return type === 'prismatic' ? value : (value * Math.PI) / 180;

  const [inLo, inHi] = desc.normalizedRanges?.[jointName] ?? DEFAULT_NORMALIZED_RANGE;
  if (inHi === inLo) return 0;
  const [outLo, outHi] = upper > lower ? [lower, upper] : ASSUMED_SPAN;
  // Deliberately not clamped here: `solvePose` clamps against the joint's real
  // limit and reports which joints it had to, so an out-of-range channel stays
  // visible instead of being quietly flattened twice.
  return outLo + ((value - inLo) / (inHi - inLo)) * (outHi - outLo);
}
