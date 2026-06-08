-- #56 (ADR-046): template hydration historically inserted
-- acceptance_criteria rows without a deliverable_id, even though each
-- phase's criteria belong to that phase's deliverable (the preset specs
-- carry a `deliverable_ref` on every criterion). The deliverable viewer
-- filters criteria by deliverable_id, so template-created projects showed
-- ZERO criteria in the viewer. The hydrator now sets the column; this
-- one-shot heals projects created before the fix.

-- 1. Gate criteria already carry the deliverable ULID in
--    body.params.deliverable_id (rewritten at hydration, #21). Bind the
--    column to it when it points at a real deliverable in the project.
UPDATE acceptance_criteria
   SET deliverable_id = json_extract(body, '$.params.deliverable_id')
 WHERE deliverable_id IS NULL
   AND kind = 'gate'
   AND json_extract(body, '$.params.deliverable_id') IN (
         SELECT d.id FROM deliverables d
          WHERE d.project_id = acceptance_criteria.project_id
       );

-- 2. Remaining criteria (text/metric, and any gate without a usable body
--    ref): bind to the phase's deliverable when the phase has exactly
--    one. Phases with zero or many deliverables are left NULL — nothing
--    to unambiguously bind to. Every shipped preset phase has 0-or-1
--    deliverable, so this heals all template-created projects.
UPDATE acceptance_criteria
   SET deliverable_id = (
         SELECT d.id FROM deliverables d
          WHERE d.project_id = acceptance_criteria.project_id
            AND d.phase = acceptance_criteria.phase
          LIMIT 1
       )
 WHERE deliverable_id IS NULL
   AND (
         SELECT COUNT(*) FROM deliverables d
          WHERE d.project_id = acceptance_criteria.project_id
            AND d.phase = acceptance_criteria.phase
       ) = 1;
