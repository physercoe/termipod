import { useT } from '../i18n';
import { SurfacePlaceholder, WorkbenchSurface } from '../ui/WorkbenchSurface';

/// J4 — Think on a graph / canvas. Incubation-by-connection: excerpt canvases,
/// idea/dependency graphs, the citation/related-notes graph. Its primary
/// component is an EMBED of **tldraw** (Apache-2 React SDK), per
/// `research-tooling-landscape.md` — a heavier dependency deliberately deferred
/// to its own round so this shell change stays low-risk. Until then the tab is
/// present and honest about what it will hold, rather than faking a canvas.
export function CanvasSurface(): JSX.Element {
  const t = useT();
  return (
    <WorkbenchSurface job="canvas">
      <SurfacePlaceholder
        posture={t('canvas.posture')}
        lines={[t('canvas.todo1'), t('canvas.todo2'), t('canvas.todo3')]}
      />
    </WorkbenchSurface>
  );
}
