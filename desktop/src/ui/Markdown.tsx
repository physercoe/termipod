import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import { useOpenLink } from './OpenLinkContext';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import rehypeHighlight from 'rehype-highlight';

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
export const Markdown = memo(function Markdown({ text }: { text: string }): JSX.Element {
  const openLink = useOpenLink();
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, [remarkMath, { singleDollarTextMath: false }]]}
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
