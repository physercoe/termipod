-- attention_items.kind='decision' is renamed to 'select' for clarity:
-- "decision" was generic (every attention is a decision); the actual
-- semantic is "pick one of N labelled options". `select` reads sharper
-- in the resolver UI and stops blurring with the binary
-- `approval_request` kind.
--
-- The MCP tool name `request_decision` is intentionally NOT renamed —
-- that's the agent-facing wire contract and would break any prompt
-- templates that reference it.
UPDATE attention_items SET kind = 'select' WHERE kind = 'decision';
