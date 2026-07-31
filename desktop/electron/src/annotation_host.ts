/// Annotation overlay — Electron main-process orchestration (D2 —
/// docs/plans/desktop-ui-context-and-pointing.md §3.4/§3.5). The electron-free
/// core (rect mapping, refusal, downscale fit, the CDP injection sequence) is
/// annotation.ts; this module owns the live halves:
///   - `annotation_capture`: capturePage on the intersected `<webview>` guest
///     (translated coords) or the shell window, downscale to the
///     browser_screenshot discipline (PNG, ≤1568px max edge), write the crop
///     to a 0o600 temp file, return it + a base64 copy for the companion path;
///   - `annotation_attach_kimi`: inject the crop into the kimi web composer's
///     file input over CDP `DOM.setFileInputFiles` — the same mechanism W2's
///     browser_upload_file proves, but main-side on the USER's gesture and
///     NEVER registered as a bridge tool: the kimiweb partition's read-only
///     posture binds agent traffic, not the desktop acting for its own user
///     (plan §3.5/§7 OQ-5). Any injection failure degrades to clipboard +
///     guest focus + a paste hint (the crop is never lost);
///   - `annotation_discard`: cancel → delete the temp crop.
///
/// Every handler is gated on the UI-context-sharing toggle (desktopui.ts —
/// the same toggle as D1, no new one): toggle off, no capture. Captures and
/// kimi attaches land in the bridge's audit ring (recordUserOverlayAudit —
/// ring-only, never hub-mirrored: the actor is the user, there is no calling
/// agent's stream). Refusals and Esc-cancels record nothing (plan §3.8).
import { clipboard, nativeImage, webContents, type WebContents } from 'electron';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  annotationTempPath,
  injectImageIntoComposer,
  injectNoteIntoComposer,
  isAnnotationTempFile,
  fitWithin,
  pickCaptureTarget,
  refusedSurface,
  toIntRect,
  type AnnotRect,
  type GuestRegion,
  type SurfaceRegion,
} from './annotation';
import { registerAnnotationRef, stripFragment } from './browserbridge';
import { recordUserOverlayAudit } from './browserbridge_host';
import { isUiSharingEnabled } from './desktopui';
import { resolvePointer } from './uipointer';
import type { Handler } from './ipc/dispatch';
import { policyForGuest } from './webtab';
import { KIMIWEB_PARTITION } from './webtab_policy';
import type { UiPointer } from '../../src/state/uiPointer';

// ── Arg narrowing (the renderer is our own, but handlers validate anyway) ────

const MAX_RECT_EDGE = 16384;
const MAX_REGIONS = 16;

function asNum(v: unknown): number | null {
  return typeof v === 'number' && Number.isFinite(v) ? v : null;
}

function asRect(v: unknown): AnnotRect | null {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return null;
  const r = v as Record<string, unknown>;
  const x = asNum(r.x);
  const y = asNum(r.y);
  const width = asNum(r.width);
  const height = asNum(r.height);
  if (x === null || y === null || width === null || height === null) return null;
  if (width <= 0 || height <= 0 || width > MAX_RECT_EDGE || height > MAX_RECT_EDGE) return null;
  if (Math.abs(x) > MAX_RECT_EDGE || Math.abs(y) > MAX_RECT_EDGE) return null;
  return { x, y, width, height };
}

function asGuests(v: unknown): GuestRegion[] {
  if (!Array.isArray(v)) return [];
  const out: GuestRegion[] = [];
  for (const g of v.slice(0, MAX_REGIONS)) {
    if (g === null || typeof g !== 'object') continue;
    const id = (g as Record<string, unknown>).id;
    const rect = asRect((g as Record<string, unknown>).rect);
    if (typeof id === 'number' && Number.isInteger(id) && id > 0 && rect !== null) out.push({ id, rect });
  }
  return out;
}

function asRegions(v: unknown): SurfaceRegion[] {
  if (!Array.isArray(v)) return [];
  const out: SurfaceRegion[] = [];
  for (const r of v.slice(0, MAX_REGIONS)) {
    if (r === null || typeof r !== 'object') continue;
    const surface = (r as Record<string, unknown>).surface;
    const rect = asRect((r as Record<string, unknown>).rect);
    // The renderer can only ever ADD refusal (only regions whose surface row
    // is capture:refuse count — refusedSurface re-checks the table), and a
    // stricter refusal can never leak pixels. It can never widen capture.
    if (typeof surface === 'string' && surface !== '' && surface.length <= 64 && rect !== null) out.push({ surface, rect });
  }
  return out;
}

// ── Guest resolution ─────────────────────────────────────────────────────────

/// A capture/injection target must be a LIVE `<webview>` guest in an
/// allowlisted partition — `webContents.fromId` would happily return the
/// app:// shell to a caller that guessed its id (the bridge's resolveGuest
/// rule, browserbridge_host.ts). `partition`, when given, additionally pins
/// the guest to that partition (the kimi attach must never touch a webtab).
function resolveGuest(id: number, partition?: string): WebContents | null {
  const wc = webContents.fromId(id);
  if (wc === undefined || wc.isDestroyed() || wc.getType() !== 'webview') return null;
  const policy = policyForGuest(wc);
  if (policy === null) return null;
  if (partition !== undefined && policy.partition !== partition) return null;
  return wc;
}

// ── Temp crop files (small LRU — the kimi composer may read lazily) ──────────

const tempFiles: string[] = [];
const TEMP_FILE_KEEP = 4;

function rememberTempFile(file: string): void {
  tempFiles.push(file);
  while (tempFiles.length > TEMP_FILE_KEEP) {
    const old = tempFiles.shift();
    if (old !== undefined) fs.rmSync(old, { force: true });
  }
}

function dropTempFile(file: string): void {
  const i = tempFiles.indexOf(file);
  if (i >= 0) tempFiles.splice(i, 1);
  fs.rmSync(file, { force: true });
}

/// before-quit hygiene (main.ts, next to the other dispose calls).
export function disposeAnnotations(): void {
  while (tempFiles.length > 0) {
    const f = tempFiles.shift();
    if (f !== undefined) fs.rmSync(f, { force: true });
  }
}

// ── CDP (the user-gesture injection — separate from the bridge's sessions) ───

const attached = new Set<number>();

/// Lazily attach to the kimi guest's debugger. If the bridge (or the user,
/// via a previous attach) already holds a session, `isAttached` is true and
/// `sendCommand` works regardless of who attached — no stealing. An attach
/// failure (devtools holds the one session) throws, and the caller degrades
/// to the clipboard fallback.
function ensureDebugger(wc: WebContents): void {
  if (attached.has(wc.id) && wc.debugger.isAttached()) return;
  if (!wc.debugger.isAttached()) wc.debugger.attach('1.3');
  attached.add(wc.id);
  wc.debugger.once('detach', () => attached.delete(wc.id));
  wc.once('destroyed', () => attached.delete(wc.id));
}

// ── D4 element-resolved pointing ─────────────────────────────────────────────

/// Resolve the element under the crop's centre, for a rect that landed on a
/// bridgeable guest. Best-effort by design: the crop is the deliverable and a
/// pointer is the bonus, so every failure path (no debugger, no AX tree, a
/// canvas app with nothing to name) yields `null` and the capture still
/// returns. An interactive hit is registered with the bridge (merge, never
/// replacing the map the agent's last snapshot minted) so the ref in the
/// message is one `browser_click` can resolve.
///
/// The point is the CLIPPED rect's centre: a drag straddling the guest edge
/// hit-tests the centre of the part inside the guest, not the user's whole
/// drag — the part outside the guest has no elements to name.
async function pointerForGuest(wc: WebContents, rect: AnnotRect): Promise<UiPointer | null> {
  const policy = policyForGuest(wc);
  // `none` partitions are outside the bridge entirely — an @eN ref for a tab
  // no tool can address would be a reference to nowhere.
  if (policy === null || policy.bridge === 'none') return null;
  try {
    ensureDebugger(wc);
    const resolved = await resolvePointer(
      (method, params) => wc.debugger.sendCommand(method, params ?? {}),
      wc.id,
      { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 },
      { actionable: policy.bridge === 'full' },
    );
    if (resolved === null) return null;
    if (resolved.refBackendNodeId !== null) {
      resolved.pointer.ref = registerAnnotationRef(wc.id, resolved.refBackendNodeId);
    }
    return resolved.pointer;
  } catch {
    return null;
  }
}

// ── Handlers ─────────────────────────────────────────────────────────────────

let captureSeq = 0;

export const annotationHostHandlers: Record<string, Handler> = {
  /// The overlay's mouseup: capture the selection. Args are the selection rect
  /// (window CSS px), the guest element rects the renderer knows (paired with
  /// webContents ids), and the visible surface regions (the refusal geometry —
  /// main re-applies the ui_policy `capture` column to the surface ids).
  annotation_capture: async (args, ctx) => {
    if (!isUiSharingEnabled()) return { ok: false, error: 'sharing-off' };
    const rect = asRect(args.rect);
    if (rect === null) return { ok: false, error: 'bad-rect' };
    const guests = asGuests(args.guests);
    const regions = asRegions(args.surface_regions);
    // Refusal only when the rect lies ENTIRELY inside a refuse region (plan
    // §3.4) — the user re-selects; nothing is captured or audited.
    const refused = refusedSurface(rect, regions);
    if (refused !== null) return { ok: false, refused: true, surface: refused };

    const target = pickCaptureTarget(rect, guests);
    let wc: WebContents;
    let partition: string | null = null;
    let guestForPointer: WebContents | null = null;
    if (target.kind === 'guest') {
      const guest = resolveGuest(target.id);
      if (guest === null) return { ok: false, error: 'guest-gone' };
      wc = guest;
      partition = policyForGuest(guest)?.partition ?? null;
      guestForPointer = guest;
    } else {
      if (ctx.win === null || ctx.win.isDestroyed()) return { ok: false, error: 'no-window' };
      wc = ctx.win.webContents;
    }

    try {
      let img = await wc.capturePage(toIntRect(target.rect));
      const size = img.getSize();
      const fit = fitWithin(size.width, size.height);
      if (fit.width !== size.width || fit.height !== size.height) {
        img = img.resize({ width: fit.width, height: fit.height, quality: 'good' });
      }
      const png = img.toPNG();
      captureSeq += 1;
      const file = annotationTempPath(os.tmpdir(), `${String(Date.now())}-${String(captureSeq)}`);
      fs.writeFileSync(file, png, { mode: 0o600 });
      rememberTempFile(file);
      // A small inline thumbnail for the target row + the companion chip —
      // the full crop crosses as base64 for the companion's postAgentInput.
      const after = img.getSize();
      const pfit = fitWithin(after.width, after.height, 320);
      const preview = (pfit.width !== after.width || pfit.height !== after.height
        ? img.resize({ width: pfit.width, height: pfit.height })
        : img
      ).toDataURL();
      // D4: the structural half of the same gesture. Resolved AFTER the
      // pixels so a pointer failure can never cost the user their crop.
      const pointer = guestForPointer !== null ? await pointerForGuest(guestForPointer, target.rect) : null;
      recordUserOverlayAudit({
        ts: new Date().toISOString(),
        tool: 'ui_annotate_capture',
        tab_id: target.kind === 'guest' ? target.id : null,
        url: target.kind === 'guest' ? stripFragment(wc.getURL()) : null,
        partition,
        args: {
          rect: [rect.x, rect.y, rect.width, rect.height],
          target: target.kind,
          width: fit.width,
          height: fit.height,
          // The audit says WHICH element was pointed at — the ref and role,
          // never the crop (the entry is a reference, ADR-062 D-2).
          ...(pointer !== null ? { pointer: { ref: pointer.ref ?? null, role: pointer.role ?? null } } : {}),
        },
        ok: true,
        error: null,
      });
      return {
        ok: true,
        file,
        width: fit.width,
        height: fit.height,
        preview,
        data_b64: png.toString('base64'),
        target: target.kind,
        ...(pointer !== null ? { pointer } : {}),
      };
    } catch (e) {
      recordUserOverlayAudit({
        ts: new Date().toISOString(),
        tool: 'ui_annotate_capture',
        tab_id: target.kind === 'guest' ? target.id : null,
        url: null,
        partition,
        args: { rect: [rect.x, rect.y, rect.width, rect.height], target: target.kind },
        ok: false,
        error: e instanceof Error ? e.message : String(e),
      });
      return { ok: false, error: 'capture-failed' };
    }
  },

  /// "Attach to kimi web": inject the crop into the kimi composer's file
  /// input, and the note (the user's words + the D4 pointer line) into its
  /// text box — the kimi path must deliver the same pointer the companion
  /// path folds into postAgentInput, or the pre-send chip promises what the
  /// agent never receives. The renderer names the kimi guest (it knows which
  /// panel is open); main re-validates the id is a live kimiweb-partition
  /// guest before attaching — this path can never reach a webtab or the shell.
  annotation_attach_kimi: async (args) => {
    if (!isUiSharingEnabled()) return { ok: false, error: 'sharing-off' };
    const file = typeof args.file === 'string' ? args.file : '';
    const note = typeof args.note === 'string' ? args.note : '';
    const guestId = typeof args.guest_id === 'number' && Number.isInteger(args.guest_id) ? args.guest_id : null;
    if (!isAnnotationTempFile(os.tmpdir(), file) || !fs.existsSync(file)) return { ok: false, error: 'bad-file' };
    if (guestId === null) return { ok: false, error: 'bad-guest' };
    const wc = resolveGuest(guestId, KIMIWEB_PARTITION);
    if (wc === null) return { ok: false, error: 'guest-gone' };

    const audit = (ok: boolean, extra: Record<string, unknown>, error: string | null): void =>
      recordUserOverlayAudit({
        ts: new Date().toISOString(),
        tool: 'ui_annotate_attach_kimi',
        tab_id: guestId,
        url: stripFragment(wc.getURL()),
        partition: KIMIWEB_PARTITION,
        args: { file: path.basename(file), ...extra },
        ok,
        error,
      });

    /// The degrade for every injection failure (no composer input, debugger
    /// busy, CDP error): the crop goes to the clipboard and the panel gets
    /// focus — the user is one Cmd+V from the same result (plan §3.5). The
    /// result names which fallback actually HAPPENED: an unreadable crop or a
    /// clipboard error must not produce a "Copied — paste" toast for a
    /// clipboard that holds nothing (the audit records the same truth).
    const fallback = (reason: string): { ok: true; injected: false; fallback: 'clipboard' | 'clipboard-failed' } => {
      try {
        const img = nativeImage.createFromPath(file);
        if (img.isEmpty()) throw new Error('empty-image');
        clipboard.writeImage(img);
        wc.focus();
        audit(true, { via: 'clipboard', reason }, null);
        return { ok: true, injected: false, fallback: 'clipboard' };
      } catch {
        audit(false, { via: 'clipboard', reason }, 'clipboard-failed');
        return { ok: true, injected: false, fallback: 'clipboard-failed' };
      }
    };

    try {
      ensureDebugger(wc);
      const send = (method: string, params?: Record<string, unknown>): Promise<unknown> => wc.debugger.sendCommand(method, params ?? {});
      const res = await injectImageIntoComposer(send, file);
      if (res !== 'attached') return fallback('no-input');
      // The note rides beside the crop. Its failure must not cost the
      // attachment (the crop is the deliverable) — the result says whether
      // it landed so the renderer can tell the user to type it themselves.
      // The audit records the FLAG only, never the note's content.
      let noteInjected: boolean | undefined;
      if (note !== '') {
        try {
          noteInjected = (await injectNoteIntoComposer(send, note)) === 'injected';
        } catch {
          noteInjected = false;
        }
      }
      wc.focus();
      audit(true, { via: 'file-input', ...(noteInjected !== undefined ? { note_injected: noteInjected } : {}) }, null);
      return { ok: true, injected: true, ...(noteInjected !== undefined ? { note_injected: noteInjected } : {}) };
    } catch (e) {
      return fallback(e instanceof Error ? e.message : String(e));
    }
  },

  /// Cancel (or companion-path handoff): delete the temp crop.
  annotation_discard: async (args) => {
    const file = typeof args.file === 'string' ? args.file : '';
    if (isAnnotationTempFile(os.tmpdir(), file)) dropTempFile(file);
    return { ok: true };
  },
};
