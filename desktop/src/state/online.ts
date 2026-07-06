import { useEffect, useState } from 'react';

/// Network reachability (parity Phase 3b). Drives the offline banner: when the
/// webview reports offline, the surfaces keep rendering the persisted query
/// cache instead of blanking, and the banner says so. This is the OS network
/// signal (navigator.onLine), not hub-liveness — good enough to explain stale
/// data without polling.
export function useOnline(): boolean {
  const [online, setOnline] = useState(() => (typeof navigator !== 'undefined' ? navigator.onLine : true));
  useEffect(() => {
    const up = (): void => setOnline(true);
    const down = (): void => setOnline(false);
    window.addEventListener('online', up);
    window.addEventListener('offline', down);
    return () => {
      window.removeEventListener('online', up);
      window.removeEventListener('offline', down);
    };
  }, []);
  return online;
}
