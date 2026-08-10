import type { IconName } from './Icon';

export type FileTone =
  | 'neutral'
  | 'blue'
  | 'cyan'
  | 'green'
  | 'yellow'
  | 'orange'
  | 'red'
  | 'violet'
  | 'pink';

export interface InspectFileVisual {
  icon: IconName;
  tone: FileTone;
}

const GROUPS: ReadonlyArray<{ exts: ReadonlySet<string>; visual: InspectFileVisual }> = [
  { exts: new Set(['ts', 'tsx', 'mts', 'cts']), visual: { icon: 'code', tone: 'blue' } },
  { exts: new Set(['js', 'jsx', 'mjs', 'cjs']), visual: { icon: 'code', tone: 'yellow' } },
  { exts: new Set(['py', 'pyi', 'pyw']), visual: { icon: 'code', tone: 'blue' } },
  { exts: new Set(['go', 'dart']), visual: { icon: 'code', tone: 'cyan' } },
  { exts: new Set(['rs', 'swift', 'java', 'kt', 'kts', 'scala']), visual: { icon: 'code', tone: 'orange' } },
  { exts: new Set(['rb']), visual: { icon: 'code', tone: 'red' } },
  { exts: new Set(['php', 'lua', 'ex', 'exs']), visual: { icon: 'code', tone: 'violet' } },
  { exts: new Set(['c', 'h', 'cc', 'cpp', 'cxx', 'hpp', 'cs']), visual: { icon: 'code', tone: 'blue' } },
  { exts: new Set(['html', 'htm', 'xml', 'svg', 'vue', 'svelte']), visual: { icon: 'code', tone: 'orange' } },
  { exts: new Set(['css', 'scss', 'sass', 'less', 'styl']), visual: { icon: 'code', tone: 'violet' } },
  { exts: new Set(['sh', 'bash', 'zsh', 'fish', 'ps1', 'bat', 'cmd']), visual: { icon: 'terminal', tone: 'green' } },
  { exts: new Set(['json', 'jsonc', 'yaml', 'yml', 'toml', 'ini', 'conf', 'config', 'env']), visual: { icon: 'sliders', tone: 'yellow' } },
  { exts: new Set(['sql', 'csv', 'tsv', 'parquet', 'arrow']), visual: { icon: 'table', tone: 'cyan' } },
  { exts: new Set(['md', 'mdx', 'rst', 'txt', 'adoc']), visual: { icon: 'note', tone: 'blue' } },
  { exts: new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'avif', 'ico', 'bmp']), visual: { icon: 'image', tone: 'violet' } },
  { exts: new Set(['mp4', 'mov', 'mkv', 'webm', 'avi']), visual: { icon: 'film', tone: 'pink' } },
  { exts: new Set(['mp3', 'wav', 'flac', 'm4a', 'aac', 'ogg']), visual: { icon: 'music', tone: 'pink' } },
  { exts: new Set(['pdf']), visual: { icon: 'file-text', tone: 'red' } },
  { exts: new Set(['diff', 'patch']), visual: { icon: 'git-compare', tone: 'green' } },
  { exts: new Set(['dot', 'gv', 'drawio', 'canvas', 'excalidraw']), visual: { icon: 'diagram', tone: 'violet' } },
  { exts: new Set(['safetensors', 'onnx', 'gguf', 'pt', 'pth', 'ckpt']), visual: { icon: 'sitemap', tone: 'pink' } },
  { exts: new Set(['zip', 'tar', 'gz', 'tgz', 'bz2', 'xz', '7z', 'rar']), visual: { icon: 'download', tone: 'yellow' } },
];

/**
 * Resolve a compact, editor-style visual identity from a filename. The mapping
 * stays deliberately categorical: related languages share a shape and hue, so
 * the tree is scannable without becoming an icon-brand catalogue.
 */
export function inspectFileVisual(filename: string): InspectFileVisual {
  const name = filename.replace(/\\/g, '/').split('/').pop()?.toLowerCase() ?? '';

  if (name.startsWith('.git')) return { icon: 'git-branch', tone: 'orange' };
  if (/^(readme|changelog|contributing|license|authors)(\.|$)/.test(name)) return { icon: 'book', tone: 'blue' };
  if (/^(dockerfile|containerfile|makefile|justfile)(\.|$)/.test(name)) return { icon: 'terminal', tone: 'cyan' };
  if (/^(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|cargo\.lock|go\.sum)$/.test(name)) return { icon: 'lock', tone: 'yellow' };

  const dot = name.lastIndexOf('.');
  const ext = dot >= 0 ? name.slice(dot + 1) : '';
  for (const group of GROUPS) if (group.exts.has(ext)) return group.visual;
  return { icon: 'file-text', tone: 'neutral' };
}
