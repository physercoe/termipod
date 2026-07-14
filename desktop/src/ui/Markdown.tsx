import { memo, useEffect, useState } from 'react';
import ReactMarkdown, { defaultUrlTransform, type Components } from 'react-markdown';
import { useOpenLink } from './OpenLinkContext';
import { loadNoteImage, NOTE_ATT_SCHEME } from '../state/attachments';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import rehypeHighlight from 'rehype-highlight';

// react-markdown's default urlTransform strips every non-http(s)/mailto URL —
// including the `data:image/…;base64,…` URIs we embed for note screenshots and
// our own `termipod-att://<key>/<file>` note-image refs (de-inlined attachments,
// resolved to a blob by <AttachmentImage> below). Without this, both render as
// `<img src="">` (a broken image). Everything else defers to the safe default.
// `<img>` can only paint — it can't execute script — so this is XSS-safe.
function urlTransform(url: string): string {
  if (/^data:image\//i.test(url) || url.startsWith(NOTE_ATT_SCHEME)) return url;
  return defaultUrlTransform(url);
}

/// An image whose `src` is a `termipod-att://<key>/<file>` note-image reference:
/// resolve the managed-attachment bytes to a Blob and paint via an object URL,
/// revoked on unmount / ref change. A plain data/http `src` renders directly.
function AttachmentImage({ src, alt }: { src?: string; alt?: string }): JSX.Element | null {
  const isRef = typeof src === 'string' && src.startsWith(NOTE_ATT_SCHEME);
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!isRef || src === undefined) return;
    let obj: string | null = null;
    let alive = true;
    void loadNoteImage(src).then((blob) => {
      if (!alive || blob === null) return;
      obj = URL.createObjectURL(blob);
      setUrl(obj);
    });
    return () => {
      alive = false;
      if (obj !== null) URL.revokeObjectURL(obj);
    };
  }, [isRef, src]);
  if (!isRef) return <img src={src} alt={alt ?? ''} />;
  if (url === null) return <span className="md-img-loading" aria-label={alt} />;
  return <img src={url} alt={alt ?? ''} />;
}

// remark-math only understands `$…$` / `$$…$$`. Content pasted from ChatGPT /
// Claude / Poe (and some Obsidian vaults) uses the LaTeX delimiters `\(…\)`
// (inline) and `\[…\]` (display) instead, which remark-math leaves as literal
// backslash-parens. Rewrite them to dollar math — but protect fenced/inline code
// first so a `\(` inside a code sample is never touched. Only run in prose mode
// (singleDollarMath), where `$…$` is already treated as math.
function normalizeMath(src: string): string {
  const stash: string[] = [];
  const hide = (m: string): string => `\u0000${stash.push(m) - 1}\u0000`;
  let s = src
    .replace(/```[\s\S]*?```|~~~[\s\S]*?~~~/g, hide) // fenced code
    .replace(/`[^`\n]+`/g, hide); // inline code
  s = s
    .replace(/\\\[([\s\S]+?)\\\]/g, (_m, inner: string) => `$$${inner}$$`)
    .replace(/\\\(([\s\S]+?)\\\)/g, (_m, inner: string) => `$${inner}$`);
  return s.replace(/\u0000(\d+)\u0000/g, (_m, i: string) => stash[Number(i)]);
}

// Deterministic heading slug for the markdown-document outline (MarkdownReader).
// Kept identical to the outline's own slugify so an outline link's target id
// matches the rendered heading's id.
interface HastLike {
  value?: string;
  children?: HastLike[];
}
function hastText(node: HastLike | undefined): string {
  if (node === undefined) return '';
  if (typeof node.value === 'string') return node.value;
  return (node.children ?? []).map(hastText).join('');
}
export function slugify(s: string): string {
  return s
    .toLowerCase()
    .trim()
    .replace(/[^\w\s-]/g, '')
    .replace(/[\s_]+/g, '-')
    .replace(/^-+|-+$/g, '');
}

/// F1 primitive — safe GitHub-flavoured Markdown for transcript text/thought
/// blocks and tool payloads. react-markdown renders to React elements (no
/// `innerHTML`), so it is XSS-safe by construction. Links are rendered as
/// buttons that open in the OS browser via `openExternal` (the Rust
/// `open_external` command): a raw `<a href>` in the single-webview Tauri build
/// would navigate the SPA away and strand the user, so links must never navigate
/// in-app.
///
/// Fenced code blocks are syntax-highlighted by `rehype-highlight` (lowlight /
/// highlight.js) — it adds `hljs`/`hljs-*` classes that `.md pre code` styles in
/// app.css with the mono font and a themed token palette. Inline/block math
/// (`$…$`, `$$…$$`) is parsed by `remark-math` and rendered by `rehype-katex`
/// (KaTeX CSS + bundled fonts imported once in main.tsx). Both are offline —
/// no CDN — so they work inside the sandboxed webview (director feedback:
/// code blocks should be highlighted with a real mono font, math should render).
///
/// `detect: true` lets highlight.js guess the language for unlabelled fences
/// (agents often emit ```​ without a lang tag); `ignoreMissing` keeps an unknown
/// lang tag from throwing — it just renders unhighlighted.
///
/// `singleDollarTextMath: false` is deliberate: this is a coding-agent
/// transcript where `$VAR`, `$1`, `$PATH` in bare prose are pervasive, and
/// treating single `$…$` as inline math would mangle them. Display math
/// (`$$…$$`) still renders; `throwOnError: false` makes a malformed formula show
/// inline in red rather than blanking the whole block.
export const Markdown = memo(function Markdown({
  text,
  singleDollarMath = false,
  headingIds = false,
}: {
  text: string;
  // Enable `$…$` inline math + `\(…\)`/`\[…\]` LaTeX-delimiter normalization.
  // Default off for agent transcripts (bare `$VAR`, `$1`, `$PATH` are pervasive
  // there and would be mangled); the prose/document contexts (a `.md` attachment,
  // item notes, the read body) turn it ON so a real LaTeX document renders math.
  singleDollarMath?: boolean;
  // Stamp `id` on headings so the MarkdownReader outline can scroll to them.
  headingIds?: boolean;
}): JSX.Element {
  const openLink = useOpenLink();
  const src = singleDollarMath ? normalizeMath(text) : text;
  const heading = (Tag: 'h1' | 'h2' | 'h3' | 'h4' | 'h5' | 'h6'): Components[typeof Tag] =>
    function H({ node, children }): JSX.Element {
      const id = headingIds ? slugify(hastText(node as HastLike)) || undefined : undefined;
      return <Tag id={id}>{children}</Tag>;
    };
  return (
    <div className="md">
      <ReactMarkdown
        urlTransform={urlTransform}
        remarkPlugins={[remarkGfm, [remarkMath, { singleDollarTextMath: singleDollarMath }]]}
        rehypePlugins={[
          [rehypeHighlight, { detect: true, ignoreMissing: true }],
          [rehypeKatex, { throwOnError: false }],
        ]}
        components={{
          h1: heading('h1'),
          h2: heading('h2'),
          h3: heading('h3'),
          h4: heading('h4'),
          h5: heading('h5'),
          h6: heading('h6'),
          img: ({ src, alt }) => <AttachmentImage src={typeof src === 'string' ? src : undefined} alt={alt} />,
          a: ({ children, href }) => {
            const external = typeof href === 'string' && /^(https?:|mailto:)/.test(href);
            return (
              <button
                type="button"
                className="md-link"
                title={href}
                disabled={!external}
                onClick={external ? () => openLink(href) : undefined}
              >
                {children}
              </button>
            );
          },
        }}
      >
        {src}
      </ReactMarkdown>
    </div>
  );
});
