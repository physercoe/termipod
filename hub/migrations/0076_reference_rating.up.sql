-- A director-curated 1–5 score for prioritizing literature in the Read library.
-- NULL means the item has not been rated.

ALTER TABLE reference_items
    ADD COLUMN rating INTEGER CHECK (rating IS NULL OR rating BETWEEN 1 AND 5);
