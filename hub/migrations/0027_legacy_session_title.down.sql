-- Reverting the title-clean migration is destructive (we can't
-- recover which sessions were the back-stamped legacy ones since
-- the migration only fired on title-equality). Best we can do is
-- a no-op down — the title cleanup is forward-only.
SELECT 1;
