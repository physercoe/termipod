# Security policy

Thanks for taking the time to report a vulnerability. We treat
security reports seriously and aim to respond promptly.

## Supported versions

TermiPod is pre-1.0 (alpha). Only the **latest tagged release** is
supported with security fixes. Older alpha tags will not receive
backports; upgrade to the latest tag to receive any fix.

You can find the latest release at
[github.com/physercoe/termipod/releases](https://github.com/physercoe/termipod/releases).

## Reporting a vulnerability

**Please do not file a public GitHub issue for security reports.**

Use GitHub's private vulnerability reporting:

1. Go to
   [github.com/physercoe/termipod/security/advisories/new](https://github.com/physercoe/termipod/security/advisories/new).
2. Fill in the form with:
   - A short title (one line)
   - Affected versions (tag or commit, if known)
   - Reproduction (steps, payload, minimum environment)
   - Impact (what an attacker can do)
3. Submit.

Maintainers receive the advisory privately and can collaborate with
you inside GitHub on a fix without exposing details publicly.

## Response timeline

- **Triage:** best-effort within 7 days of submission.
- **Fix:** depends on severity and complexity; we'll communicate an
  estimate during triage.
- **Disclosure:** coordinated. We aim for a minimum of 30 days
  between fix availability and public disclosure, longer if the
  fix requires user action (e.g., upgrading host-runner on remote
  hosts).

This project is maintained on a best-effort basis. There is no
formal SLA.

## Disclosure policy

We prefer **coordinated disclosure**:

1. You report privately via the channel above.
2. Maintainers investigate and develop a fix.
3. We agree on a disclosure date with you.
4. The fix ships in a tagged release.
5. Public details (advisory + CVE if applicable) publish on the
   agreed date, crediting the reporter unless they prefer
   anonymity.

If you intend to disclose publicly before a fix is available, please
let us know in advance so we can coordinate user communication.

## Out of scope

The following are explicitly **out of scope** for this project's
security policy:

- **Issues in third-party engines** (Claude Code, Codex CLI, Gemini
  CLI). Report those to the respective vendors. TermiPod spawns
  these as subprocesses but does not own their security posture.
- **Issues requiring physical access** to an unlocked device. The
  mobile app uses `flutter_secure_storage` (OS keychain) for
  bearer tokens and SSH keys; if an attacker has physical access to
  an unlocked device, that's a device-security issue, not an app
  issue.
- **Issues in user-supplied templates or YAML specs.** Templates
  define what agents do; a malicious template is operator-side
  abuse, not a vulnerability in this project. (Report supply-chain
  concerns about *bundled* templates — those are in scope.)
- **Self-hosted misconfiguration** (e.g., running `hub-server` with
  `0.0.0.0:8443` on a public network without TLS). The
  [`install-hub-server.md`](docs/how-to/install-hub-server.md)
  guide warns against these patterns; reports of "I did the
  unsupported thing and got owned" are out of scope.

## Scope clarifications

In scope:

- Mobile app (`lib/`)
- Hub services (`hub/cmd/hub-server`, `hub/cmd/host-runner`,
  `hub/cmd/hub-mcp-server`, `hub/cmd/hub-mcp-bridge`)
- Bundled templates under `hub/templates/`
- Default configurations and example deploys under `hub/deploy/`

Out of scope:

- Anything in `docs/` (these are docs, not runtime)
- Test fixtures and test-only code
- The mock-trainer (`hub/cmd/mock-trainer`) is a development
  harness, not production code

## Hardening defaults

This project's hardening posture is documented in the architecture
spine; relevant references:

- [`docs/spine/blueprint.md`](docs/spine/blueprint.md) — overall
  architecture, including the auth model
- [`docs/how-to/install-hub-server.md`](docs/how-to/install-hub-server.md)
  — production deploy with TLS, systemd hardening, dedicated user
- [`docs/how-to/install-host-runner.md`](docs/how-to/install-host-runner.md)
  — host-runner deploy + token discipline

Bearer tokens are stored as SHA-256 hashes server-side; only the
plaintext leaves the issuer once. The mobile app stores tokens in
`flutter_secure_storage` (OS keychain).
