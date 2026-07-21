// Generate the Tauri→Electron handoff manifest (ADR-055 M3.4).
//
// At cutover the final Tauri release surfaces the successor Electron installer
// as a "new version — Download" prompt (the Tauri updater can't install a
// foreign installer format in place). The frontend (`src/state/handoff.ts`)
// fetches `releases/latest/download/handoff.json`; this emits that file, its
// shape matching `HandoffManifest` and its per-OS URLs matching the deterministic
// electron-builder `artifactName`s on the `v<version>` release.
//
// Usage: node gen-handoff.mjs <version> [outPath]
//   HANDOFF_NOTES env overrides the default note text.
import { writeFileSync } from 'node:fs';

const version = process.argv[2];
if (version === undefined || version === '') {
  console.error('usage: node gen-handoff.mjs <version> [outPath]');
  process.exit(1);
}
const out = process.argv[3] ?? 'handoff.json';

// electron-builder's GitHub publisher tags the release `v<version>`; the
// artifactName templates (electron-builder.yml) fix these space-free names.
const base = `https://github.com/physercoe/termipod/releases/download/v${version}`;
const manifest = {
  version,
  notes:
    process.env.HANDOFF_NOTES ??
    'TermiPod has moved to a new installer. Download the latest build to keep receiving updates.',
  platforms: {
    windows: `${base}/TermiPod-Setup-${version}.exe`,
    macos: `${base}/TermiPod-${version}-mac.dmg`,
    linux: `${base}/TermiPod-${version}.AppImage`,
  },
};

writeFileSync(out, `${JSON.stringify(manifest, null, 2)}\n`);
console.log(`wrote ${out}:\n${JSON.stringify(manifest, null, 2)}`);
