-- Down is a documented no-op (mirrors 0047). The added column and indexes are
-- inert to pre-migration code, which never reads channels.team_id; rolling the
-- schema back would collapse per-team #hub-meta isolation again, so we leave
-- the column in place rather than rebuild-and-drop (a second lossy rebuild for
-- no functional gain). Recreate the DB from a pre-0048 backup if a true
-- structural rollback is required.
SELECT 1;
