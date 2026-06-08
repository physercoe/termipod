-- WS3 (ADR-046): the `write-memo` and `reproduce-paper` project templates
-- were empty single-phase-less shells (5 of 6 shipped templates were). They
-- are removed from the embedded set; this drops any rows a prior Init seeded
-- so they no longer appear in the template picker. Concrete projects created
-- from them keep their own materialized state — `template_id` is a plain text
-- column, not a foreign key, so deleting the template row leaves them intact.
DELETE FROM projects
 WHERE is_template = 1
   AND name IN ('write-memo', 'reproduce-paper');
