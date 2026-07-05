import { useSession } from './state/session';
import { useApplyTheme } from './state/theme';
import { AppShell } from './ui/AppShell';
import { ConnectPanel } from './ui/ConnectPanel';

export function App(): JSX.Element {
  useApplyTheme();
  const client = useSession((s) => s.client);
  return client === null ? <ConnectPanel /> : <AppShell />;
}
