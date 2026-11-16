# Mikrozia Project Charter

Status: authoritative project direction
Audience: maintainers, contributors, Codex, Antigravity/Gemini, and other agents

## 1. Product statement

Mikrozia is an independent control panel and runtime package designed for
MikroTik RouterOS Container, with CHR as the first-class reference platform.

The project started from 3x-ui because reusing a mature panel, protocol model,
database, and Xray integration is safer and faster than recreating them from
zero. That origin does not define the final product. Mikrozia will keep useful
structure while progressively owning its architecture, identity, release
process, documentation, features, and maintenance decisions.

The long-term product is not “3x-ui with a MikroTik image.” It is Mikrozia: a
RouterOS-native product whose historical code base originated from 3x-ui.

## 2. Priority order

When goals compete, decide in this order:

1. User data integrity and recoverability.
2. RouterOS/CHR correctness and operational safety.
3. Security and deterministic supply chain.
4. Reliable beginner installation, update, rollback, and troubleshooting.
5. Runtime stability and appropriate CPU/RAM/storage use.
6. Retaining useful panel functionality.
7. Simplicity and removal of dead or misleading code.
8. Visual branding and broad internal renaming.

Branding must not get ahead of a stable runtime. Feature count must not get
ahead of data safety or RouterOS correctness.

## 3. RouterOS platform contract

### 3.1 Supported operating model

- RouterOS Container is the production environment.
- CHR x86_64 is the reference environment for beginner documentation and the
  first acceptance target.
- Published architectures must be limited to architectures that can be built,
  pulled, started, and smoke-tested under the RouterOS contract.
- The image must start without systemd and without assuming control of the host
  Linux kernel.

### 3.2 Network ownership

Mikrozia does not create, modify, or delete RouterOS:

- firewall filter rules;
- NAT or masquerade rules;
- port forwarding;
- policy routing;
- address lists;
- access-control policy.

The RouterOS administrator owns those decisions. Mikrozia may document required
ports and connectivity, validate reachability, and show warnings, but must not
silently mutate network policy.

### 3.3 Persistent data

The stable runtime contract is:

- database and panel settings: `/etc/x-ui`;
- certificates and ACME state: `/root/cert`;
- application logs: stdout/stderr first, with persistent logs only when
  explicitly designed and documented;
- software binaries: image RootFS, not persistent data mounts.

Replacing or rolling back an image must not erase the database, accounts,
clients, traffic state, settings, or certificates.

### 3.4 Immutable software

Panel, Xray, and bundled sidecar binaries are installed at image build time.
Runtime code must not download and replace executable software or rewrite the
container RootFS as an update mechanism.

Software updates use a new versioned image. A valid update flow preserves
mounts, supports rollback to the previous image, and reports migration risks
before destructive change.

## 4. Feature decision matrix

Evaluate inherited and proposed features with this sequence:

| Finding                                                              | Decision                                                   |
| -------------------------------------------------------------------- | ---------------------------------------------------------- |
| Works safely in RouterOS Container                                   | Keep it and add RouterOS coverage                          |
| Works after a bounded adaptation                                     | Adapt it behind the platform contract                      |
| Requires a host service that can be replaced in userspace            | Use the userspace design if it is stable and resource-safe |
| Requires unavailable host-kernel control or privileged host mutation | Remove the complete feature slice                          |
| Mutates firewall/NAT policy                                          | Keep policy outside Mikrozia; document inputs only         |
| Duplicates another supported workflow                                | Keep one canonical workflow and remove the duplicate       |
| Has no active UI/API/runtime consumer                                | Remove dead code, dependencies, docs, and tests            |

Do not remove a feature merely because it came from upstream. Do not retain a
feature merely because upstream has it.

## 5. Simplicity rules

- One supported way to install on RouterOS.
- One supported way to update: replace the image while reusing data mounts.
- One canonical source for each release-critical version or setting.
- No buttons or API routes that cannot succeed on the supported platform.
- No compatibility setting without a real migration or rollback need.
- No dead permission scope, translation key, dependency, workflow, or docs path.
- No generated file edited by hand when a generator owns it.
- No silent fallback that changes security, persistence, or network behavior.
- No “temporary” parallel architecture without an owner and removal condition.

Compatibility code for old stored data may remain only when it is private,
tested, and has a clear migration boundary. It must not remain as a public new
configuration option.

## 6. Data and migration policy

- Existing user data is presumed valuable.
- Never zero, delete, or rewrite a stored field merely because its old runtime
  feature is unavailable.
- Schema and semantic changes require explicit migrations.
- Migrations must be idempotent and tested against realistic old databases.
- Preserve token hashes, account identifiers, expiry values, enable state, and
  credentials unless the change explicitly requires rotation.
- A failed migration must leave a recoverable database and a useful error.
- Major releases require documented backup, validation, and rollback steps.

## 7. Build and supply-chain policy

- `MIKROZIA_VERSION` identifies the Mikrozia release.
- `XRAY_VERSION` identifies the Xray version bundled into the image.
- The upstream 3x-ui base is tracked separately and must not become Mikrozia's
  permanent public version identity.
- Release inputs are pinned and validated.
- Downloaded executables are checksum-verified before extraction or execution.
- CI must pass the same version values to smoke and publish builds.
- Versioned tags are the production interface; `latest` is convenience only.
- Every published architecture must share the same source revision, version
  metadata, migrations, and runtime contract.
- SBOM, provenance, digest, image labels, and smoke-test results belong in the
  release record.

## 8. Upstream relationship

### 8.1 What is preserved

- GPL-3.0 compliance and attribution;
- useful architecture and mature protocol behavior;
- selected upstream security fixes and bug fixes;
- selected features that pass the Mikrozia platform contract.

### 8.2 What is not preserved automatically

- upstream branding in the final product;
- VPS/systemd assumptions;
- host firewall management;
- in-place software self-update;
- every upstream feature, dependency, workflow, or release policy;
- upstream versioning as Mikrozia's permanent identity.

### 8.3 Importing upstream changes

For each upstream import:

1. Identify the exact commits and purpose.
2. Review data migrations and public API changes.
3. Check RouterOS/container assumptions and resource cost.
4. Adapt instead of blindly merging when contracts differ.
5. Run Mikrozia regression, image, and CHR tests.
6. Record the upstream base and imported fixes in release notes.

After the independence milestone, upstream is a curated input, not a branch to
which Mikrozia must remain mechanically identical.

## 9. Identity and version transition

The current transitional version may include the upstream base, for example:

`v1-mikrozia`

After the RouterOS baseline, migrations, release pipeline, and documentation are
stable, Mikrozia moves to independent semantic versioning, for example:

`Mikrozia v1.0.0`

At that point the upstream base remains internal release metadata rather than
the public product version. The identity phase includes:

- product name, logo, colors, metadata, links, and user-facing copy;
- repository and release ownership;
- documentation and support identity;
- package/module/namespace migration where technically justified;
- removal of obsolete upstream installation and release artifacts.

Do not mix a broad module-path or namespace rename into compatibility work. It
is a dedicated, tested migration phase.

## 10. Roadmap and gates

### Phase 0 — compatibility and safety baseline (current)

- Audit inherited features and dependencies.
- Remove unsafe in-place panel and executable updates.
- Eliminate host-only behavior that cannot work in RouterOS Container.
- Preserve user data while retiring dead features.
- Make image inputs pinned, verified, and deterministic.
- Keep useful userspace protocols and operations working.

Gate: code, UI, API, docs, translations, and tests agree on the RouterOS
contract; no unsupported control remains exposed.

### Phase 1 — RouterOS product contract

- Validate supported RouterOS releases and architectures.
- Establish resource budgets and real CHR test matrix.
- Finalize mounts, ports, logs, health checks, backup, restore, and rollback.
- Provide a beginner installation path and deterministic troubleshooting tree.

Gate: a new user can install, validate, update, roll back, back up, restore, and
diagnose Mikrozia from the documentation on a clean CHR.

### Phase 2 — Mikrozia identity

- Replace user-facing upstream identity.
- Align repository, metadata, docs site, image labels, and release notes.
- Introduce independent versioning.
- Perform internal namespace/module migration only with a dedicated plan.

Gate: the product is presented and released as Mikrozia while attribution and
license obligations remain correct.

### Phase 3 — independent maintenance

- Own release cadence, security response, migrations, and Xray qualification.
- Selectively import upstream work.
- Add or remove features according to Mikrozia users and RouterOS constraints.
- Maintain a compatibility matrix and upgrade guarantees.

Gate: Mikrozia can evolve without requiring upstream release structure or
runtime services.

## 11. Established decisions

These decisions are already accepted and must not be reversed accidentally:

- RouterOS firewall and NAT remain user-managed.
- User IP-limit data is not silently cleared when host enforcement is absent.
- Legacy panel self-update is not a supported runtime path.
- Panel releases are versioned container images.
- Xray is a build-time image component, not a runtime-installed component.
- Existing legacy token scopes migrate losslessly to supported scopes.
- Current version display is read-only unless a future RouterOS-aware image
  updater is explicitly designed and approved.
- AmneziaWG runs exclusively via the embedded userspace engine (`amneziawg-go`);
  Kernel Mode is removed and not supported in RouterOS Container.
- Host Kernel BBR (`x-ui` `sysctl` toggle) is removed from Mikrozia because
  RouterOS Container does not expose the required host-kernel BBR/qdisc
  capability on the reference CHR; userspace TUIC/QUIC/FinalMask BBR is
  preserved.
- Kernel-dependent Xray `sockopt.tcpcongestion=bbr` is preserved in storage
  for backward compatibility and rollback safety, omitted from RouterOS runtime
  config generation (including `tlsSettings.echSockopt` and outbounds) so
  subsequent `sockopt` options are not skipped, and new configurations default
  to `OS Default (Unset)`.
- Logs Management in RouterOS Container serves in-memory panel application
  logs and Xray access logs (with a UI privacy notice), removes `journalctl`
  and host `syslog` backend fallback/API controls, reads only `"${log_folder}/3xui.log"`
  in `x-ui.sh` Alpine mode, and documents RouterOS `logging=yes` /
  `/log/print follow-only where topics~"container"` as read-only user guidance.

New decisions should be added here only after acceptance, not while they are
still proposals or unfinished experiments.

## 12. Required workflow for each change

### Before editing

1. Read `AGENTS.md` and this charter.
2. Inspect `git status --short` and relevant diffs.
3. Identify the current phase and affected platform invariants.
4. Trace UI → API → service → persistence/job/dependency → docs/tests.
5. State what must remain working.

### During implementation

1. Preserve unrelated and user-owned changes.
2. Make the smallest complete vertical change, not the smallest line diff.
3. Add data migration before removing old public semantics.
4. Remove dead vertical slices completely.
5. Keep generated outputs synchronized through their generators.
6. Add tests that would fail if the old unsafe behavior returned.

### Verification

Run the complete relevant suites, normally including:

- Go formatting, focused packages, and full Go tests;
- frontend format, lint, typecheck, build, and full Vitest;
- translation dead-key/locale parity tests;
- Python RouterOS/generator tests;
- shell syntax and shellcheck when available;
- OpenAPI generation and byte-for-byte docs synchronization;
- docs format, lint, typecheck, tests, and build;
- Docker image build, labels, bundled binaries, mounts, and smoke tests;
- CHR/RouterOS tests for changes that affect the runtime contract.

An environment blocker is not a pass. Record the exact command, exit code, and
error, and complete all verification that the environment permits.

## 13. Definition of done

A change or phase is complete only when:

- the implementation matches this charter;
- the entire vertical slice is internally consistent;
- user data and rollback behavior are proven safe;
- no unsupported or dead control remains exposed;
- generated code, OpenAPI, translations, and docs are synchronized;
- required tests have exited and their real outcomes are reported;
- Docker/CHR behavior is tested or an explicit environment blocker is recorded;
- static searches confirm removed behavior is absent with documented exceptions;
- the report matches the actual diff and `git status --short`;
- no commit, push, tag, release, or publication occurred without explicit user
  authorization.

## 14. Instruction hierarchy

- `AGENTS.md`: agent entry point and operational rules.
- This charter: authoritative product direction and architecture decisions.
- `CLAUDE.md`: inherited repository engineering map and detailed conventions.
- `frontend/CLAUDE.md`: frontend-specific implementation rules.
- `docs/architecture.md`: current technical topology.
- Phase reports: evidence and history, not permanent authority.

If these sources disagree, fix the stale lower-level document instead of
inventing another parallel rule set.
