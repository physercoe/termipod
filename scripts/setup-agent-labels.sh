#!/usr/bin/env bash
# Create (idempotently) the labels the agent-collaboration protocol uses.
# Run once per repo by the maintainer.
# See docs/how-to/agent-collaboration.md.
set -euo pipefail

create() { gh label create "$1" --color "$2" --description "$3" --force; }

# Ticket state machine
create "ticket:ready"     "0e8a16" "Specced and unclaimed — a builder may pick it up"
create "ticket:claimed"   "fbca04" "A builder has claimed this ticket"
create "ticket:in-review" "1d76db" "PR open, CI green, awaiting maintainer review"
create "ticket:changes"   "d93f0b" "Maintainer requested changes"
create "ticket:blocked"   "b60205" "Builder is blocked; needs maintainer attention"

# Capability tiers (describe the work, not the agent)
create "tier:mechanical"  "c2e0c6" "Bounded, near-mechanical work"
create "tier:medium"      "fef2c0" "Needs some reasoning"
create "tier:judgment"    "f9d0c4" "Needs design / vocabulary / ADR judgment"

# Hot-file baton
create "holds:arb"        "5319e7" "Baton: holds the lib/l10n/*.arb serialization lock"

echo "agent-collaboration labels created/updated."
