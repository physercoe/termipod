#!/usr/bin/env node
import { _electron as electron } from 'playwright';
import { copyFile, mkdir, readFile } from 'node:fs/promises';
import path from 'node:path';

const [sourceArg, ...outputArgs] = process.argv.slice(2);
if (sourceArg === undefined || outputArgs.length === 0) {
  throw new Error('usage: node render-light-icon.mjs <source.svg> <output.png> [...]');
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
  await page.setViewportSize({ width: 512, height: 512 });
  await page.setContent(`<style>html,body{margin:0;width:512px;height:512px;overflow:hidden}svg{display:block;width:512px;height:512px}</style>${svg}`);

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
