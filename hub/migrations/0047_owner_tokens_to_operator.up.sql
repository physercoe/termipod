-- 0047_owner_tokens_to_operator.up.sql — operator/principal split (ADR-037 D2/D4).
--
-- Before this migration there was no operator kind: the single
-- bootstrap `owner` token (init.go) was the hub root — `requireOwner`
-- gated every /v1/admin/* endpoint and the hub-wide config surface.
-- ADR-037 splits that conflated credential into a hub-wide `operator`
-- and a per-team `owner` (principal/director).
--
-- Every `owner` token that existed pre-split was, by definition, a hub
-- root (there was no team provisioning, so the only owner was the
-- `default` bootstrap owner, and any extra owners a single-user install
-- hand-minted carried the same unrestricted reach). Reclassifying them
-- all to `operator` therefore preserves their exact prior capability:
-- operator is strictly more privileged than owner (requireOwner now
-- also admits operator). New per-team owners minted *after* this
-- migration — by an operator, the team-provisioning endpoint (W3), or
-- `tokens issue --kind owner` — are written as `owner` and stay owner.
--
-- Idempotent: re-running matches nothing once converted.

UPDATE auth_tokens
   SET kind = 'operator'
 WHERE kind = 'owner';
