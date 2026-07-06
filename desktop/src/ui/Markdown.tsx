import { memo } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';

/// F1 primitive — safe GitHub-flavoured Markdown for transcript text/thought
/// blocks and tool payloads. react-markdown renders to React elements (no
/// `innerHTML`), so it is XSS-safe by construction. Links are rendered as
/// non-navigating styled text: a raw `<a href>` in the Tauri webview would
/// hijack the SPA, and we have no external-open plugin wired yet. Code blocks
/// are styled via `.md` CSS; syntax highlighting is deferred (bundle weight).
export const Markdown = memo(function Markdown({ text }: { text: string }): JSX.Element {
  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ children, href }) => (
            <span className="md-link" title={href}>
              {children}
            </span>
          ),
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
});
