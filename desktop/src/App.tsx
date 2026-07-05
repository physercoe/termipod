import { useSession } from './state/session';
import { AppShell } from './ui/AppShell';
import { ConnectPanel } from './ui/ConnectPanel';

export function App(): JSX.Element {
  const client = useSession((s) => s.client);
  return client === null ? <ConnectPanel /> : <AppShell />;
}
