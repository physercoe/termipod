-- Scraper enrichment for reference_items (desktop ADR-053 companion work).
-- The desktop "Enrich" scraper fetches a reference's citation graph (references
-- + cited-by), journal metrics (an open, IF-like signal), open-access status,
-- topics, and code/data links. That's derived metadata the hub stores but does
-- NOT interpret — one opaque JSON blob, not a column per field, keeping the hub
-- agnostic to the enrichment shape (it can evolve on the desktop without a
-- migration). Agents still see it: it round-trips through the reference_* tools.
ALTER TABLE reference_items ADD COLUMN enrichment_json TEXT;
