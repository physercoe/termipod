/// `desktop_open`'s wiring (coworking lane H).
///
/// Deliberately thin, and split from `agentNavigate.ts` for the reason
/// `authorBridgeHost.ts` is split from `authorBridge.ts`: the rule — execute,
/// record the undo, report the depth — belongs where `node --test` can drive it
/// against the real stores, and this file imports the shell bridge, which it
/// cannot.
import { listen, invoke } from '../bridge';
import { isShell } from '../platform';
import { asNavigateOrder, NAVIGATE_EVENT, NAVIGATE_RESULT_COMMAND, runNavigateOrder } from './agentNavigate';

/// Subscribe to main's navigate channel (called once from main.tsx). No-op
/// outside the Electron shell — a browser build has no agent driving it.
export function initAgentNavigate(): void {
  if (!isShell()) return;
  void listen<unknown>(NAVIGATE_EVENT, (event) => {
    const order = asNavigateOrder(event.payload);
    // A payload with no usable id has nowhere to reply TO; main's own deadline
    // is what ends that call.
    if (order === null) return;
    let depth: 'entity' | 'surface' | 'unknown';
    try {
      depth = runNavigateOrder(order);
    } catch {
      // A store that throws mid-navigation leaves the screen in an unknown
      // state; the honest answer to the agent is "this did not resolve", not a
      // hang until main's deadline.
      depth = 'unknown';
    }
    void invoke(NAVIGATE_RESULT_COMMAND, { id: order.id, depth });
  });
}
