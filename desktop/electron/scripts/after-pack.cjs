const fs = require('node:fs/promises');
const path = require('node:path');

/**
 * Restore executable permissions on node-pty's macOS launcher before the app is
 * signed. npm 11 may unpack the published helper as 0644; node-pty invokes it
 * with posix_spawnp, which then fails even though the binary exists.
 */
exports.default = async function afterPack(context) {
  if (context.electronPlatformName !== 'darwin') return;

  const resources = path.join(
    context.appOutDir,
    `${context.packager.appInfo.productFilename}.app`,
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
