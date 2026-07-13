import { memo } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import { useOpenLink } from './OpenLinkContext';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import rehypeHighlight from 'rehype-highlight';

// react-markdown's default urlTransform strips every non-http(s)/mailto URL —
// including the `data:image/…;base64,…` URIs we embed for note screenshots (an
// area shot added via "Add to note"), so `![figure](data:image/png;base64,…)`
// rendered as `<img src="">` (a broken image). Allow inline image data-URIs
// through; defer everything else to the safe default. `<img src="data:…">` can
// only paint a raster/vector — it can't execute script — so this is XSS-safe.
function urlTransform(url: string): string {
  return /^data:image\//i.test(url) ? url : defaultUrlTransform(url);
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
}: {
  text: string;
  // Enable `$…$` inline math. Default off for agent transcripts (bare `$VAR`,
  // `$1`, `$PATH` are pervasive there and would be mangled); the prose/document
  // contexts (a `.md` attachment, item notes, the read body) turn it ON so a
  // real LaTeX document renders its inline math.
  singleDollarMath?: boolean;
}): JSX.Element {
  const openLink = useOpenLink();
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
        {text}
      </ReactMarkdown>
    </div>
  );
});
