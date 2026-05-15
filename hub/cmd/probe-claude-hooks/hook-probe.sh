#!/usr/bin/env bash
# Hook probe for claude-code v2.x.
#
# Captures the JSON payload of each hook event to a per-call file
# under /tmp/cc-hook-probe/. Pure observation — does not override
# claude-code's decisions, does not block the agent.
#
# Configured per-event via .claude/settings.local.json:
#   "command": "bash /path/to/hook-probe.sh <EventName>"
#
# Each invocation:
#   - reads the hook payload as JSON from stdin
#   - writes it (with _event + _ts metadata) to
#     /tmp/cc-hook-probe/<EventName>-<ts>.json
#   - echoes "{}" to stdout (empty hook output = no override)
#   - exits 0 so claude-code doesn't treat the hook as failing
#
# Inspect results after a session:
#   ls -la /tmp/cc-hook-probe/
#   jq . /tmp/cc-hook-probe/Notification-*.json | less
#   jq -r '._event' /tmp/cc-hook-probe/*.json | sort | uniq -c
#
# Reset between scenarios:
#   rm -rf /tmp/cc-hook-probe/
#
# Override log location:
#   CC_HOOK_PROBE_DIR=/some/other/path bash hook-probe.sh <Event>

set -euo pipefail

EVENT="${1:-unknown}"
LOG_DIR="${CC_HOOK_PROBE_DIR:-/tmp/cc-hook-probe}"
mkdir -p "$LOG_DIR"

TS=$(date +%s%N)
OUT="${LOG_DIR}/${EVENT}-${TS}.json"

# Read stdin (the hook's JSON payload). If empty or malformed, still
# record the event so we know it fired.
PAYLOAD="$(cat || true)"

if [[ -z "$PAYLOAD" ]]; then
  printf '{"_event":"%s","_ts":"%s","_note":"empty stdin"}\n' "$EVENT" "$TS" > "$OUT"
elif printf '%s' "$PAYLOAD" | jq . > /dev/null 2>&1; then
  printf '%s' "$PAYLOAD" | jq --arg event "$EVENT" --arg ts "$TS" \
    '. + {_event: $event, _ts: $ts}' > "$OUT"
else
  # Malformed JSON: capture raw with metadata wrapper.
  RAW_ESCAPED=$(printf '%s' "$PAYLOAD" | jq -Rs .)
  printf '{"_event":"%s","_ts":"%s","_note":"non-json stdin","_raw":%s}\n' \
    "$EVENT" "$TS" "$RAW_ESCAPED" > "$OUT"
fi

# Empty hook output = no override, no decision, no block.
echo '{}'
exit 0
