-- The Environment entity (plan docs/plans/environments-and-embodiments.md,
-- wedge E2). A run is policy × environment, generalization is performance
-- ACROSS env axes, and sim2real is an edge between a real site and its sim
-- twin — but until now the environment had no identity in the system, so the
-- physical-coherence metadata the episode element already carries (frames,
-- calibration, units, rig_id) had nothing to point AT.
--
-- IDENTITY, NOT SEMANTICS (plan §1 decision). The row answers "is this the
-- SAME task/scene, and which version?" — never "what is the task?". Rewards,
-- goal predicates and success criteria are code; schema-tizing them fails for
-- half the backends (BDDL/PDDL only ever worked inside their own ecosystems).
-- So task_ref is a POINTER plus a format tag a future viewer can render, and
-- success_desc is one human line. The only machine-readable task-adjacent
-- field stays the episode outcome enum, which lives on the episode.
--
-- SCOPE (plan §1 decision, and the review anchor "scope honesty"). team_id is
-- NOT NULL for every row: a physical bench outlives any one project, and two
-- projects sharing it must share its calibration history and twin edge. A sim
-- environment MAY additionally be project-scoped (project_id set); a real-site
-- family never is, which the handler enforces. Introducing this table
-- project-scoped and widening it later is the painful migration this decision
-- exists to avoid.
--
-- Not windowed like episodes: a team's registry is a curated list of tens, and
-- the bulk-data concern (blueprint §4) is the episode table, not this. The
-- opaque blobs (config_json, scene_refs_json) are capped at write time so the
-- registry cannot quietly become a byte store.

CREATE TABLE environments (
    id             TEXT PRIMARY KEY,
    team_id        TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    -- NULL = team-scoped (always, for real sites). Set = a sim environment
    -- that belongs to one line of work. ON DELETE SET NULL, not CASCADE:
    -- deleting a project must not delete an environment other projects may
    -- already reference by handle.
    project_id     TEXT REFERENCES projects(id) ON DELETE SET NULL,

    -- The handle's three parts. `env_ref = "family:env_id@version"` is built
    -- from exactly these, so their charset is validated at write time (family
    -- has no ':' or '@', env_id no '@') — otherwise a row could exist that no
    -- env_ref can name. This is validation of the ENTITY, and it does not
    -- change E0's contract: env_ref STRINGS on runs and datasets stay
    -- unvalidated, and an unmatched one resolves to "unresolved", never an
    -- error.
    family         TEXT NOT NULL,               -- isaac-lab | maniskill | mujoco | real-site | … (open set)
    env_id         TEXT NOT NULL,               -- task/scene name within the family (PickCube-v1, bench-3)
    version        TEXT NOT NULL DEFAULT '',    -- '' is a legitimate version: E0's derived handles carry none

    -- What the content actually is, for the drift question §0 names: benchmarks
    -- version env ids precisely because env drift silently invalidates
    -- comparison. Two registrations of one handle claiming different content
    -- are a conflict (409), not a merge — changed content means a new version.
    content_hash   TEXT NOT NULL DEFAULT '',

    embodiment_ref  TEXT NOT NULL DEFAULT '',   -- E1 manifest key (a robot_type alias)
    scene_refs_json TEXT NOT NULL DEFAULT '',   -- asset links; Objaverse/YCB stay INTEROP link+cache
    config_json     TEXT NOT NULL DEFAULT '',   -- physics + randomization spec, opaque to the hub
    task_ref        TEXT NOT NULL DEFAULT '',   -- pointer to the task spec/code (file, repo path, module)
    task_format     TEXT NOT NULL DEFAULT '',   -- how to read that pointer (bddl|pddl|python|yaml|…)
    success_desc    TEXT NOT NULL DEFAULT '',   -- one line, human — never parsed

    -- The sim2real edge (plan §1). One column read from either side rather than
    -- two mirrored rows, so the pair cannot disagree. Traversal + the "persist
    -- across versions" rule are E3's; the column exists now so E3 is not a
    -- migration on a table people already have rows in.
    twin_of        TEXT REFERENCES environments(id) ON DELETE SET NULL,

    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

-- The handle IS the identity: resolution matches (family, env_id, version)
-- exactly within a team, so two rows answering one env_ref would make the
-- resolved/unresolved distinction a lie. UNIQUE makes that unrepresentable.
CREATE UNIQUE INDEX idx_environments_handle
    ON environments(team_id, family, env_id, version);
CREATE INDEX idx_environments_team ON environments(team_id);
CREATE INDEX idx_environments_project
    ON environments(project_id) WHERE project_id IS NOT NULL;
CREATE INDEX idx_environments_twin
    ON environments(twin_of) WHERE twin_of IS NOT NULL;
