-- Runs ↔ datasets (plan docs/plans/replay-datasets-episodes.md, wedge W5).
--
-- Runs were already first-class and datasets became first-class in 0068, but
-- nothing joined them: a training run could not say what it trained ON, and an
-- eval run could not say what it rolled OUT. That edge is what makes a run's
-- results watchable instead of merely plotted.
--
-- Plain TEXT with no REFERENCES clause, following 0005's note: SQLite cannot
-- add a foreign key with ALTER TABLE ADD COLUMN, and the migration runner sets
-- foreign_keys=OFF anyway. The link is enforced at the application layer —
-- handleUpdateRun refuses a dataset that is not in the run's own project, and
-- handleDeleteDataset clears the column rather than leaving a dangling id.
--
-- Nullable and unset by default. A run that is not about a dataset is the
-- common case, and the sniff that proposes one (run_dataset_hint.go) only ever
-- proposes: the write is an explicit act, because a config key that merely
-- *looks* like a dataset is a guess, and a wrong edge here would send someone
-- to watch the wrong robot.

ALTER TABLE runs ADD COLUMN dataset_id TEXT;

CREATE INDEX idx_runs_dataset ON runs(dataset_id) WHERE dataset_id IS NOT NULL;
