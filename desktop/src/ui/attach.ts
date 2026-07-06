import type { InputAttachments, WireAttachment } from '../hub/client';

/// Composer attachment logic (parity Phase 1c). Mirrors the hub caps and MIME
/// allowlists in hub/internal/server/handlers_agent_input.go so we clamp with
/// clear errors client-side rather than round-tripping a 400. Text/code files
/// are inlined into the body as a fenced code block (mobile
/// composer_text_attach.dart), not sent as binary attachments.

export type AttachKind = 'image' | 'pdf' | 'audio' | 'video' | 'text';

interface Cap {
  mimes: string[];
  max: number;
  size: number;
}

// Byte caps and MIME allowlists — handlers_agent_input.go:27-74.
export const CAPS: Record<'image' | 'pdf' | 'audio' | 'video', Cap> = {
  image: { mimes: ['image/png', 'image/jpeg', 'image/webp', 'image/gif'], max: 3, size: 5 * 1024 * 1024 },
  pdf: { mimes: ['application/pdf'], max: 1, size: 32 * 1024 * 1024 },
  audio: {
    mimes: ['audio/mpeg', 'audio/mp4', 'audio/wav', 'audio/webm', 'audio/ogg', 'audio/aac', 'audio/flac'],
    max: 1,
    size: 20 * 1024 * 1024,
  },
  video: { mimes: ['video/mp4', 'video/webm', 'video/quicktime'], max: 1, size: 20 * 1024 * 1024 },
};

const CODE_EXT = new Set([
  'ts', 'tsx', 'js', 'jsx', 'py', 'go', 'rs', 'dart', 'java', 'c', 'cpp', 'h', 'hpp', 'cs', 'rb', 'php', 'swift',
  'kt', 'sh', 'bash', 'zsh', 'sql', 'md', 'markdown', 'txt', 'json', 'yaml', 'yml', 'toml', 'ini', 'cfg', 'conf',
  'xml', 'html', 'css', 'scss', 'csv', 'log', 'env',
]);

const EXT_LANG: Record<string, string> = {
  ts: 'ts', tsx: 'tsx', js: 'js', jsx: 'jsx', py: 'python', go: 'go', rs: 'rust', dart: 'dart', rb: 'ruby',
  sh: 'bash', bash: 'bash', zsh: 'bash', md: 'markdown', markdown: 'markdown', yml: 'yaml', yaml: 'yaml',
};

function ext(name: string): string {
  const i = name.lastIndexOf('.');
  return i >= 0 ? name.slice(i + 1).toLowerCase() : '';
}

export function classify(file: File): AttachKind | null {
  const mime = file.type;
  if (CAPS.image.mimes.includes(mime)) return 'image';
  if (CAPS.pdf.mimes.includes(mime)) return 'pdf';
  if (CAPS.audio.mimes.includes(mime)) return 'audio';
  if (CAPS.video.mimes.includes(mime)) return 'video';
  const e = ext(file.name);
  if (mime.startsWith('text/') || mime === 'application/json' || mime === 'application/xml' || CODE_EXT.has(e)) {
    return 'text';
  }
  return null;
}

function readDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error ?? new Error('read failed'));
    r.readAsDataURL(file);
  });
}

function readText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error ?? new Error('read failed'));
    r.readAsText(file);
  });
}

/** RAW base64 (strip the `data:<mime>;base64,` prefix the hub does not want). */
export async function toBase64(file: File): Promise<string> {
  const url = await readDataUrl(file);
  const comma = url.indexOf(',');
  return comma >= 0 ? url.slice(comma + 1) : url;
}

/** A staged attachment awaiting send. `text` items carry inlined content. */
export interface Pending {
  id: string;
  kind: AttachKind;
  name: string;
  mime: string;
  size: number;
  data?: string; // base64 for binary kinds
  text?: string; // inlined content for text kind
}

let seq = 0;
export function nextId(): string {
  seq += 1;
  return `att${seq}`;
}

/** Validate a candidate file against the caps given what's already staged.
 * Returns an error string, or null if it may be added. */
export function checkAddable(file: File, kind: AttachKind, staged: Pending[]): string | null {
  if (kind === 'text') return file.size > 512 * 1024 ? `${file.name}: text file too large (max 512 KiB)` : null;
  const cap = CAPS[kind];
  if (file.size > cap.size) return `${file.name}: too large (max ${Math.round(cap.size / 1024 / 1024)} MiB)`;
  const count = staged.filter((p) => p.kind === kind).length;
  if (count >= cap.max) return `at most ${cap.max} ${kind}${cap.max > 1 ? 's' : ''} per message`;
  return null;
}

/** Read a classified file into a staged attachment. */
export async function stage(file: File, kind: AttachKind): Promise<Pending> {
  const base = { id: nextId(), kind, name: file.name, mime: file.type, size: file.size };
  if (kind === 'text') return { ...base, text: await readText(file) };
  return { ...base, data: await toBase64(file) };
}

function fence(name: string, content: string): string {
  const lang = EXT_LANG[ext(name)] ?? '';
  return `\n\n\`\`\`${lang} ${name}\n${content}\n\`\`\``;
}

/** Compose the final `{body, attachments}` from the draft text + staged items.
 * Text items are appended to the body as fenced blocks; binary items become
 * the typed attachment arrays. */
export function compose(draft: string, staged: Pending[]): { body: string; att: InputAttachments } {
  let body = draft;
  const images: WireAttachment[] = [];
  const pdfs: WireAttachment[] = [];
  const audios: WireAttachment[] = [];
  const videos: WireAttachment[] = [];
  for (const p of staged) {
    if (p.kind === 'text') {
      body += fence(p.name, p.text ?? '');
    } else if (p.data !== undefined) {
      const w: WireAttachment = { mime_type: p.mime, data: p.data, filename: p.name };
      if (p.kind === 'image') images.push({ mime_type: p.mime, data: p.data });
      else if (p.kind === 'pdf') pdfs.push(w);
      else if (p.kind === 'audio') audios.push(w);
      else if (p.kind === 'video') videos.push(w);
    }
  }
  const att: InputAttachments = {};
  if (images.length > 0) att.images = images;
  if (pdfs.length > 0) att.pdfs = pdfs;
  if (audios.length > 0) att.audios = audios;
  if (videos.length > 0) att.videos = videos;
  return { body, att };
}
