import { create } from 'zustand';

/// Surface-provided context for the dock companion (the unified assistant
/// dock — ui/AssistantDock.tsx). Each surface that can feed the companion
/// registers a provider keyed by its job: a `label` for the context chip, a
/// `build()` that produces the context block prepended to a sent message, and
/// an optional `insert()` for the "insert reply into the surface" affordance.
/// The dock's AgentCompanion reads the ACTIVE provider and uses it for its
/// context chip + onInsert.
///
/// Focus tracking — the sanctioned approximation: Read/Author have no focus
/// event of their own, so a surface REGISTERS on mount and again on
/// selection / active-document change, and UNREGISTERS on unmount (or when it
/// no longer has a context to offer — e.g. no paper selected). Registration
/// moves the job to the top of the focus-order stack, so the active provider
/// is the surface whose context the user most recently touched. In a split
/// (Read | Author both mounted) simply editing without a selection change
/// does not re-signal focus — cheap and documented, per the approved design.

export interface CompanionContextProvider {
  /// Shown in the companion's context chip (paper/doc title).
  label: string;
  /// The context block prepended to a sent message. Read LIVE at send time
  /// (providers close over the store, not a render snapshot).
  build: () => string;
  /// The "insert reply" target — absent when the surface has no safe insert
  /// (e.g. a structured document body).
  insert?: (text: string) => void;
}

interface CompanionContextState {
  providers: Readonly<Record<string, CompanionContextProvider>>;
  /// Focus order, least → most recent; the active provider tops the stack.
  order: readonly string[];
  /// Upsert the job's provider AND mark it most-recently focused.
  register: (job: string, provider: CompanionContextProvider) => void;
  unregister: (job: string) => void;
}

/// The provider the dock companion should use right now: the most recently
/// registered surface's. Returns the stored object (stable reference, safe as
/// a zustand selector result).
export function activeProvider(s: {
  providers: Readonly<Record<string, CompanionContextProvider>>;
  order: readonly string[];
}): CompanionContextProvider | null {
  const top = s.order[s.order.length - 1];
  return top !== undefined ? (s.providers[top] ?? null) : null;
}

export const useCompanionContext = create<CompanionContextState>((set) => ({
  providers: {},
  order: [],
  register: (job, provider) =>
    set((s) => ({
      providers: { ...s.providers, [job]: provider },
      order: [...s.order.filter((j) => j !== job), job],
    })),
  unregister: (job) =>
    set((s) => {
      if (!(job in s.providers)) return s;
      const providers = { ...s.providers };
      delete providers[job];
      return { providers, order: s.order.filter((j) => j !== job) };
    }),
}));
