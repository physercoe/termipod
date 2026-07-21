// Minimal ambient types for `wavedrom` (3.x ships no type definitions). Only the
// surface the figure registry uses: render a WaveJSON spec to an ONML tree, then
// stringify that tree to an SVG string. `renderAny`'s `index` seeds the SVG's
// element ids, so a distinct index per render avoids id collisions when several
// WaveDrom figures render on one page.
declare module 'wavedrom' {
  export function renderAny(index: number, source: unknown, waveSkin: unknown): unknown;
  export const onml: { stringify(tree: unknown): string };
  export const waveSkin: unknown;
  const _default: {
    renderAny: typeof renderAny;
    onml: typeof onml;
    waveSkin: typeof waveSkin;
  };
  export default _default;
}
