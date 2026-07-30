/// DOM shims for the renderer modules the node --test suites import cross-tree
/// (e.g. ../../src/ui/attach.ts references FileReader in its staging half).
/// Typecheck-only: the electron bundle never includes those modules (esbuild's
/// entries are main.ts + preload.ts), and the tests exercise the pure halves
/// (compose, rect math) that never construct these globals. Keep this list
/// minimal — it exists so a cross-tree import doesn't force DOM lib on the
/// whole main-process typecheck.
declare class FileReader {
  onload: (() => void) | null;
  onerror: (() => void) | null;
  readonly result: unknown;
  readonly error: unknown;
  readAsDataURL(blob: unknown): void;
  readAsText(blob: unknown): void;
}
