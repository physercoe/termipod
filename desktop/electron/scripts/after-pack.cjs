const fs = require('node:fs/promises');
const path = require('node:path');
const { rewriteMachOUuids } = require('./macho-uuid.cjs');

/**
 * Apply the two macOS mutations that must happen before code signing:
 * give the app executable its own LC_UUID for Local Network attribution, and
 * restore node-pty's launcher permissions after npm 11 unpacks it as 0644.
 */
exports.default = async function afterPack(context) {
  if (context.electronPlatformName !== 'darwin') return;

  const appBundle = path.join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`);
  const executable = path.join(appBundle, 'Contents', 'MacOS', context.packager.appInfo.productFilename);
  const originalExecutable = await fs.readFile(executable);
  const seed = `${context.packager.appInfo.id}\0${context.packager.appInfo.version}`;
  const patched = rewriteMachOUuids(originalExecutable, seed);
  await fs.writeFile(executable, patched.buffer);

  const resources = path.join(
    appBundle,
    'Contents',
    'Resources',
    'app.asar.unpacked',
    'node_modules',
    'node-pty',
    'prebuilds',
  );

  let fixed = 0;
  for (const arch of ['darwin-arm64', 'darwin-x64']) {
    const helper = path.join(resources, arch, 'spawn-helper');
    try {
      await fs.chmod(helper, 0o755);
      fixed += 1;
    } catch (error) {
      if (error?.code !== 'ENOENT') throw error;
    }
  }

  if (fixed === 0) {
    throw new Error(`node-pty spawn-helper not found under ${resources}`);
  }
};
