-- tasks.block_reason — a dedicated field for *why* a task is currently
-- blocked, so callers stop overloading body_md as the block-reason field
-- and destroying the original task description (#54). body_md is the
-- task's standing description; block_reason is the transient "what is
-- holding this up right now" note, cleared when the task leaves the
-- blocked state. History of blocks lives in audit_events, not here.
ALTER TABLE tasks ADD COLUMN block_reason TEXT;
