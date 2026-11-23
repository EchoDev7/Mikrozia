# Gemini / Antigravity Instructions

Before analyzing or editing this repository, read `AGENTS.md` and then
`docs/MIKROZIA_PROJECT_CHARTER.md` in full. They define the authoritative
Mikrozia mission, RouterOS invariants, phase order, upstream policy, data-safety
requirements, and completion criteria.

@[Mikrozia Project Charter](docs/MIKROZIA_PROJECT_CHARTER.md)

Use `CLAUDE.md`, `frontend/CLAUDE.md`, and `docs/architecture.md` for inherited
implementation details. Where inherited Mikrozia guidance conflicts with Mikrozia
product direction, follow the Mikrozia charter.

Do not report completion from progress logs. Verify the actual diff, run the
required complete tests, disclose every failure or environment blocker, and do
not commit, push, tag, release, or publish unless the user explicitly requests
that external action.
