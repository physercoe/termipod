#!/usr/bin/env node
// Generates the minimal solid-color PNG icons Tauri needs to compile/bundle.
// Brand teal (#00C0D1). Run: node generate-icons.mjs  (writes icons/*.png).
// Zero dependencies — uses node:zlib for the IDAT deflate.

import { deflateSync } from 'node:zlib';
import { mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, 'icons');
mkdirSync(outDir, { recursive: true });

const RGBA = [0x00, 0xc0, 0xd1, 0xff];

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(buf) {
  let c = 0xffffffff;
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type, data) {
  const typeBuf = Buffer.from(type, 'ascii');
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([typeBuf, data])), 0);
  return Buffer.concat([len, typeBuf, data, crc]);
}

function png(size) {
  const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // color type RGBA
  // 10,11,12 = compression/filter/interlace = 0
  const row = Buffer.alloc(1 + size * 4);
  for (let x = 0; x < size; x++) {
    row[1 + x * 4] = RGBA[0];
    row[1 + x * 4 + 1] = RGBA[1];
    row[1 + x * 4 + 2] = RGBA[2];
    row[1 + x * 4 + 3] = RGBA[3];
  }
  const raw = Buffer.concat(Array.from({ length: size }, () => row));
  return Buffer.concat([sig, chunk('IHDR', ihdr), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))]);
}

const buffers = new Map();
for (const [name, size] of [
  ['32x32.png', 32],
  ['128x128.png', 128],
  ['128x128@2x.png', 256],
  ['icon.png', 512],
]) {
  const buf = png(size);
  buffers.set(size, buf);
  writeFileSync(join(outDir, name), buf);
  console.log(`wrote icons/${name} (${size}x${size})`);
}

// Windows needs an .ico (tauri-build generates a Windows resource from it).
// Modern ICO can embed PNG-compressed entries (Vista+); pack 32px + 256px.
function ico(entries) {
  const count = entries.length;
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0); // reserved
  header.writeUInt16LE(1, 2); // type = icon
  header.writeUInt16LE(count, 4);
  const dir = Buffer.alloc(16 * count);
  let offset = 6 + 16 * count;
  entries.forEach(({ size, buf }, i) => {
    const e = dir.subarray(i * 16, i * 16 + 16);
    e.writeUInt8(size >= 256 ? 0 : size, 0); // width (0 => 256)
    e.writeUInt8(size >= 256 ? 0 : size, 1); // height
    e.writeUInt8(0, 2); // palette
    e.writeUInt8(0, 3); // reserved
    e.writeUInt16LE(1, 4); // color planes
    e.writeUInt16LE(32, 6); // bits per pixel
    e.writeUInt32LE(buf.length, 8);
    e.writeUInt32LE(offset, 12);
    offset += buf.length;
  });
  return Buffer.concat([header, dir, ...entries.map((e) => e.buf)]);
}

writeFileSync(
  join(outDir, 'icon.ico'),
  ico([
    { size: 32, buf: buffers.get(32) },
    { size: 256, buf: buffers.get(256) },
  ]),
);
console.log('wrote icons/icon.ico (32 + 256)');
