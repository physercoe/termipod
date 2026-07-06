import { useApplyTheme } from './state/theme';
import { AppShell } from './ui/AppShell';

/// The shell renders always — even without a hub connection — so the terminal,
/// settings, and the offline chrome work standalone; the connect form is an
/// overlay the shell raises when disconnected (issue: "show main pages without
/// login; work offline though not fully functional").
export function App(): JSX.Element {
  useApplyTheme();
  return <AppShell />;
}
