const crypto = require('node:crypto');

const LC_UUID = 0x1b;

function endianReaders(buffer, littleEndian) {
  return {
    u32(offset) {
      return littleEndian ? buffer.readUInt32LE(offset) : buffer.readUInt32BE(offset);
    },
    u64(offset) {
      const value = littleEndian ? buffer.readBigUInt64LE(offset) : buffer.readBigUInt64BE(offset);
      if (value > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('Mach-O offset exceeds JavaScript safe integer range');
      return Number(value);
    },
  };
}

function thinLayout(buffer, offset) {
  if (offset + 4 > buffer.length) throw new Error('truncated Mach-O header');
  const magic = buffer.readUInt32BE(offset);
  if (magic === 0xfeedface) return { littleEndian: false, headerSize: 28 };
  if (magic === 0xcefaedfe) return { littleEndian: true, headerSize: 28 };
  if (magic === 0xfeedfacf) return { littleEndian: false, headerSize: 32 };
  if (magic === 0xcffaedfe) return { littleEndian: true, headerSize: 32 };
  throw new Error(`unsupported Mach-O magic 0x${magic.toString(16)}`);
}

function thinSlices(buffer) {
  if (buffer.length < 8) throw new Error('truncated Mach-O file');
  const magic = buffer.readUInt32BE(0);
  const fat = new Map([
    [0xcafebabe, { littleEndian: false, arch64: false }],
    [0xbebafeca, { littleEndian: true, arch64: false }],
    [0xcafebabf, { littleEndian: false, arch64: true }],
    [0xbfbafeca, { littleEndian: true, arch64: true }],
  ]).get(magic);
  if (fat === undefined) return [{ offset: 0, size: buffer.length }];

  const read = endianReaders(buffer, fat.littleEndian);
  const count = read.u32(4);
  const entrySize = fat.arch64 ? 32 : 20;
  const tableEnd = 8 + count * entrySize;
  if (tableEnd > buffer.length) throw new Error('truncated fat Mach-O architecture table');

  const slices = [];
  for (let index = 0; index < count; index += 1) {
    const entry = 8 + index * entrySize;
    const offset = fat.arch64 ? read.u64(entry + 8) : read.u32(entry + 8);
    const size = fat.arch64 ? read.u64(entry + 16) : read.u32(entry + 12);
    if (offset + size > buffer.length) throw new Error('fat Mach-O slice extends beyond the file');
    slices.push({ offset, size });
  }
  return slices;
}

function customUuid(seed, original) {
  const uuid = crypto.createHash('sha256').update(seed).update('\0').update(original).digest().subarray(0, 16);
  // RFC 9562 UUIDv8: application-defined payload with standard variant bits.
  uuid[6] = (uuid[6] & 0x0f) | 0x80;
  uuid[8] = (uuid[8] & 0x3f) | 0x80;
  return uuid;
}

/**
 * Replace every LC_UUID in a thin or universal Mach-O buffer.
 *
 * Electron ships the same prebuilt main executable (and therefore the same
 * UUID) to every app. macOS Local Network privacy uses that UUID as a fallback
 * identity and can consequently attribute TermiPod to com.github.Electron,
 * rejecting the first LAN socket before it notices TermiPod's signed bundle.
 * The replacement is deterministic per app/version/slice and runs before
 * electron-builder signs or notarizes the bundle.
 */
function rewriteMachOUuids(buffer, seed) {
  const out = Buffer.from(buffer);
  const rewritten = [];
  for (const [sliceIndex, slice] of thinSlices(out).entries()) {
    const layout = thinLayout(out, slice.offset);
    const read = endianReaders(out, layout.littleEndian);
    const commandCount = read.u32(slice.offset + 16);
    const commandBytes = read.u32(slice.offset + 20);
    const commandsStart = slice.offset + layout.headerSize;
    const commandsEnd = commandsStart + commandBytes;
    if (commandsEnd > slice.offset + slice.size || commandsEnd > out.length) {
      throw new Error('Mach-O load commands extend beyond their slice');
    }

    let command = commandsStart;
    let found = false;
    for (let index = 0; index < commandCount; index += 1) {
      if (command + 8 > commandsEnd) throw new Error('truncated Mach-O load command');
      const kind = read.u32(command);
      const size = read.u32(command + 4);
      if (size < 8 || command + size > commandsEnd) throw new Error('invalid Mach-O load command size');
      if (kind === LC_UUID) {
        if (size < 24) throw new Error('invalid LC_UUID load command size');
        const uuidOffset = command + 8;
        const original = Buffer.from(out.subarray(uuidOffset, uuidOffset + 16));
        const replacement = customUuid(`${seed}\0${sliceIndex}`, original);
        replacement.copy(out, uuidOffset);
        rewritten.push({ sliceIndex, original, replacement });
        found = true;
      }
      command += size;
    }
    if (!found) throw new Error(`Mach-O slice ${sliceIndex} has no LC_UUID load command`);
  }
  return { buffer: out, rewritten };
}

module.exports = { rewriteMachOUuids };
