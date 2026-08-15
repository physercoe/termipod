#!/usr/bin/env node
import { _electron as electron } from 'playwright';
import { copyFile, mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';

const args = process.argv.slice(2);
const sizeFlag = args[0] === '--size';
const size = sizeFlag ? Number.parseInt(args[1] ?? '', 10) : 512;
const [sourceArg, ...outputArgs] = args.slice(sizeFlag ? 2 : 0);
if (!Number.isInteger(size) || size < 16 || size > 2048) {
  throw new Error('icon size must be an integer between 16 and 2048');
}
if (sourceArg === undefined || outputArgs.length === 0) {
  throw new Error('usage: node render-light-icon.mjs [--size px] <source.svg> <output.png> [...]');
}

const source = path.resolve(sourceArg);
const outputs = outputArgs.map((output) => path.resolve(output));
const app = await electron.launch({
  args: [
    '--no-sandbox',
    '--disable-gpu',
    path.resolve('out/main.cjs'),
  ],
  env: {
    ...process.env,
    TERMIPOD_DIST: path.resolve('../dist'),
    TERMIPOD_E2E: '1',
  },
});

try {
  const page = await app.firstWindow();
  const svg = await readFile(source, 'utf8');
  await page.setViewportSize({ width: size, height: size });
  await page.setContent(`<style>html,body{margin:0;width:${size}px;height:${size}px;overflow:hidden}svg{display:block;width:${size}px;height:${size}px}</style>${svg}`);

  const [first, ...rest] = outputs;
  await mkdir(path.dirname(first), { recursive: true });
  await page.locator('svg').screenshot({ path: first, omitBackground: true });
  for (const output of rest) {
    await mkdir(path.dirname(output), { recursive: true });
    await copyFile(first, output);
  }
} finally {
  await app.close();
}
