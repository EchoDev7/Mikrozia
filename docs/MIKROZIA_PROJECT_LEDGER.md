# Mikrozia Project Ledger

Status: living project-control record
Owner: Mikrozia maintainer
Task authority: delegated to the Mikrozia project manager unless the owner pauses or overrides a task
Last reconciled: 2026-11-20

This is the operational record for work performed after the Mikrozia charter.
It lets a new agent identify what is accepted, what is only implemented or
verified, and the one bounded task it may perform next. It does **not** replace
`AGENTS.md` or `docs/MIKROZIA_PROJECT_CHARTER.md`; those documents win on any
conflict.

## Status and approval rules

Every agent reads this file after the required files in `AGENTS.md` and before
planning or editing.

- `[ ]` — not started; this ledger entry alone does not authorize work.
- `[~]` — active or awaiting correction; do not start another task.
- `[?]` — reported and awaiting project-manager review.
- `[x]` — accepted after project-manager evidence review.

1. A model report, a green selected test, or a passing build never changes a
   task to `[x]` by itself. The project manager must review the evidence against
   the charter and actual diff first.
2. The project manager may activate, sequence, and accept bounded internal tasks
   without a separate owner confirmation. The owner may pause, override, or
   reprioritize work at any time.
3. An implementation agent updates this ledger only when its prompt explicitly
   authorizes that update. Otherwise it reports evidence and leaves task status
   unchanged.
4. Before every task, record the actual dirty-worktree baseline and preserve all
   pre-existing modifications, deletions, renames, and untracked files.
5. A task is not complete if any required test is skipped, killed, still
   running, blocked, or failed.
6. Each task is one bounded vertical slice with explicit tests, exclusions, and
   acceptance conditions.
7. Do not create or begin a new task while an existing task is active, unless
   the project manager closes, replaces, or explicitly pauses it.

External and difficult-to-reverse actions remain owner-authorized even when task
sequencing is delegated: commits, pushes, tags, releases, Docker Hub publishing,
changes to real RouterOS devices, credential rotation, and destructive data
operations.

## Product invariants

- RouterOS Container and CHR are primary production targets.
- RouterOS policy is operator-owned by default. In explicitly enabled Managed
  RouterOS Integration, Mikrozia may reconcile only its own marked inbound
  exposure objects through the configured RouterOS API; it never adopts or
  mutates unmarked user-owned objects.
- Images are immutable; updates replace the versioned image while preserving
  persistent data mounts.
- User data, settings, traffic, database state, and certificates survive image
  replacement and rollback and are never silently reset or weakened.
- SQLite is the default database. PostgreSQL is optional and external; Mikrozia
  never installs a local PostgreSQL server inside RouterOS Container.
- No commit, push, tag, release, Docker Hub publication, or external-system
  change occurs without current-task owner authorization.

## Dirty-worktree baseline

The repository intentionally contains substantial user-owned work. It is never
permission to discard, reformat, regenerate, or rewrite unrelated files.

| Recorded on | `git status --short` entries | `git diff --stat`                                             | Notes                                                                                                                                                                                                                                                                                                                                                |
| ----------- | ---------------------------: | ------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 2026-11-14  |                          271 | 213 files; 6,395 insertions; 12,747 deletions                 | Baseline before PostgreSQL 2C3E. Re-record before each task; numbers may become stale.                                                                                                                                                                                                                                                               |
| 2026-11-17  |                            6 | 5 files; +248/−31, plus pre-existing user edits in the ledger | Baseline before P1-AUTH-ACME-01 at HEAD `28876c06`: `docs/MIKROZIA_CHR_INSTALL_FA.md`, `docs/MIKROZIA_PROJECT_LEDGER.md`, `docs/content/docs/fa/guide/installation.mdx`, `docs/content/docs/fa/operations/backup-restore.mdx`, `docs/content/docs/fa/reference/ports-firewall.mdx`, `tests/test_docs_contract.py`. All are user-owned and preserved. |
| 2026-11-17  |                          101 | 90 files; +3,221/−1,105, plus 11 new files                    | End state of P1-AUTH-ACME-01 at HEAD `28876c06`. The 6 pre-existing user-owned entries above are still present and were extended in place, never reverted. The other 95 entries are the credential, ACME, documentation, and regression work of this task.                                                                                           |
| 2026-11-18  |                            0 | clean worktree                                                | Release `v1.0.1-mikrozia` is committed at `6b7e017735be1becabe7d70b74c0b1f0f06f06de`, tagged, pushed, and published as a multi-architecture OCI image.                                                                                                                                                                                               |
| 2026-11-20  |                          111 | 85 files; +3,390/−910 (approx., plus untracked files)         | Local release-candidate preparation for `v1.0.2-mikrozia` at HEAD `6b7e017735be1becabe7d70b74c0b1f0f06f06de`: Dashboard & RouterOS API telemetry work, OpenAPI/docs sync, test-race/lint closure (`P1-RC-01`..`P1-RC-09`), and local bump of `MIKROZIA_VERSION` to `v1.0.2-mikrozia`. No commit, tag, push, publish, or CHR change yet.              |

## Accepted product decisions

These charter decisions are accepted and must not be reversed accidentally.

- [x] RouterOS firewall and NAT policy remain user-managed by default. Explicit
      Managed RouterOS Integration may automatically reconcile only marked
      Mikrozia-owned inbound exposure objects through the configured RouterOS API.
- [x] User IP-limit data is not silently cleared when host enforcement is unavailable.
- [x] Legacy in-place panel self-update is unsupported; releases are versioned images.
- [x] Xray and sidecars are image build-time components.
- [x] AmneziaWG uses the embedded userspace engine only; kernel mode is removed.
- [x] Host-kernel BBR controls are removed; userspace protocol BBR remains.
- [x] Version display is read-only until an explicitly approved RouterOS-aware updater exists.
- [x] RouterOS logging is user-managed; the panel does not depend on systemd or `journalctl`.
- [x] The administrator account is owned by the RouterOS Container Environment
      (`MIKROZIA_ADMIN_USERNAME` / `MIKROZIA_ADMIN_PASSWORD`): no default `admin/admin`,
      no random first-login credential in logs, a supplied pair is reconciled on every
      start, a half-supplied pair fails closed, an existing account without the
      variables is preserved, a first install without them refuses to start, and the
      panel-UI and legacy shell/CLI password mutation paths are retired in RouterOS
      mode. Plaintext credentials never reach logs, API responses, diagnostics, docs,
      image layers, or tests.
- [x] Certificate issuance and renewal are supported again with exactly two
      official methods: HTTP-01 manual (operator-owned temporary port 80) and
      Cloudflare DNS-01 automatic (least-privilege API token). Mikrozia creates no
      RouterOS policy rule in either method and never exports a RouterOS private key
      into the container.
- [x] ACME artifacts are persistent under `/root/cert` (account state in
      `/root/cert/.acme.sh/`, certificate at `/root/cert/<domain>/fullchain.pem` and
      `/root/cert/<domain>/privkey.pem`); validated issuance updates the panel and
      subscription TLS paths automatically, reloads only what changed, and preserves
      the previous certificate and settings on failure. The manual certificate-path
      fields are retained and the RouterOS reverse-proxy workflow is retired from
      current guidance while live installations stay untouched.
- [x] Any bundled ACME tool is a pinned, checksum-verified image build input; no
      dynamic `curl | sh` installer, runtime package installation, unpinned download,
      or unmanaged cron daemon is reintroduced. Full contract:
      `docs/MIKROZIA_AUTH_ACME_SPEC.md`.
- [x] RouterOS observability and Managed RouterOS Integration use direct internal
      native RouterOS `api` on TCP/8728, restricted to the container-router
      network. The owner accepts its plaintext credential transport for this
      isolated path. The technical reference is
      `docs/MIKROZIA_ROUTEROS_API_INTEGRATION.md`.
- [x] Dashboard review precedes Dashboard implementation. No visible Dashboard
      control, RouterOS API client, metric source, or RouterOS setting changes until
      the owner accepts a bounded Dashboard report and implementation scope.

## Task register

| ID                | Status | Bounded task                                                                                                          | Evidence / current result                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Owner approval condition                                                                                                                                                                                              |
| ----------------- | ------ | --------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GOV-01            | [x]    | Establish Mikrozia charter and repository governance                                                                  | `AGENTS.md` and the project charter define the RouterOS contract and decision hierarchy.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | Charter accepted by owner.                                                                                                                                                                                            |
| P0-01             | [x]    | Retire inherited host-only RouterOS controls                                                                          | Charter records removal of kernel AmneziaWG, host-kernel BBR, in-place self-update, and systemd/syslog assumptions.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Existing charter decisions are accepted; do not reverse without a new task.                                                                                                                                           |
| PG-2C1-2C3D       | [x]    | PostgreSQL connection, migration, backup/restore, snapshot, and rollback implementation                               | Accepted with PG-2C3E on 2026-11-15 after live PostgreSQL 18, package, race, shuffle, lint, docs, and cleanup evidence was reviewed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 | Accepted by owner.                                                                                                                                                                                                    |
| PG-2C3E           | [x]    | PostgreSQL 18 verification and documentation closure gate                                                             | Accepted 2026-11-15. Live PostgreSQL 18 tests had no skips; relevant package, race, shuffle, lint, diff, documentation, static, and temporary-resource cleanup gates passed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                         | Accepted by owner.                                                                                                                                                                                                    |
| P0-CLOSURE-01     | [x]    | Read-only Phase 0 RouterOS compatibility and safety closure audit                                                     | Accepted 2026-11-15. Audit found Phase 0 not ready and identified bounded corrective tasks; this accepts the audit, not Phase 0 closure.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | Accepted by owner.                                                                                                                                                                                                    |
| P0-F2B-01         | [x]    | Retire unsupported Fail2ban/IP-limit enforcement controls without losing stored user data                             | Accepted 2026-11-15 after legacy SQLite/PostgreSQL preservation, Go, frontend, i18n, OpenAPI, docs, shell, Python, lint, and static verification passed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | Accepted after project-manager review.                                                                                                                                                                                |
| P0-VER-01         | [x]    | Establish one canonical Mikrozia version source across runtime, image metadata, workflows, docs, and release tooling  | Accepted 2026-11-15 after strict byte-exact version validation, runtime/image/workflow consistency tests, and project-manager review.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-GEO-01         | [x]    | Make bundled Geo data release inputs deterministic, pinned, checksum-verified, and RouterOS-safe                      | Accepted 2026-11-15 after pinned manifest, checksum, immutable-runtime, UI/API, data-preservation, and focused verification review.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-RT-01          | [x]    | Retire or safely adapt remaining runtime executable-download and RootFS-mutation paths                                | Accepted 2026-11-15 after immutable entrypoint, ACME/speed-test retirement, certificate-path preservation, and project-manager verification.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-REL-01         | [x]    | Reconcile release workflows, artifacts, architecture claims, and supply-chain evidence with the RouterOS OCI contract | Accepted 2026-11-15 after legacy binary-release retirement, OCI-only publish contract, architecture smoke matrix, and project-manager review.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-DOC-01         | [x]    | Align all current installation, update, and operations documentation with the RouterOS-only product contract          | Accepted 2026-11-16 after direct-terminal installation guidance, localization restoration, historical labelling, docs gates, and project-manager review.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-BACKUP-01      | [x]    | Close RouterOS backup, restore, image-replacement, and rollback contract gaps                                         | Accepted 2026-11-16 after WAL-safe SQLite backup correction, atomic restore/dump handling, PostgreSQL schema-scope review, localized lifecycle documentation, and regression evidence.                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-CLOSURE-02     | [x]    | Final read-only Phase 0 RouterOS compatibility and safety closure audit                                               | Accepted 2026-11-16. Audit found Phase 0 not ready: active host installer and executable `.rsc` deployment paths conflict with the one supported direct-terminal installation path.                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Accepted audit; it does not close Phase 0.                                                                                                                                                                            |
| P0-INSTALL-01     | [x]    | Retire executable legacy host and `.rsc` installation paths, leaving one RouterOS direct-terminal installation path   | Accepted 2026-11-16 after host installer, cloud-init, `.rsc`, generator, related docs/site tools, tests, and workflow references were retired with regression coverage.                                                                                                                                                                                                                                                                                                                                                                                                                                                                              | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-CLOSURE-03     | [x]    | Final read-only Phase 0 closure re-audit after legacy installation retirement                                         | Accepted 2026-11-16. Audit found Phase 0 not ready: exposed docs firewall-policy generator remains in the published MDX registry.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Accepted audit; it does not close Phase 0.                                                                                                                                                                            |
| P0-POLICY-TOOL-01 | [x]    | Retire exposed host-firewall policy generator from the published documentation bundle                                 | Accepted 2026-11-16 after component, helper, helper tests, MDX registration, and active identifiers were removed with regression coverage.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-CLOSURE-04     | [x]    | Final Phase 0 read-only closure decision after all bounded corrective tasks                                           | Accepted 2026-11-16. Audit found Phase 0 not ready: live Docker Hub overview contains RouterOS firewall/NAT commands and OpenRC host-service residue remains.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        | Accepted audit; it does not close Phase 0.                                                                                                                                                                            |
| P0-RESIDUE-01     | [x]    | Retire current RouterOS policy-command documentation and final OpenRC host-service residue                            | Accepted 2026-11-16 after overview policy commands were replaced with operator-owned inputs and `x-ui.rc` vertical slice was removed with regression coverage.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       | Accepted after project-manager evidence review.                                                                                                                                                                       |
| P0-CLOSURE-05     | [x]    | Final read-only Phase 0 closure confirmation after policy and host-residue removal                                    | Accepted 2026-11-16. Source contract is aligned: no unsupported control remains exposed; real-device evidence is explicitly deferred to P1.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Phase 0 closed by project-manager evidence review.                                                                                                                                                                    |
| P1-CHR-01         | [~]    | Clean-CHR beginner installation, validation, update, rollback, backup, restore, and troubleshooting acceptance test   | `v1-mikrozia` was accepted on clean CHR x86_64 RouterOS 7.25.1. Release `v1.0.1-mikrozia` was then pulled and run on CHR x86_64 (RouterOS/container `7.25.1`): `linux/amd64` image pull/extract, start/restart, internal reachability, persistent `/etc/x-ui` and `/root/cert` mounts, environment-owned administrator login, externally verified panel TLS, Subscription, and an Xray inbound passed. **Remaining, owner-deferred acceptance gates:** live SQLite backup/restore with deliberate disposable data; rollback from `v1.0.1-mikrozia` to a distinct compatible published tag and return, proving database and certificate preservation. | Owner authorized real-device actions and later explicitly authorized operator-owned panel/Subscription/inbound rules; Mikrozia itself did not alter policy.                                                           |
| P1-AUTH-ACME-01   | [x]    | Environment-authoritative administrator bootstrap and RouterOS-native persistent ACME                                 | Accepted 2026-11-18 after source-contract review, race/container/docs gates, commit `6b7e0177`, multi-arch publication `majlot/mikrozia:v1.0.1-mikrozia`, and real CHR validation. Environment-owned login was verified; HTTP-01 issuance for `soon.parsiserver.com` stored artifacts on `/root/cert`, reconciled TLS paths, reloaded services, removed the temporary operator-owned port-80 rule, and served a publicly valid Let's Encrypt certificate.                                                                                                                                                                                            | Accepted by project-manager evidence review. HTTP-01 renewal remains an operator-run procedure; a live Cloudflare DNS-01 token/renewal exercise is optional future operational coverage, not a blocker for this task. |
| P1-REL-AUTO-01    | [ ]    | Restore GitHub Actions Docker Hub publishing for future immutable releases                                            | `v1.0.1-mikrozia` was successfully published directly after the workflow's Validate and three architecture smoke jobs passed, but its Publish job failed because repository secrets `DOCKER_HUB_USERNAME` and `DOCKER_HUB_TOKEN` were absent. No next release may rely on an unverified workflow publication path.                                                                                                                                                                                                                                                                                                                                   | Owner must add/rotate least-privilege Docker Hub credentials as GitHub Actions secrets; then a dedicated release-workflow dry run/release must verify publication.                                                    |
| P1-RC-01..09      | [~]    | Release readiness, test-race/lint closure, and multi-arch local OCI verification for `v1.0.2-mikrozia`                | Verified locally on 2026-11-20 before version bump: OpenAPI/docs synced, Charter formatted, MTProto symlink & Discord heartbeat test races resolved, `golangci-lint v2.15.0` `0 issues.`, `make test-fe` (`225/225`, `2062/2062`), 2× `make test-go`, `make race`, docs (`104/104`, `275` pages), local `amd64` image + `118/118` Python suite, and `arm64/v8` + `arm/v7` OCI exports all exited `0`. `MIKROZIA_VERSION` bumped locally to `v1.0.2-mikrozia`.                                                                                                                                                                                        | Awaiting explicit owner authorization for commit, tag, push, Docker Hub publish, and CHR deployment.                                                                                                                  |
| P2-IDENTITY-01    | [ ]    | Mikrozia identity, branding, independent versioning, and release ownership                                            | Not authorized. No broad rename or Go module-path migration before a stable RouterOS baseline.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       | Owner authorizes a dedicated migration plan.                                                                                                                                                                          |

## Most recently accepted task: PG-2C3E

### Goal

Finish PostgreSQL 18 verification and documentation closure without expanding
product scope. The owner accepted this task on 2026-11-15 after all listed
acceptance work and verification gates passed.

### Verified evidence

- PostgreSQL 18 server and PostgreSQL 18 client tools ran in temporary Docker
  containers without host package installation.
- These tests ran and passed without skipping:
  - `TestLivePostgres_URI_Keyword_AutoMigrate_Ping`
  - `TestLivePostgres_ResolveCanonicalSchema`
  - `TestExportPostgresToSQLiteContextLivePG18`
  - `TestLivePostgres_PreSnapshotAndExactPostCommitRollback`
  - `TestLivePostgres_SQLiteMigrationHostSettingsParity`
- Related `XUI_TEST_PG_DSN` tests passed in that run.
- PostgreSQL backup/restore documentation was updated in English, Persian,
  Russian, and Chinese; documentation checks and `golangci-lint v2.15.0` passed.

### Accepted completion evidence

1. The process-group timeout, PostgreSQL lock/cancellation, Controller import,
   and PostgreSQL backup-stream tests were made deterministic and passed.
2. `CloseDB` now drops a successfully closed global handle; a regression test
   proves stale PostgreSQL dialect state cannot bypass SQLite recovery.
3. Relevant database, service, and Controller package tests passed with the
   PostgreSQL 18 DSN.
4. Relevant race tests passed with no data races; shuffled tests passed with
   seed `20261115`.
5. Mandatory PostgreSQL 18 live tests and related `XUI_TEST_PG_DSN` tests ran
   without skips.
6. `golangci-lint v2.15.0`, `git diff --check`, documentation checks, static
   checks, and temporary Docker-resource cleanup all passed.

### Explicit exclusions

- No host-level package installation.
- No CI workflow, Dockerfile, docker-compose, or release-workflow change.
- No PostgreSQL 15/16 validation.
- No Mikrozia image or multi-architecture build.
- No RouterOS configuration change.
- No commit, push, tag, release, or Docker Hub publication.

### Owner approval record

| Field                  | Value                                                                |
| ---------------------- | -------------------------------------------------------------------- |
| Candidate report       | PostgreSQL 2C3E completion report reviewed after its correction run. |
| Current owner decision | Accepted on 2026-11-15.                                              |
| Approval evidence      | Explicit owner message: `تأیید PG-2C3E`.                             |
| Next action            | P0-F2B-01 is active.                                                 |

## Most recently accepted audit: P0-CLOSURE-01

### Goal

Perform a read-only closure audit of the Phase 0 RouterOS compatibility and
safety baseline. The owner accepted this audit on 2026-11-15. It found that
Phase 0 remains open and requires bounded corrective tasks before real-CHR
acceptance work begins.

### Scope

- Read-only inspection only: no source, documentation, generated-output,
  configuration, workflow, or external-system change.
- Trace all established Phase 0 decisions through their complete vertical slices.
- Reconcile the current dirty worktree rather than relying on earlier reports.
- Identify proven compliance, missing evidence, stale controls, conflicting
  documentation, test gaps, and environment blockers.
- Produce a prioritized, bounded correction plan if Phase 0 cannot close.

### Required findings

The audit must cover at least:

1. RouterOS network-policy non-mutation.
2. Immutable image and no in-place panel/Xray update path.
3. Persistent mounts, backup, restore, update, and rollback safety.
4. Retired host-only behavior: kernel AmneziaWG, host-kernel BBR, systemd,
   `journalctl`, Fail2ban/IP-limit enforcement, and local PostgreSQL install.
5. UI, API, service, jobs, translations, permissions, and docs consistency for
   every retired or adapted feature.
6. Current version display and supported image-release workflow.
7. Build input pinning, checksum verification, architecture claims, and
   multi-architecture evidence.
8. Beginner documentation and the remaining prerequisites for P1-CHR-01.
9. Test coverage and exact gates needed before claiming Phase 0 closure.

### Owner approval record

| Field                  | Value                                                                             |
| ---------------------- | --------------------------------------------------------------------------------- |
| Current owner decision | Audit accepted on 2026-11-15; Phase 0 itself is not closed.                       |
| Allowed mutation       | None. This is a read-only task.                                                   |
| Completion condition   | Evidence-based report reviewed by owner. The audit itself does not close Phase 0. |
| Next action            | P0-F2B-01 is active.                                                              |

## Most recently accepted task: P0-F2B-01

### Goal

Remove public Fail2ban/IP-limit enforcement controls that cannot work in the
RouterOS Container contract, while preserving all existing user data and legacy
compatibility fields. This task was accepted on 2026-11-15 after its complete
vertical-slice and preservation verification passed.

### Non-negotiable data rule

Never clear, zero, delete, migrate away, or reinterpret stored `limitIp` values
or related client data. Existing data must remain intact through startup,
backup, restore, migration, image replacement, and rollback.

### Scope

- Audit and remove or truthfully retire the complete unsupported public slice:
  UI, API routes/status, frontend queries, shell menus/messages, translations,
  docs, Compose assumptions, tests, and dead dependencies.
- Preserve private legacy storage and import/export compatibility where required
  for data safety.
- Ensure new users cannot configure an enforcement control that cannot succeed
  on RouterOS Container.
- Keep RouterOS firewall/NAT/routing policy user-managed.

### Accepted evidence

- No Fail2ban install, status, enforcement, or remediation path remains exposed
  as a supported Mikrozia feature.
- No `XUI_ENABLE_FAIL2BAN`, Fail2ban capability, or Fail2ban-only
  `NET_ADMIN`/`NET_RAW` requirement remains in the RouterOS contract.
- Legacy `limitIp` data is proven unchanged by SQLite and live PostgreSQL tests.
- UI/API/docs/translations/OpenAPI/tests agree with the retired behavior.

### Explicit exclusions

- No database data deletion or destructive migration.
- No replacement IP-limit enforcement architecture in this task.
- No RouterOS firewall, NAT, address-list, or policy-routing automation.
- No unrelated rootfs, Geo, ACME, release-pipeline, or broad documentation work.
- No commit, push, tag, release, or Docker Hub publication.

## Most recently accepted task: P0-VER-01

### Goal

Establish one canonical Mikrozia version source for runtime display, image
labels, build arguments, workflows, documentation, and release tooling. The
task must remove version drift and stale fallback values without changing the
currently intended Mikrozia release version.

### Scope

- Trace `MIKROZIA_VERSION`, `defaultMikroziaVersion`, ldflags, image labels,
  build arguments, workflow tags, version documentation, and release scripts.
- Make one value authoritative and derive all other consumers deterministically.
- Add regression coverage proving a build cannot silently display a stale
  fallback version when release metadata says otherwise.
- Preserve upstream base/version metadata separately from Mikrozia public
  identity.

### Explicit exclusions

- No version bump or release publication.
- No Docker Hub, tag, commit, or external-system action.
- No broad Phase 2 branding/module-path migration.
- No unrelated supply-chain, Geo, ACME, documentation, or architecture work.

### Accepted completion evidence

- `MIKROZIA_VERSION` is the sole editable release input and carries the selected
  independent release tag `v1-mikrozia`.
- Runtime panel/API/CLI display returns only a valid ldflags-injected Mikrozia
  version; absent, malformed, whitespace-padded, or multiline input truthfully
  displays `dev` rather than a stale production version.
- The version validator performs byte-exact comparison with the canonical file,
  rejects all whitespace and multiline inputs, and is used by the Docker build
  and supported Docker publish workflow.
- Docker OCI label, runtime `x-ui -v`, image build argument, and release tag
  are tested to agree before the multi-architecture publish job.
- Generator and static RouterOS installer pin remain derived from or checked
  against the canonical version. Upstream 3x-ui base metadata remains separate.
- Project-manager independent checks reproduced the malformed-input rejection,
  relevant Go/Python suites, shell syntax, and `git diff --check`. Workflow
  lint could not be re-run locally because the network could not resolve the
  Go module proxy; this is recorded as an environment blocker, not a pass.

## Most recently accepted task: P0-GEO-01

### Goal

Make every Geo data artifact shipped in a Mikrozia RouterOS image a
deterministic, pinned, checksum-verified build input. Retire or truthfully
restrict any runtime Geo executable/download/update behavior that violates the
immutable-image contract, while preserving user configuration and useful
RouterOS-supported routing features.

### Required audit and scope

- Trace the entire vertical slice for `geoip.dat`, `geosite.dat`, Iranian Geo
  variants, Xray geodata, panel Geo browsing, update jobs/buttons/API/CLI,
  Docker and release downloads, workflows, checksums, tests, translations, and
  documentation.
- First classify each Geo feature as build-time input, immutable bundled
  runtime data, user-persistent data, RouterOS-safe browsing/configuration, or
  unsupported runtime executable mutation.
- Preserve routing settings and user data. Do not delete a useful Geo-based
  feature unless it is proven unsafe or impossible under the RouterOS contract.
- For retained downloaded artifacts, pin an exact upstream release/reference
  and verify a reviewed checksum before extraction or use. Never use a mutable
  `latest` URL as a production image input.
- If the inherited in-panel update mechanism rewrites RootFS or downloads
  executable/data artifacts at runtime, remove its complete unsupported public
  slice or adapt it only to a documented persistent-data contract that does not
  violate immutability.

### Explicit exclusions

- No version bump, commit, push, tag, release, Docker Hub publication, or real
  RouterOS action.
- No broad release-pipeline redesign beyond the Geo input path.
- No unrelated ACME, PostgreSQL, protocol, branding, or documentation cleanup.

### Accepted completion evidence

- The six Geo databases used by the RouterOS OCI image are sourced only from
  exact date-tagged releases in `GEO_DATA_SOURCES`, each with a recorded
  SHA-256 verified during image construction.
- Docker build code rejects mutable `latest` Geo URLs, malformed manifest rows,
  unexpected artifact names, and checksum failures. The manifest is included
  in the resulting image and the supported image-smoke workflow verifies every
  bundled artifact against it.
- The mutable panel/API/CLI/frontend Geo updater vertical slice is removed;
  read-only Geo browsing and `geoip:`/`geosite:` routing support remain.
- Stored templates and routing data are preserved. The Xray-native `geodata`
  downloader block is retained in storage but intentionally omitted from the
  RouterOS runtime configuration because it would mutate Geo data at runtime.
- Project-manager checks passed for shell syntax, Geo and container Python
  tests, focused Go service/controller tests, and `git diff --check`.
  `docker build --check` could not run locally because the Docker socket was
  unavailable; that is an environment blocker, not a pass.
- The inherited non-container binary-release workflow still has mutable Geo
  `latest` downloads. It does not build the supported RouterOS OCI image and
  is explicitly deferred to the dedicated P0-REL release-pipeline task.

## Most recently accepted task: P0-RT-01

### Goal

Audit and retire or safely adapt every remaining runtime path that downloads,
installs, upgrades, replaces, or executes mutable software from inside a
Mikrozia container. The resulting RouterOS runtime must honor immutable image
contents while retaining safe certificate and user-data operations.

### Required audit and scope

- Trace all executable, script, package-manager, curl/wget, acme, speed-test,
  Xray, sidecar, panel, database-client, and shell-menu paths that can download
  or replace software after image build.
- Classify each path as immutable build input, safe persistent user-data
  operation, unsupported RootFS/software mutation, or inherited out-of-scope
  release tooling.
- Preserve certificates, database state, configuration, and other user data.
  Do not confuse ACME account/certificate renewal with executable self-update.
- Remove an unsupported control only as a complete vertical slice, or adapt it
  only when it can operate without downloading/replacing executable software
  and without mutating the immutable RootFS.

### Explicit exclusions

- No image release, Docker Hub publication, commit, push, tag, external-system
  action, or real RouterOS change.
- No broad release-pipeline work, Geo changes, protocol redesign, or branding.
- No silent user-data deletion, certificate deletion, or database migration.

### Accepted completion evidence

- `DockerEntrypoint.sh` is a pure command dispatcher with no runtime
  filesystem setup or mutation; `HOME=/root` is supplied at image build time.
- Container-side ACME installation, certificate issuance, renewal scheduling,
  ACME state migration, and speed-test/package installation paths are removed.
- TLS certificate path configuration, mounted certificate files, database
  tools, backups, restores, and local panel settings remain intact.
- Current certificate documentation says acquisition and renewal are
  user-managed outside the container. Historical field records remain clearly
  marked as superseded rather than being presented as installation guidance.
- Project-manager checks reproduced entrypoint syntax, custom-command dispatch,
  runtime-immutability/container tests, and `git diff --check`.

## Most recently accepted task: P0-REL-01

### Goal

Make the supported Mikrozia release path unambiguous, deterministic, and
limited to RouterOS-supported OCI artifacts. Reconcile inherited GitHub release
workflows, release assets, architecture claims, tags, version metadata, build
inputs, checksum/provenance evidence, and documentation with that contract.

### Required audit and scope

- Trace all GitHub workflows, release scripts, Docker build/publish paths,
  release artifacts, image tags, architecture matrices, build inputs, checksum
  steps, SBOM/provenance settings, and user-facing release documentation.
- Classify each workflow/artifact as supported RouterOS OCI release path,
  test-only path, intentionally retained developer tooling, or unsupported
  inherited legacy distribution path.
- Remove or truthfully retire any inherited production workflow that publishes
  mutable/unverified artifacts, claims unsupported RouterOS architectures, or
  can be mistaken for the Mikrozia release path.
- Keep one canonical versioned-image update interface. Do not publish, tag, or
  create a release in this task.

### Explicit exclusions

- No Docker Hub publication, GitHub release, push, tag, commit, credential
  action, or real RouterOS action.
- No broad branding/module-path migration, Geo data redesign, protocol work, or
  unrelated documentation cleanup.
- No deletion or mutation of user runtime data.

### Accepted completion evidence

- `.github/workflows/docker.yml` is the only supported production publisher:
  it accepts only canonical Mikrozia tags or an exact manual dispatch, validates
  version/build inputs, smoke-tests every published architecture, and publishes
  one multi-architecture OCI image with SBOM, provenance, labels, and digest.
- The inherited broad-tag binary-release workflow, Windows packaging assets,
  systemd unit artifacts, mutable Geo downloads, dynamic toolchain discovery,
  and seven-architecture tarball release path are retired.
- The supported platform set is explicit and tested: `linux/amd64`,
  `linux/arm64/v8`, and `linux/arm/v7`. Each has an architecture-specific
  smoke build and runtime contract check before publication.
- Project-manager checks reproduced the release-pipeline/static tests and
  `git diff --check`. A fresh local actionlint run was blocked by DNS access to
  the Go module proxy; the candidate report's completed lint run is retained as
  evidence, while the local blocker is not counted as a new pass.

## Most recently accepted task: P0-DOC-01

### Goal

Bring all current user-facing Mikrozia installation, update, operations, and
troubleshooting documentation into agreement with the RouterOS-only contract,
without rewriting clearly labelled historical records or beginning Phase 2
branding work.

### Required audit and scope

- Inventory every README, localized README, docs-site page, install/update
  guide, Docker Hub overview, script comment, and generated help path that a
  user could follow to install, update, issue certificates, configure firewall,
  use unsupported host services, or obtain legacy artifacts.
- Classify each as current supported guidance, historical evidence that needs a
  conspicuous superseded label, generated reference, or unrelated upstream
  material to remove/retire.
- Ensure one supported path is discoverable: versioned OCI image on RouterOS,
  persistent mounts, user-managed network policy and certificates, and no
  runtime software updater.
- Preserve translations and generated-file ownership. Do not change product
  behavior in this documentation-only task.

### Explicit exclusions

- No code/runtime/workflow/configuration change except documentation generators
  and generated documentation outputs when required by their source changes.
- No commit, push, tag, release, Docker Hub publication, credential action, or
  real RouterOS action.
- No Phase 2 visual rebrand, module-path migration, or broad prose rewrite
  unrelated to supported RouterOS guidance.

### Accepted completion evidence

- All current beginner guides now use ordered, copy-pasteable RouterOS terminal
  commands rather than importing an `.rsc` script. They explain the versioned
  image, persistent mounts, user-owned network policy, and the uncompleted
  P1 clean-CHR acceptance honestly.
- The prior unauthorized deletion of localized API-reference pages was reversed:
  Persian, Russian, and Chinese each retain the complete 13-page API inventory
  plus metadata, with regression coverage for that inventory.
- Current documentation no longer directs users to legacy installers, tarballs,
  systemd, production Compose, in-container ACME, runtime updates, or retired
  API endpoints. Historical records are conspicuously labelled superseded.
- Project-manager checks reproduced the documentation contract test plus docs
  format, lint, typecheck, and test gates. A second local docs build was blocked
  by a concurrent/stale Next build lock after the candidate's successful full
  build; the lock is an environment condition, not treated as a new pass.

## Most recently accepted task: P0-BACKUP-01

### Goal

Verify and close the complete RouterOS persistent-data lifecycle: backup,
restore, image replacement, rollback, SQLite/PostgreSQL behavior, certificates,
settings, and user traffic/client data. The supported workflow must be safe for
beginners before the Phase 0 closure audit.

### Required audit and scope

- Trace all backup/export/import/restore/migration/snapshot/rollback code,
  controller routes, CLI functions, UI, documentation, persistent mounts,
  temporary files, database backends, and image-replacement instructions.
- Prove that image replacement and rollback preserve `/etc/x-ui` and
  `/root/cert` without mutating the image RootFS or losing user data.
- Identify any restore operation that can silently overwrite, truncate, or
  reinterpret data; make it explicit, recoverable, and tested.
- Reconcile the PostgreSQL implementation with RouterOS external-PostgreSQL
  constraints and ensure secrets are never leaked by backups, CLI output, or
  documentation.

### Explicit exclusions

- No destructive backup/restore action against user data or a real RouterOS
  device.
- No Docker Hub publication, tag, release, commit, push, credential action, or
  external-system change.
- No unrelated protocol, release-pipeline, branding, or broad documentation
  work.
- No deletion, silent reset, or reinterpretation of stored user routing data.

### Accepted completion evidence

- SQLite logical dumps use one pinned read transaction and are proven against
  concurrent WAL writers, so a dump cannot combine values from different
  committed states.
- Shell-driven SQLite dump and restore stage output beside the requested target
  and replace it only after success; failed operations leave prior output and
  SQLite sidecars intact.
- PostgreSQL dump, restore, and rollback use an exact quoted canonical-schema
  pattern and reject invalid scope instead of widening a restore unexpectedly.
- Current English, Persian, Russian, and Chinese documentation no longer tells
  users to copy a live `x-ui.db` file. It uses a consistent logical dump for a
  running panel and explicitly requires a stopped container for raw database
  file replacement. Certificate backup is documented separately.
- Project-manager review reproduced the four-locale documentation contract
  suite (15 tests) and `git diff --check`. The candidate report additionally
  records database, race, service, translation, shell, and documentation gates.
  A local full service/controller Go run was blocked by the sandbox forbidding
  IPv6 loopback listener binding; this environment limitation is not counted as
  a test pass.

## Most recently accepted audit: P0-CLOSURE-02

### Goal

Perform one final read-only reconciliation of the accepted Phase 0 work against
the Mikrozia charter, current worktree, and RouterOS Container contract. Decide
from evidence whether Phase 0 may close and P1 clean-CHR acceptance may begin,
or identify the smallest necessary corrective task.

### Scope and exclusions

- Inspect only: no source, test, documentation, workflow, generated-artifact,
  or ledger edit was authorized.
- Cover RouterOS policy non-mutation; immutable runtime; data mounts; update,
  rollback, backup, and restore; all retired host-only features; userspace
  features retained; versions/releases/Geo inputs; current beginner docs; and
  test/build/CHR evidence gaps.
- Reconcile actual code and static searches, not earlier reports alone.
- Do not claim real CHR, Docker Hub, publication, or multi-architecture
  acceptance without direct evidence. Do not modify real RouterOS or external
  systems.

### Accepted audit result

- Phase 0 is not ready to close. `install.sh` labels itself unsupported but
  still calls host-install functions unconditionally and can install an upstream
  systemd-host deployment.
- Executable generator and clean-room `.rsc` deployment paths remain alongside
  the supported direct-terminal route. The owner explicitly chose direct
  step-by-step terminal commands rather than `.rsc` import, so those paths need
  a complete retirement decision rather than documentation hiding.
- The audit independently recorded 133 passing Python compatibility tests and
  `git diff --check`; it did not claim Go, Docker, real CHR, publication, or
  architecture-runtime acceptance that it did not run.

## Most recently accepted task: P0-INSTALL-01

### Goal

Retire every executable legacy installation path that can install 3x-ui on a
systemd host or instruct RouterOS to import a generated `.rsc` deployment,
while preserving the one supported beginner path: ordered direct RouterOS
terminal commands in current Mikrozia documentation.

### Scope and exclusions

- Audit the full vertical slices for `install.sh`, `deploy/` host-install
  artifacts and smoke tests, generator-created deploy/teardown scripts, static
  clean-room `.rsc` scripts, their tests, workflow references, current docs,
  and historical records.
- Remove an unsupported executable entrypoint or replace it with a clear,
  non-bypassable refusal only where keeping the file is required for licensing
  or historical-source preservation. Remove dead tests and dependencies with
  the retired behavior.
- Preserve clearly labelled historical attribution/evidence where useful, but
  it must not be executable or presented as a supported workflow.
- Do not replace the retired paths with RouterOS automation, firewall/NAT
  mutation, a new installer, or an external service. Do not alter runtime
  protocol behavior, data mounts, release publication, or user data.

### Accepted completion evidence

- The executable host installer, cloud-init deployment assets, and host smoke
  path are removed rather than merely labelled unsupported.
- The `.rsc` generator, static RouterOS deploy/preflight/teardown scripts, and
  their direct tests/references are removed. Current beginner documentation
  retains the direct-terminal RouterOS method in all supported locales.
- The docs landing-page curl installer components and their dead support code
  are removed; historical upstream material remains conspicuously labelled.
- Project-manager review reproduced 93 focused Python compatibility tests with
  no failures. The candidate report additionally records full Python, shell,
  documentation format/lint/typecheck/test/build, YAML, and diff gates.

## Most recently accepted audit: P0-CLOSURE-03

### Goal

Perform a final read-only Phase 0 closure audit after retirement of all active
legacy installation paths. Determine strictly from current evidence whether
Phase 0 can close and P1 clean-CHR acceptance can begin.

### Scope and exclusions

- Read-only: do not edit source, tests, docs, workflows, generated artifacts,
  or this ledger.
- Repeat the Phase 0 contract audit against actual current code and inspect
  every remaining exposed or dead RouterOS policy tool, especially the
  documentation-component registry and firewall-rule generator.
- Reconcile persistent-data safety, immutable runtime, retired host controls,
  version/release/Geo policy, installation path, and exact test/CHR evidence.
- Do not claim Docker, real RouterOS/CHR, Docker Hub, or published-image proof
  without direct evidence and do not modify external systems.

### Accepted audit result

- Phase 0 is not ready to close. `FirewallRulesGenerator` is registered in the
  published MDX component registry even though no current MDX page uses it.
  Its helper generates unsupported Linux-host `ufw` and destructive `nftables`
  policy, contradicting the RouterOS operator-owned network-policy contract.
- No active RouterOS firewall/NAT/route generator was found after P0-INSTALL-01;
  this is a bounded dead-but-exposed documentation tool, not a runtime RouterOS
  mutation path.
- The audit independently reproduced full Python tests, targeted Go build/tests,
  docs/frontend tests, linters, and diff integrity. It properly left real CHR,
  image publication, and Docker Hub evidence to later acceptance work.

## Most recently accepted task: P0-POLICY-TOOL-01

### Goal

Remove the exposed host-firewall policy generator and its complete dead vertical
slice from the documentation bundle, leaving only operator-owned explanatory
guidance about RouterOS network policy.

### Scope and exclusions

- Remove the component, its firewall helper and helper test, and its MDX
  registry import/registration. Remove only related dead tests and references.
- Add regression coverage that the retired paths and identifiers are absent
  while current ports/security documentation keeps the operator-owned policy
  statement.
- Do not add a replacement firewall generator or alter RouterOS instructions,
  runtime code, frontend panel, protocols, image/release behavior, mounts,
  data behavior, translations, or historical records.

### Accepted completion evidence

- The published MDX registry no longer imports or registers the firewall tool;
  its host-firewall UI component, pure helper, and helper test are removed.
- The regression contract verifies exact removed paths and rejects the retired
  identifiers throughout active docs/frontend source while retaining the
  RouterOS administrator-owned firewall/NAT statements.
- Project-manager inspection reproduced the absent paths and static search;
  the candidate report records docs/frontend quality gates, full Python suite,
  TypeScript build/typecheck, and diff integrity all passing.

## Most recently accepted audit: P0-CLOSURE-04

### Goal

Make the final read-only Phase 0 closure decision after all bounded corrective
tasks. Verify from the actual worktree that no unsupported control remains
exposed before authorizing P1 clean-CHR acceptance.

### Scope and exclusions

- Inspect only: do not edit any file, update this ledger, or modify external
  systems.
- Audit RouterOS policy ownership, immutable runtime, persistent data,
  retired host features, one installation path, version/release/Geo inputs,
  documentation, and exact verification evidence.
- Clearly distinguish Phase 0 source-contract readiness from the real-device
  requirements that remain solely for P1.

### Accepted audit result

- Phase 0 is not ready to close. The current Docker Hub overview provides
  executable RouterOS firewall, NAT, masquerade, port-forward, and filter-rule
  commands branded as Mikrozia guidance, rather than documenting inputs owned
  by the RouterOS administrator.
- `x-ui.rc` is a tracked Alpine/OpenRC host-service script whose retired
  `install.sh` path was its consumer. It is an incomplete host-install vertical
  slice and must be removed with its architecture-map/reference-test updates.
- Real CHR, published-image, and Docker Hub acceptance are still correctly
  deferred to P1 and were not claimed by this source audit.

## Most recently accepted task: P0-RESIDUE-01

### Goal

Remove the last current RouterOS policy-command documentation and final OpenRC
host-service residue without changing the user-owned network-policy guidance,
runtime behavior, or persistent-data contract.

### Scope and exclusions

- Replace only the executable policy-command section of the Docker Hub overview
  with concise operator-owned required network inputs; it must contain no
  RouterOS firewall/NAT command, rule action, interface, subnet, or prescribed
  policy implementation.
- Remove `x-ui.rc`, its architecture-map reference, and update exact regression
  checks for the absence of all retired host service scripts.
- Add focused contract tests. Do not alter panel/runtime/protocols/DB/mounts,
  Dockerfile/image/release workflow, current direct-install commands, or any
  real RouterOS/Docker Hub state.

### Accepted completion evidence

- The current Docker Hub overview replaces executable RouterOS firewall/NAT
  policy with concise operator-owned connectivity inputs and explicitly states
  that Mikrozia supplies no policy commands.
- `x-ui.rc`, its architecture-map entry, and its final host-service vertical
  slice are removed. Release and documentation contracts reject restoration of
  the retired artifact or policy syntax.
- Project-manager inspection reproduced the overview content, absent service
  files, and policy-token search. Candidate evidence records red-to-green tests,
  full Python/docs verification, and diff integrity passing.

## Most recently accepted audit: P0-CLOSURE-05

### Goal

Make the final read-only Phase 0 closure decision after all known corrective
tasks. Confirm the complete source contract before authorizing P1 clean-CHR
acceptance.

### Scope and exclusions

- Read-only: do not edit source, tests, docs, workflows, generated output, or
  this ledger; do not modify external systems.
- Reconcile actual current code, active docs, tools, workflows, and source test
  evidence against every Phase 0 charter rule.
- Explicitly distinguish Phase 0 source-contract readiness from the live-CHR,
  image-publish, and production evidence that belongs to P1.

### Accepted closure evidence

- All current executable/documented policy automation and host-install residue
  is retired. Static hits for those concepts are restricted to exact tests,
  governance records, or prominently labelled historical documents.
- The current source contract preserves immutable image operation, persistent
  mounts, data-safe backup/restore, external-only PostgreSQL, deterministic
  version/Geo/release inputs, and one direct-terminal RouterOS install route.
- The final audit reproduced 112 Python tests and `git diff --check`, with no
  worktree mutation. Real CHR/device, published-image, registry, update,
  rollback, and beginner acceptance evidence is not claimed and is assigned to
  P1-CHR-01.

### Phase 0 closure decision

Phase 0 is closed at the source-contract level as of 2026-11-16. P1-CHR-01 is
the next task, but it changes real RouterOS/container state and remains
owner-authorized before execution.

## Active task: P1-CHR-01

### Goal

Prove that a beginner can install, validate, update, roll back, back up,
restore, and diagnose Mikrozia from the documented direct RouterOS terminal
commands on a clean, disposable CHR.

### Live-device guardrails

- The owner authorized real-device actions on 2026-11-16. Work only on the
  disposable CHR explicitly provided for this acceptance test.
- Do not create, alter, or delete firewall, NAT, masquerade, port-forward,
  routing-policy, address-list, or access-control rules unless the owner gives
  explicit task-specific authorization. Any authorized rule remains an
  operator-owned RouterOS configuration, is tagged `[Mikrozia:operator]`, and
  is never created by Mikrozia itself.
- Take a read-only baseline before mutation. Use only Mikrozia-owned object
  names/comments and persistent paths. Do not touch unrelated containers or
  storage.
- Test image replacement/rollback and SQLite backup/restore with deliberate
  disposable test data; do not test destructive restore against unrelated user
  data. External PostgreSQL live acceptance needs a separately supplied test
  database and is not assumed.
- Record exact RouterOS commands, observed state, image tags/digests, resource
  figures, failures, and recovery actions. Do not claim a step passed without
  visible device evidence.

### Current live evidence and remaining gates (2026-11-18)

- A CHR x86_64 Amazon EC2 instance running RouterOS and the Container package
  at `7.25.1` pulled and started `majlot/mikrozia:v1.0.1-mikrozia` for
  `linux/amd64`. The published multi-architecture manifest was independently
  checked before deployment.
- The persistent database and certificate mounts, environment-owned panel
  administrator, container restart, internal reachability, externally
  allow-listed panel access, Subscription forwarding, and one live Xray
  inbound were observed working. The operator's temporary HTTP-01 port-80
  forwarding was removed after successful issuance.
- The two owner-deferred acceptance gates are intentionally still open: (1)
  live SQLite backup and restore using disposable data, and (2) replace the
  image with a distinct compatible published tag, roll back to
  `v1.0.1-mikrozia`, and prove both database and certificate preservation.
- This live result is not a claim that the separate Ubuntu host (redacted) was
  converted to CHR; the validated instance is the CHR acceptance environment
  (redacted).

## Most recently accepted task: P1-AUTH-ACME-01

### Goal

Make the RouterOS Container Environment the authoritative administrator
credential source, and restore certificate issuance and renewal as supported
product features with a persistent, deterministic, RouterOS-native ACME
implementation. The full technical contract is
`docs/MIKROZIA_AUTH_ACME_SPEC.md`; this entry records the decisions, the
compatibility behavior, and the exclusions.

### Accepted owner decisions

- The administrator account is configured only through
  `MIKROZIA_ADMIN_USERNAME` and `MIKROZIA_ADMIN_PASSWORD`. No default
  `admin/admin` account, no generated random first-login credential printed to
  logs, and no plaintext credential in logs, API responses, diagnostics,
  documentation examples, image layers, or tests.
- On every container start a supplied pair is reconciled onto the stored
  administrator account. A half-supplied pair fails closed. A panel-UI or
  legacy shell/CLI password change is retired in RouterOS mode and refuses with
  a truthful message that directs the operator to the Environment values.
- Certificate issuance and renewal are supported again with exactly two
  official methods: HTTP-01 manual with a temporary operator-owned port-80
  reachability, and Cloudflare DNS-01 automatic with a least-privilege API
  token. Mikrozia creates no RouterOS policy rule and exports no RouterOS
  private key into the container.
- ACME artifacts are persistent under `/root/cert` (account state under
  `/root/cert/.acme.sh/`, certificate at `/root/cert/<domain>/fullchain.pem`
  and `/root/cert/<domain>/privkey.pem`). A validated issuance updates the
  panel and subscription TLS paths automatically, reloads only what changed, and
  preserves the previous certificate and settings on failure.
- Any bundled ACME tool is a pinned, checksum-verified image build input. No
  dynamic `curl | sh` installer, runtime package installation, unpinned
  download, or unmanaged cron daemon is reintroduced.

### Planned compatibility behavior for existing installations

- An existing database that already holds an administrator account and a
  container without the two variables keeps that account exactly as it is:
  same username, same password hash, working login, no reset, no deletion, and
  no insecure default account is created. The panel starts and states that the
  account is managed outside the Environment.
- An existing database plus the two variables is reconciled to exactly those
  values; no other user data (inbounds, clients, traffic, certificates,
  settings) is touched, and the login epoch is bumped so sessions made with the
  previous password stop working.
- A fresh database without the variables does not start the panel; it fails with
  an explicit message naming both variables instead of minting an unknown
  account.
- Certificates, private keys, ACME account state, and Cloudflare tokens live on
  the persistent `/root/cert` mount and therefore survive image replacement and
  rollback.
- The live RouterOS reverse proxy, its certificate, and every real RouterOS
  rule are left untouched.

### Historical source-task exclusions

- Real CHR cutover, real RouterOS rule changes, certificate deletion, and any
  Cloudflare account mutation are excluded; they are a separate owner-authorized
  acceptance task after a successful issuance with a published image.
- No commit, push, tag, version bump, Docker Hub publication, or GitHub release
  occurs in this task.
- The ledger task stays unaaccepted: the implementation stops at
  "awaiting project-manager review" with its final evidence report.

Those exclusions applied to the source implementation stage. They were
subsequently superseded by explicit owner authorization for release and
live-CHR validation. The acceptance evidence is recorded in the task register
and the addendum below; it does not change the operator-owned network-policy
contract.

### Verification evidence (2026-11-17)

Every command below was executed in this working tree. Exit codes are the
authoritative result.

| Gate                           | Command                                                                       | Result                                                                                    |
| ------------------------------ | ----------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| Go formatting                  | `gofmt -l .`                                                                  | clean, no output                                                                          |
| Go vet                         | `go vet ./...`                                                                | exit 0                                                                                    |
| Go tests                       | `go test ./...`                                                               | exit 0                                                                                    |
| Frontend lint / format / types | `npm run lint`, `npm run format:check`, `npm run typecheck`                   | exit 0, 0 warnings                                                                        |
| Frontend tests                 | `npm test`                                                                    | exit 0, 217 files / 2,021 tests                                                           |
| Frontend build / storybook     | `npm run build`, `npm run build-storybook`                                    | exit 0                                                                                    |
| Frontend dependency audit      | `npm run audit`                                                               | exit 1 — **pre-existing**, `frontend/package.json` and `package-lock.json` are unmodified |
| OpenAPI generation             | `npm run gen`                                                                 | exit 0, idempotent; `docs/public/openapi.json` equals `frontend/public/openapi.json`      |
| Docs gates                     | `pnpm typecheck`, `pnpm lint`, `pnpm format:check`, `pnpm test`, `pnpm build` | exit 0 for all five                                                                       |
| i18n parity                    | flattened key comparison over `internal/web/translation/*.json`               | 14 locales, 2,625 keys each, no missing or extra key                                      |
| Python contract suites         | `python3 -m unittest discover -s tests -p 'test_*.py'`                        | exit 0, 116 tests, against `mikrozia:local-p1`                                            |
| Shell syntax                   | `bash -n` / `sh -n` on all shipped scripts                                    | exit 0 for every script                                                                   |
| Shell lint                     | `shellcheck` 0.9.0 in a container, the CI severity levels                     | only one pre-existing `SC2016` in `scripts/build/verify-mikrozia-version.sh`              |
| Go lint                        | `golangci-lint` v2.15.0 (the CI-pinned version) in a container                | exit 0, 0 issues                                                                          |
| Workflow lint                  | `actionlint -shellcheck=`                                                     | exit 0                                                                                    |
| Container build                | `docker build … -t mikrozia:local-p1 .`                                       | exit 0, digest `sha256:47c930c0…`                                                         |
| RouterOS runtime contract      | image content + Geo checksum contract, run in the image                       | exit 0                                                                                    |
| ACME pin                       | downloaded archive SHA-256 vs `ACME_SOURCES`                                  | exact match                                                                               |

Notes on the two linters:

- `golangci-lint` and `shellcheck` are not installed on this host, so both were
  run in their upstream container images at the versions CI pins
  (`golangci/golangci-lint:v2.15.0`, `koalaman/shellcheck:v0.9.0`).
- The lint run found two `noctx` findings in the new `acme_cli.go`. Both were
  fixed (`net.Resolver.LookupHost` and `net.Dialer.DialContext` with a
  context), and the re-run is clean.
- The single remaining `shellcheck` finding is `SC2016` (info) in
  `scripts/build/verify-mikrozia-version.sh`, a file this task did not touch.
  It is a deliberate literal string and is pre-existing on `main`.
- `npm run audit` exits 1 on a pre-existing `follow-redirects` advisory.
  `frontend/package.json` and `frontend/package-lock.json` are unmodified by
  this task, so the result is identical to the baseline.

### Acceptance addendum (2026-11-18)

- The final implementation was committed at
  `6b7e017735be1becabe7d70b74c0b1f0f06f06de`, tagged
  `v1.0.1-mikrozia`, and published as
  `majlot/mikrozia:v1.0.1-mikrozia`. The immutable image manifest includes
  `linux/amd64`, `linux/arm64`, and `linux/arm/v7`; the CHR deployment used
  the `linux/amd64` member.
- Source lifecycle coverage includes serialization of certificate reload,
  panel/subscription/Xray restart, and shutdown operations. The relevant Go
  race suites, container credential/immutability suite, docs gates, shell and
  workflow lint, and final image smoke checks passed as recorded in the release
  evidence.
- On the live CHR, a complete administrator Environment pair reconciled the
  login; HTTP-01 issuance for `soon.parsiserver.com` persisted ACME state and
  certificate files under `/root/cert`, updated panel TLS paths, reloaded the
  affected services, and served a valid Let's Encrypt certificate. The
  temporary operator-created port-80 rule was removed after issuance.
- The project-manager review accepts this bounded task. HTTP-01 renewal remains
  manual by design. A live Cloudflare DNS-01 renewal exercise is optional
  future operational coverage and requires a separately supplied least-
  privilege token.

## Release candidate preparation: `v1.0.2-mikrozia` (P1-RC-01..09)

### Goal

Prepare the Dashboard + internal native RouterOS API telemetry worktree for
`v1.0.2-mikrozia` release publication by clearing all non-image and
multi-architecture release gates locally, bumping `MIKROZIA_VERSION` to
`v1.0.2-mikrozia`, and recording exact evidence before any owner-authorized
commit, tag, push, Docker Hub publish, or CHR deployment.

### Verified release-gate evidence (2026-11-20, P1-RC-09)

| Gate                                    | Command / Target                                                                   | Result                                                                                        |
| --------------------------------------- | ---------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| Frontend tests                          | `make test-fe` (`vitest run`)                                                      | exit `0`, `225/225` files, `2062/2062` tests (`587s`)                                         |
| Go tests (Run 1 & Run 2)                | `make test-go` ×2 sequentially                                                     | exit `0` (`117s`), exit `0` (`107s`)                                                          |
| Go race detector                        | `make race`                                                                        | exit `0` (`281s`)                                                                             |
| Go lint                                 | `golangci/golangci-lint:v2.15.0` (`--timeout 10m`)                                 | exit `0`, `0 issues.` (`581s`)                                                                |
| Docs gates                              | `format:check`, `lint`, `typecheck`, `test`, `build`                               | exit `0` for all five (`120` formatted, `0` lint issues, `104/104` tests, `275` static pages) |
| OpenAPI sync & diff hygiene             | `cmp -s frontend/public/openapi.json docs/public/openapi.json`, `git diff --check` | exit `0` (`MATCH`, clean)                                                                     |
| Local `amd64` image & Python suite      | `mikrozia:rc-final-amd64` (`sha256:a5d27367…`) + `unittest discover -s tests`      | exit `0`, `118/118` tests passed (`60.9s`)                                                    |
| Multi-arch OCI exports (`--pull=false`) | `linux/arm64/v8`, `linux/arm/v7` via `buildx` (`mk`)                               | exit `0` for both; verified OCI platform metadata                                             |

### Explicit exclusions (still pending owner authorization)

- No Git commit, tag, or push.
- No Docker Hub image publication or GitHub release.
- No live CHR/RouterOS deployment or configuration change.

## Live CHR deployment and acceptance: `v1.0.2-mikrozia` (P1-DEPLOY-01)

### Goal

Replace the existing Mikrozia container on the owner's CHR with the immutable
`majlot/mikrozia:v1.0.2-mikrozia` image, preserve all persistent data, connect the
panel to the internal RouterOS native API (read-only), and accept the Dashboard
visually. No Git, GitHub, Docker Hub, or RouterOS network-policy change was made.

### Deployed state (2026-11-20, redacted)

| Item            | Result                                                                                               |
| --------------- | ---------------------------------------------------------------------------------------------------- |
| RouterOS        | `7.25.1` on CHR (`Amazon EC2 t3.small`), container package available                                 |
| Image tag       | `majlot/mikrozia:v1.0.2-mikrozia` (pinned; `v1-mikrozia` deliberately unused)                        |
| Container       | `mikrozia:v1.0.2-mikrozia`, new per-release `root-dir`, `RUNNING`                                    |
| Old container   | Definition only removed after the new one started; previous `root-dir` left on disk                  |
| Persistent data | unchanged: `MIKROZIA_DATA` → `/etc/x-ui`, `MIKROZIA_CERT` → `/root/cert`                             |
| Panel           | login OK, shows `Mikrozia v1.0.2`, Xray `Running v26.10.21`                                          |
| Data verified   | same `x-ui.db`, same cert directory, inbound and clients preserved                                   |
| RouterOS API    | internal `api` on TCP/8728 only, restricted to the container VETH address                            |
| API identity    | dedicated user with `read,api` only; no write/policy/sensitive/ssh/winbox                            |
| Dashboard       | `RouterOS API: Connected`; live CPU/RAM with Avg/Peak; Clients, Top Inbounds, Storage, Uptime render |

### Accepted limitations (truthful states, not defects to hide)

- `Overall Speed` renders `Unavailable` in this deployment. Reason: inside the
  RouterOS container the interface is named `veth-…`, and the panel counts that
  name only inside a proven container scope; otherwise it labels `netIO`
  unavailable instead of guessing host traffic. No code change was made.
- CPU/RAM `Container` lines render `—` because container-scoped cgroup series are
  not exposed in this RouterOS container runtime; RouterOS lines are primary.
- RouterOS container log reads remain deferred prerequisites (no stable configured
  container identity is assumed by the panel).
- Xray logs show repeated client-side TLS handshake failures from an external peer;
  these are client certificate mismatches, not panel/API errors.

### Explicit exclusions (respected)

- No commit, tag, push, release, or image publish in this task.
- No Firewall/NAT/masquerade/routing/address-list change; existing operator NAT was
  left untouched.
- No RouterOS reboot, no other container touched, no data/cert/mount deleted.

## Local candidate preparation: `v1.0.3-mikrozia` (2026-11-21)

The candidate adds exact-comment RouterOS container discovery, bounded read-only
`/container/log/print` access, and an optional separately credentialed
`/container/restart` path. Restart rechecks ID, name, comment, and running
status immediately before one command; a lost reply is reported as uncertain
without retry. Container History CPU/RAM uses only verified cgroup samples;
host fallback is not plotted as container data. The pre-existing container
network-interface fix remains in this worktree.

`MIKROZIA_VERSION` was advanced locally from `v1.0.2-mikrozia` to
`v1.0.3-mikrozia`. The local `amd64` image is tagged
`mikrozia:v1.0.3-local`; this is not a published release tag. An isolated
localhost preview was upgraded with the same temporary database and certificate
mounts, retained its login, and displayed `Mikrozia v1.0.3`. The Restart menu,
RouterOS log fallback, and Container History scope were inspected visually.

Verification: `make test-go` exit `0`; full Vitest `225/225` files and
`2068/2068` tests exit `0`; frontend format/lint/typecheck/build exit `0`;
`golangci-lint v2.15.0` initially reported `0 issues.`; docs tests
`104/104`, build `275/275`, and format check exit `0`; full Python suite on
the final local image `119/119` exit `0`. Two stale Python assertions were
corrected after their initial failure. The first full Vitest attempt was
blocked by sandbox loopback `EPERM`; the identical run with loopback access
passed. The first docs build stalled in sandbox and the repeated local build
passed. Final lint after a new restart uncertainty test and race validation
are recorded in the task report rather than inferred from these earlier gates.

Live acceptance remains separate: the installed CHR still runs
`v1.0.2-mikrozia`. The new image has not been installed there, the optional
RouterOS operational account has not been configured, and no real container
restart was sent. Before publishing, verify read-only container logs against
WinBox, cgroup history on CHR, permissions for the operational account, and
one explicitly authorized restart with persistent data checks. No commit,
push, tag, Docker Hub publication, or CHR mutation was performed here.

## Release preparation: `v1.0.4-mikrozia` (2026-11-22, DASH-RELEASE-01..04)

Owner-authorized release of `v1.0.4-mikrozia` (`majlot/mikrozia:v1.0.4-mikrozia`
and floating `majlot/mikrozia:v1-mikrozia` on `linux/amd64`, `linux/arm64/v8`,
and `linux/arm/v7`):

- **Truthful Stop Xray:** immediate live Xray state reconciliation in
  `ServerService.CurrentStatus()`, `StopXrayService()`, and `RestartXrayService()`;
  fail-closed `isManuallyStopped` handling; guards in `NodeTrafficSyncJob` and
  `tunnelmonitor` so a manually stopped core is not automatically restarted.
- **Stop Container retirement:** `Stop Container` removed from `OverviewActionBar`
  and all 14 translation catalogs (`Stop Panel` remains disabled; `Restart Container`
  readiness preserved).
- **RouterOS 7.25.1 short env aliases:** `MIKROZIA_ROS_CONTAINER_COMMENT`,
  `MIKROZIA_ROS_RESTART_USERNAME`, and `MIKROZIA_ROS_RESTART_PASSWORD` supported
  strictly as fallback when canonical `MIKROZIA_ROUTEROS_*` names are unset, with
  fail-closed rejection of partial canonical or alias configurations.
- **Documentation & tests:** `env-vars.mdx` aligned across `en`, `fa`, `ru`, and `zh`;
  regression coverage added for Stop Xray, RouterOS aliases, Container Logs, and the
  Restart Container availability matrix.

## Release preparation: `v1.0.5-mikrozia` (2026-11-22, Patch Release)

Owner-authorized patch release of `v1.0.5-mikrozia` (`majlot/mikrozia:v1.0.5-mikrozia`
and floating `majlot/mikrozia:v1-mikrozia` on `linux/amd64`, `linux/arm64/v8`,
and `linux/arm/v7`):

- **Root cause & code fix (`internal/routeros/container.go`):** In RouterOS 7.25.1
  Native API `/container/print`, the `logging` property returns `"true"` instead
  of `"yes"`, causing `Logs → RouterOS Container` to falsely report
  `logging_disabled` while RouterOS API was connected and container logging was
  active. Added `routerOSBool(value)` accepting only explicit `"yes"` and
  `"true"` (while `"no"`, `"false"`, empty, or invalid values remain `false`),
  preserving single-container exact-comment identity verification without any
  fallback or RouterOS mutation.
- **Regression coverage (`internal/routeros/container_test.go`):** Preserved
  `logging=yes` and identity checks (zero, wrong comment, duplicate comment) and
  added cases for `logging=true`, `logging=false`, `logging=no`, and empty
  `logging=`.
- **Installation/upgrade docs (`docs/MIKROZIA_CHR_INSTALL_FA.md`):** Updated the
  upgrade runbook to use explicit `<VERSIONED_TAG>` and `<PREVIOUS_VERSIONED_TAG>`
  placeholders, clarified that only the previous container definition is removed
  while `MIKROZIA_DATA` and `MIKROZIA_CERT` mounts are preserved, warned against
  unfiltered `print detail` exposing sensitive envs, recorded the RouterOS 7.25.1
  short alias `MIKROZIA_ROS_CONTAINER_COMMENT` command followed by restarting
  only that container, and removed the stale deferred status for Container Logs.
- **Publication status:** Git commit `cb6ed93ed0515fd209dfb0ab9392ad3101598f4a`
  and annotated tag `v1.0.5-mikrozia` (`bbf735777b927481e3c7088496c58d575a7046da`)
  were pushed to `origin`, but **no Docker image was published for
  `v1.0.5-mikrozia`** because upstream `Chocolate4U/Iran-v2ray-rules` pruned
  pinned release tag `202611141143` (HTTP 404 during `DockerInit.sh`).
  `v1.0.5-mikrozia` is therefore not installable as a container image and is
  superseded by `v1.0.6-mikrozia` without altering or deleting the Git tag.

## Release preparation: `v1.0.6-mikrozia` (2026-11-22, Patch Release)

Owner-authorized patch release superseding the unpublished `v1.0.5-mikrozia`
container image:

- **Pinned Geo data refresh (`GEO_DATA_SOURCES`):** Replaced the pruned upstream
  `Chocolate4U/Iran-v2ray-rules` tag `202611141143` (HTTP 404) with immutable
  dated release tag `202611221249` for `geoip_IR.dat`
  (`a03d84225badee0956766f154873edabcb30664082d301b783ad36161f9e63de`) and
  `geosite_IR.dat`
  (`0522f6f4dbcdab30734e961cd36fc8998151a3b610e7eddb443e8409eafe5565`), both
  verified against upstream `.sha256sum` sidecars.
- **Preserved fixes:** Carries the `v1.0.5-mikrozia` RouterOS Container Logs
  `logging=true`/`yes` fix (`internal/routeros/container.go`), regression tests,
  and updated Persian CHR upgrade guide (`docs/MIKROZIA_CHR_INSTALL_FA.md`).

## Live CHR deployment and dashboard acceptance: `v1.0.6-mikrozia` (P1-DEPLOY-06)

Owner-authorized image replacement and visual acceptance completed on 2026-11-23.
The versioned image was installed through RouterOS Container with a new
per-release RootFS while preserving the existing `MIKROZIA_DATA` and
`MIKROZIA_CERT` mounts. A RouterOS backup was saved before the replacement; the
old container definition was removed only after the new image had extracted
successfully. No persistent data or certificate mount was deleted.

- **Dashboard accepted:** the new panel version, running Xray, and internal
  read-only RouterOS API connection rendered correctly. RouterOS CPU/RAM,
  cgroup-scoped Container CPU/RAM, Overall Speed, Clients, Top Inbounds,
  Storage, Uptime, and Container History were visually checked against RouterOS.
- **Container logs accepted:** `Logs → RouterOS Container` returned live
  Container download/startup and panel/Xray entries after the new container had
  become ready; they were compared with RouterOS Container logs. The existing
  dedicated `read,api` account remained least-privilege and was not broadened.
- **Lifecycle safety:** Restart/Stop controls were inspected only. `Restart
  Container` remains disabled because no separately authorized operational
  RouterOS account was provisioned; `Stop Container` remains absent by design.
  No panel, Xray, or container lifecycle action was triggered during acceptance.
- **Exclusions:** no RouterOS Firewall/NAT/routing change, API credential or
  policy change, RouterOS reboot, Git operation, image publication, or change to
  another container. Operational identifiers and credentials are intentionally
  absent from this Ledger.

## Release preparation: `v1.0.7-mikrozia` (2026-11-23, Mikrozia Identity & `echodev7/mikrozia`)

Owner-authorized release completing the Mikrozia brand identity transition and
moving Docker Hub publication to `echodev7/mikrozia` (`echodev7/mikrozia:v1.0.7-mikrozia`
and floating `echodev7/mikrozia:v1-mikrozia` on `linux/amd64`, `linux/arm64/v8`,
and `linux/arm/v7`):

- **Go module & build metadata:** Migrated `go.mod`, internal imports,
  `.golangci.yml`, and `Dockerfile` version `-ldflags` to
  `github.com/echodev7/mikrozia/v3`.
- **UI, OpenAPI, translations, and docs:** Completed Mikrozia rebranding across
  the React frontend (`AppSidebar`, `LoginPage`, `manifest.webmanifest`,
  `build-openapi.mjs` / `openapi.json`), Fumadocs site, READMEs, and `.github/`
  metadata; removed legacy `donate.sanaei.dev`/`docs.sanaei.dev` links, dead CSS
  and dead i18n keys (`menu.donate`, `menu.docs`), unused upstream media assets,
  and stray build artifacts.
- **Docker Hub migration:** Updated `.github/workflows/docker.yml`, READMEs,
  multilingual installation/operations docs, and contract tests to
  `echodev7/mikrozia` while preserving historical records and GPL-3.0
  attribution.
- **Runtime compatibility preserved:** Kept persistent RouterOS mount paths
  (`/etc/x-ui`, `/root/cert`), `/app/x-ui`, `XUI_*` environment variables, and
  existing database/session compatibility intact.

## New-task template

Add an entry when the project manager authorizes a new bounded task:

| ID        | Status | Bounded task                     | Evidence / current result                    | Owner approval condition                    |
| --------- | ------ | -------------------------------- | -------------------------------------------- | ------------------------------------------- |
| `AREA-NN` | [~]    | One-sentence vertical-slice goal | Baseline, report summary, and known blockers | Exact test gates and project-manager review |

Every task prompt must state its ledger ID, goal, exclusions, required reading,
dirty-worktree preservation rule, verification gates, ledger-update authority,
and instruction to stop for project-manager review after its final report.
