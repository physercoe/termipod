const fs = require('node:fs/promises');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { rewriteMachOUuids } = require('./macho-uuid.cjs');

function adHocSign(executable) {
  execFileSync('/usr/bin/codesign', ['--force', '--sign', '-', executable]);
}

async function rewriteExecutableUuid(executable, seed, signer = adHocSign) {
  const originalExecutable = await fs.readFile(executable);
  const patched = rewriteMachOUuids(originalExecutable, seed);
  await fs.writeFile(executable, patched.buffer);
  // Electron's executable arrives ad-hoc signed. Mutating LC_UUID invalidates
  // that CodeDirectory, and alpha packages deliberately skip Developer ID
  // signing, so restore a valid ad-hoc signature before packaging continues.
  signer(executable);
  return patched;
}

/**
 * Apply the two macOS mutations that must happen before code signing:
 * give the app executable its own LC_UUID for Local Network attribution, and
 * restore node-pty's launcher permissions after npm 11 unpacks it as 0644.
 */
exports.default = async function afterPack(context) {
  if (context.electronPlatformName !== 'darwin') return;

  const appBundle = path.join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`);
  // SSH and SFTP sockets are opened by the Electron main process. Keep this
  // mutation deliberately scoped to that executable; helper UUIDs do not
  // participate in direct host connections.
  const executable = path.join(appBundle, 'Contents', 'MacOS', context.packager.appInfo.productFilename);
  const seed = `${context.packager.appInfo.id}\0${context.packager.appInfo.version}`;
  await rewriteExecutableUuid(executable, seed);

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

exports.rewriteExecutableUuid = rewriteExecutableUuid;
