/// The hub returns loosely-typed JSON maps; the Flutter app reads them as
/// `Map<String, dynamic>`. We mirror that: a permissive record, narrowed at the
/// point of use rather than with rigid DTOs (structural, typed where stable).
export type Entity = Record<string, unknown>;

export interface HubInfo {
  name?: string;
  version?: string;
  [k: string]: unknown;
}

export function str(e: Entity, key: string): string | undefined {
  const v = e[key];
  return typeof v === 'string' ? v : undefined;
}

export function num(e: Entity, key: string): number | undefined {
  const v = e[key];
  return typeof v === 'number' ? v : undefined;
}

export function obj(e: Entity, key: string): Entity | undefined {
  const v = e[key];
  return v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Entity) : undefined;
}
