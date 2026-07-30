import type { ReactNode } from 'react';
import type { JobId } from '../state/workbench';
import type { HubProfile } from '../state/profiles';
import { AuthorSurface } from '../surfaces/AuthorSurface';
import { CompareSurface } from '../surfaces/CompareSurface';
import { DebugSurface } from '../surfaces/DebugSurface';
import { Navigator } from '../surfaces/Navigator';
import { ProjectsSurface } from '../surfaces/ProjectsSurface';
import { ReadSurface } from '../surfaces/ReadSurface';
import { RecordSurface } from '../surfaces/RecordSurface';
import { ReplaySurface } from '../surfaces/ReplaySurface';
import { SettingsSurface } from '../surfaces/Settings';
import { MissionLayout } from './MissionLayout';

/// The job → surface mapping, factored out of `AppShell`'s ternary chain so the
/// shell can render it **twice** — once per pane (`plans/desktop-shell-split-pane.md`
/// §3.2). A pure switch: no behaviour of its own, no store reads beyond the
/// surfaces' own.
///
/// The fleet toolbar's buttons open AppShell-owned overlays, so it arrives as a
/// node rather than being built here; `settings` needs the connect callback for
/// the same reason. Neither job is split-eligible, so passing one of each is
/// enough — see `isSplitEligible`.
export function SurfaceView({
  job,
  fleetToolbar,
  onConnect,
}: {
  job: JobId;
  fleetToolbar: ReactNode;
  onConnect: (edit?: HubProfile) => void;
}): JSX.Element | null {
  switch (job) {
    case 'fleet':
      // `storageKey` persists the three-region split; only one fleet pane can
      // exist (singleton rule), so the key stays unqualified.
      return <MissionLayout storageKey="fleet" nav={<Navigator />} toolbar={fleetToolbar} />;
    case 'projects':
      return <ProjectsSurface />;
    case 'read':
      return <ReadSurface />;
    case 'author':
      return <AuthorSurface />;
    case 'debug':
      return <DebugSurface />;
    case 'compare':
      return <CompareSurface />;
    case 'replay':
      return <ReplaySurface />;
    case 'record':
      return <RecordSurface />;
    case 'settings':
      return <SettingsSurface onConnect={onConnect} />;
    case 'terminal':
      // The always-mounted TerminalPanel renders it as AppShell's sibling — its
      // <Screen>s die if unmounted, so it can never live in this stack.
      return null;
  }
}
