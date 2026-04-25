# Logo sketches

The current `docs/logo/logo.svg` dates from the MuxPod era — a `>_ ✦`
shell prompt. That reads "AI terminal". The product is now a
director's harness for a pod of agents; the terminal is one surface
among many. These five sketches span "evolve the prompt DNA" to "new
identity".

All use the existing palette: dark `#0D0E18` tile + teal→blue gradient
(`#00e0d0` → `#0090ff`). Animations are omitted for side-by-side
comparison; the chosen direction gets the pulse/twinkle added back.

| # | File | Concept | Keeps prompt DNA? | Says "agent pod"? | Favicon-friendly? |
|---|---|---|---|---|---|
| 1 | [01-orbit.svg](01-orbit.svg) | Steward spark at center, three agent nodes on an orbit ring | ✗ | ✅✅ | ◐ (dots shrink) |
| 2 | [02-capsule.svg](02-capsule.svg) | The current `>_ ✦`, but enclosed in a literal pod capsule | ✅ | ◐ | ✅ |
| 3 | [03-monogram.svg](03-monogram.svg) | Geometric **T** with a sparkle as the dot-over-the-letter | ◐ | ✗ | ✅✅ |
| 4 | [04-hex-node.svg](04-hex-node.svg) | `>_ ✦` inside a hexagonal compute-node pod | ✅ | ✅ | ◐ |
| 5 | [05-nested.svg](05-nested.svg) | Three concentric rounded squares, spark at the core — principal → steward → agent | ✗ | ✅ | ◐ |

## Reading them

**1. Orbit** — tells the product story most directly. One steward (the
spark), three agents (the dots), one team (the ring). Feels like an
AI-console, not a terminal app. Risk: the three dots disappear at 32 px,
so the favicon becomes "a sparkle in a circle" which is generic.

**2. Capsule** — most conservative. Keeps the existing marks (`>`, `_`,
sparkle) and adds a pod outline around them, making the "termi + pod"
name literal. The evolution, not revolution, option. Scales well.

**3. Monogram** — the classic move when you outgrow a concept icon:
retreat to a letterform. **T** with a twinkle dot works anywhere from
favicon to splash screen, and survives future renames of "Steward" /
"Agent" vocabulary because it doesn't depict either. Risk: least
distinctive — a lot of products are a letter + sparkle.

**4. Hex node** — same prompt glyphs as today, framed as a node in a
compute mesh. Reads "this device is a pod on a network". Good middle
ground between 1 and 2. Risk: visually busy; hexagons are overused in
dev tooling.

**5. Nested** — pure abstract: the delegation stack (you → steward →
agent) as three concentric containers with the spark at the core. Most
conceptual, most faithful to the blueprint. Risk: reads as "generic app
tile" without the story — needs a moment of explanation.

## Recommendation

If the product pitch is "I direct a pod of agents", **1 (Orbit)** tells
that in a glance. If we want to preserve the current brand continuity,
**2 (Capsule)** is the minimum-regret evolution. **3 (Monogram)** is the
safe long-term play that survives any future renames.

Pick one (or mix — e.g., Orbit as app icon, Monogram as favicon) and
the chosen direction gets:

- `assets/icon/icon.png` + `icon-foreground.png` re-rendered at launcher
  sizes
- The animated halo/pulse from the current `logo.svg` added back
- `docs/logo/logo.svg` overwritten; sketches directory can stay as the
  archive of what we considered
