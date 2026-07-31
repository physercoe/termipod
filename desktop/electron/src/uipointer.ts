/// Element-resolved pointing — the CDP half (D4 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4 step 5).
///
/// D2 gave the agent pixels of the region the user dragged over. When that
/// region is a `<webview>` guest, the bridge can do better: the same
/// accessibility machinery `browser_snapshot` uses already names the elements
/// under those pixels, so the message can carry a STRUCTURAL pointer beside
/// the image — and, on an action-drivable partition, the very `@eN` ref
/// `browser_click` takes.
///
/// The sequence, against the guest's debugger session:
///
///   1. hit-test the rect centre in the page (`elementFromPoint`, then
///      `closest()` up to the nearest interactive ancestor — the user drags
///      over a button's LABEL, and means the button);
///   2. turn that JS object into a `backendNodeId` (requestNode → describeNode)
///      — the id the accessibility tree keys on;
///   3. take the tab's AX snapshot and compact it exactly as `browser_snapshot`
///      does, which MINTS the `@eN` refs; the caller registers them, so the
///      ref in the message is live for a follow-up `browser_click`;
///   4. read role + name for that node (`getPartialAXTree`), falling back to
///      the DOM tag name when the page exposes no accessibility node.
///
/// Every step degrades to "no pointer" rather than failing the capture: the
/// crop is the deliverable, the pointer is the bonus. Electron-free (the
/// `CdpSend` seam is annotation.ts's), so `node --test` drives the whole
/// sequence against a fake.
import { compactAxTree, type AxNode } from './browserbridge.ts';
import type { CdpSend } from './annotation.ts';
import type { UiPointer } from '../../src/state/uiPointer.ts';

/// The ancestors worth pointing at. Deliberately the same shape as the AX
/// tree's interactive roles: if the user drags over the text inside a button,
/// the pointer should name the button, not the text node — and only these get
/// `@eN` refs, so anything else would resolve to a ref-less pointer anyway.
export const POINTER_INTERACTIVE_SELECTOR =
  'a[href],button,input,select,textarea,summary,label,[role],[onclick],[tabindex],[contenteditable]';

/// The hit-test expression. Coordinates are interpolated as VALIDATED
/// integers — the only numbers that reach it come from the user's own drag,
/// but the rule ("no caller string ever enters an evaluated expression") is
/// the bridge's and holds here too.
export function pointerHitScript(x: number, y: number): string {
  const px = Math.round(x);
  const py = Math.round(y);
  return `(function(){var el=document.elementFromPoint(${String(px)},${String(py)});if(!el)return null;return el.closest(${JSON.stringify(POINTER_INTERACTIVE_SELECTOR)})||el;})()`;
}

export interface PointerResolution {
  /// `ref` is left unset here: the CALLER registers the node with the bridge
  /// (registerAnnotationRef — merge semantics, never clobbering the map the
  /// agent's last snapshot minted) and fills in whatever name that returns.
  pointer: UiPointer;
  /// The node to register, when the element is interactive (this compaction
  /// minted a ref for it). null = a ref-less pointer: nothing to register,
  /// and the tab's existing ref map must not be touched.
  refBackendNodeId: number | null;
}

/// Resolve the element under `point` (guest CSS px, i.e. the capture rect's
/// centre translated into the guest's own coordinate space). Returns null when
/// there is nothing to point at; throws only if the caller's `send` does.
///
/// This runs AFTER capturePage (a pointer failure must never cost the crop),
/// so it hit-tests what is under the point NOW, not what the crop shows — a
/// navigation, scroll, or SPA re-render in between can make the pointer name
/// a different element than the pixels. The pre-send chip is the mitigation:
/// the user sees what the agent will be told and re-selects on a mismatch.
export async function resolvePointer(
  send: CdpSend,
  tabId: number,
  point: { x: number; y: number },
  opts: { actionable: boolean },
): Promise<PointerResolution | null> {
  // `DOM.requestNode` needs the DOM agent to hold a document; getting it is
  // also how the annotation injection primes its own session.
  await send('DOM.getDocument', { depth: 0 });

  const hit = (await send('Runtime.evaluate', { expression: pointerHitScript(point.x, point.y), returnByValue: false })) as {
    result?: { objectId?: string; subtype?: string };
  };
  const objectId = hit.result?.objectId;
  if (typeof objectId !== 'string' || hit.result?.subtype === 'null') return null;

  let backendNodeId: number | null = null;
  let tagName = '';
  try {
    const requested = (await send('DOM.requestNode', { objectId })) as { nodeId?: number };
    if (typeof requested.nodeId !== 'number' || requested.nodeId === 0) return null;
    const described = (await send('DOM.describeNode', { nodeId: requested.nodeId })) as {
      node?: { backendNodeId?: number; nodeName?: string };
    };
    if (typeof described.node?.backendNodeId !== 'number') return null;
    backendNodeId = described.node.backendNodeId;
    tagName = typeof described.node.nodeName === 'string' ? described.node.nodeName.toLowerCase() : '';
  } finally {
    await send('Runtime.releaseObject', { objectId }).catch(() => undefined);
  }

  // Interactivity is decided by the SAME compaction browser_snapshot
  // performs — an element it would mint a ref for is one browser_click can
  // address. Only the DECISION is taken from this compaction; the name the
  // agent sees comes from the bridge's registry (registerAnnotationRef), so
  // the map an earlier snapshot minted is merged into, never replaced.
  const tree = (await send('Accessibility.getFullAXTree')) as { nodes?: AxNode[] };
  const compact = compactAxTree(tree.nodes ?? []);
  let refable = false;
  for (const backend of compact.refs.values()) {
    if (backend === backendNodeId) {
      refable = true;
      break;
    }
  }

  const described = await describeAx(send, backendNodeId);
  const pointer: UiPointer = { tab_id: tabId, actionable: opts.actionable };
  const role = described.role !== '' ? described.role : tagName;
  if (role !== '') pointer.role = role;
  if (described.name !== '') pointer.name = described.name;
  return { pointer, refBackendNodeId: refable ? backendNodeId : null };
}

/// Role + accessible name for one DOM node. A page with no accessibility node
/// for it (canvas apps, aria-hidden subtrees) answers empty, and the caller
/// falls back to the tag name — an honest "a div" beats a fabricated role.
async function describeAx(send: CdpSend, backendNodeId: number): Promise<{ role: string; name: string }> {
  try {
    const partial = (await send('Accessibility.getPartialAXTree', { backendNodeId, fetchRelatives: false })) as {
      nodes?: AxNode[];
    };
    const node = (partial.nodes ?? []).find((n) => n.backendDOMNodeId === backendNodeId && n.ignored !== true);
    if (node === undefined) return { role: '', name: '' };
    return {
      role: (node.role?.value ?? '').toLowerCase(),
      name: clipName(node.name?.value ?? ''),
    };
  } catch {
    return { role: '', name: '' };
  }
}

/// Accessible names can be paragraphs. The pointer is a reference, not a
/// content channel — the same discipline compactAxTree applies to its lines.
const POINTER_NAME_MAX = 80;

export function clipName(name: string): string {
  const flat = name.replace(/\s+/g, ' ').trim();
  return flat.length > POINTER_NAME_MAX ? `${flat.slice(0, POINTER_NAME_MAX - 1)}…` : flat;
}
