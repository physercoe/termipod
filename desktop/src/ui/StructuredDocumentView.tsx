import { useMemo } from 'react';
import { useT } from '../i18n';
import { Markdown } from './Markdown';
import {
  parseStructuredDocument,
  withoutDuplicateTitle,
  type StructuredSectionStatus,
} from './structuredDocumentModel';

function stateLabel(status: StructuredSectionStatus, t: (key: string) => string): string {
  return t(`docs.sectionState.${status}`);
}

/// Render either a typed, section-aware document or ordinary Markdown. Typed
/// documents are stored as a JSON envelope in `content_inline`; feeding that
/// envelope directly to Markdown is what previously exposed raw JSON to users.
export function DocumentContent({ text, schemaId }: { text: string; schemaId?: string }): JSX.Element {
  const t = useT();
  const structured = useMemo(() => parseStructuredDocument(text), [text]);
  const isTyped = schemaId !== undefined && schemaId !== '';

  if (structured === null) {
    if (!isTyped) return <Markdown text={text} />;
    return (
      <div className="typed-doc-invalid">
        <div className="error">{t('docs.structuredInvalid')}</div>
        <pre className="mono">{text}</pre>
      </div>
    );
  }

  const resolvedSchemaId = structured.schemaId || schemaId || t('docs.structured');
  const ratified = structured.sections.filter((section) => section.status === 'ratified').length;

  return (
    <article className="typed-doc">
      <header className="typed-doc-summary">
        <span className="typed-doc-schema mono">{resolvedSchemaId}</span>
        <span className="spacer" />
        <span className="muted small">
          {t('docs.sectionProgress')
            .replace('{ratified}', String(ratified))
            .replace('{total}', String(structured.sections.length))}
        </span>
      </header>
      {structured.sections.length === 0 ? (
        <div className="typed-doc-empty muted">{t('docs.noSections')}</div>
      ) : (
        <div className="typed-doc-sections">
          {structured.sections.map((section, index) => {
            const title = section.title || t('docs.sectionNumber').replace('{number}', String(index + 1));
            const body = withoutDuplicateTitle(section.body, title);
            return (
              <section className="typed-doc-section" key={`${section.slug}:${index}`}>
                <div className="typed-doc-section-head">
                  <h2>{title}</h2>
                  <span className={`typed-doc-state ${section.status}`}>
                    <span className="typed-doc-state-dot" aria-hidden="true" />
                    {stateLabel(section.status, t)}
                  </span>
                </div>
                {body.trim() === '' ? (
                  <div className="typed-doc-section-empty muted">{t('docs.sectionEmpty')}</div>
                ) : (
                  <Markdown text={body} />
                )}
              </section>
            );
          })}
        </div>
      )}
    </article>
  );
}
