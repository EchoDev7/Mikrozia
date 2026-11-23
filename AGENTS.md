# Mikrozia Repository Instructions

These instructions apply to the entire repository, except vendored code under
`vendor/`. They are the entry point for Codex and other coding agents.

## Required reading

Before planning or changing code, read:

1. `docs/MIKROZIA_PROJECT_CHARTER.md` — authoritative product direction,
   architecture invariants, roadmap, and definition of done.
2. `CLAUDE.md` — inherited implementation map and engineering conventions.
3. `frontend/CLAUDE.md` when changing the frontend.
4. `docs/architecture.md` when changing request flow, jobs, database, nodes, or
   runtime boundaries.
5. `docs/MIKROZIA_PROJECT_LEDGER.md` — current task status, accepted decisions,
   verification gates, and owner-approval history.

If inherited Mikrozia documentation conflicts with the Mikrozia charter on product
direction or RouterOS behavior, the Mikrozia charter wins. Existing language,
testing, generation, and layering rules still apply unless the charter replaces
them explicitly.

## Mission

Mikrozia began as a fork of the 3x-ui project to avoid rebuilding a mature panel from zero.
The destination is an independent RouterOS-container product with its own
identity, releases, documentation, roadmap, and selectively maintained code.
The inherited structure is a bootstrap asset, not a permanent product boundary.

Do not perform a broad rename or module-path migration before the identity phase.
First establish a stable, tested RouterOS baseline; then separate branding and
project identity in a controlled phase.

## Non-negotiable product rules

- RouterOS Container and CHR are the primary production targets.
- Mikrozia never creates or changes RouterOS firewall, NAT, port-forward,
  masquerade, routing-policy, or access-control rules.
- Panel, Xray, and sidecar software are immutable image contents. Replace the
  container image to update software; do not self-modify the runtime RootFS.
- User data, settings, database state, and certificates live on persistent
  mounts and must survive image replacement and rollback.
- Never silently reset, delete, reinterpret, or weaken user data. Compatibility
  migrations must be explicit, idempotent, tested, and lossless.
- Keep every useful feature that RouterOS can support safely. Remove a feature
  only after proving it is impossible, unsafe, or outside the product contract.
- When removing a feature, remove its complete vertical slice: UI, API, service
  logic, permissions, jobs, dependencies, translations, docs, and dead tests.
- Prefer one supported installation/update path over parallel legacy paths.
- Builds and releases must be deterministic, pinned, checksum-verified, and
  multi-architecture only where RouterOS support is real and tested.
- The beginner installation and troubleshooting path is a product feature, not
  an afterthought.

## Upstream policy

- Preserve GPL-3.0 obligations, copyright notices, and honest attribution.
- Treat Mikrozia as an upstream reference, not an automatic source of truth.
- Never merge upstream wholesale into a release branch.
- Review upstream changes individually for RouterOS compatibility, data safety,
  resource cost, security, and conflict with Mikrozia decisions.
- Import selected security fixes, protocol improvements, and useful features
  deliberately, with Mikrozia tests and release notes.
- Track the upstream base separately from the independent Mikrozia version.

## Change workflow

1. Inspect the working tree and preserve unrelated/user-owned changes.
2. Read the current phase and accepted decisions in the charter.
3. Audit the full vertical slice before editing; do not patch only the symptom.
4. Make one bounded architectural change at a time.
5. Add migration and regression coverage before claiming data compatibility.
6. Regenerate OpenAPI, generated types, translations, and docs through the
   project tools when their sources change.
7. Run tests proportional to risk, including the complete relevant suites.
8. Distinguish code failures, pre-existing failures, and environment blockers.
9. Compare the final report with the actual diff and static searches.

Do not commit, push, tag, publish an image, create a release, or modify external
systems unless the user explicitly authorizes that action for the current task.

## Reporting standard

Never call a stage complete while a required test is still running, was killed,
or was replaced by a smaller selected suite. Reports must include exact commands,
exit codes, failures, environment blockers, static-search exceptions, and
`git status --short`. Claims must match the repository state.
