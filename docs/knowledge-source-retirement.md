# Knowledge source retirement

This manifest governs the staged retirement of the legacy `prompts` and
`journal` repositories and the temporary freeze of `wiki`. It does not
authorize remote archive, deletion, push, merge, or publication.

## Source revisions

| Source | Reviewed revision | Disposition |
|---|---:|---|
| `ms-course-promts` | `2a16785` | Extract reusable policy, then archive after project-local migration and reference checks |
| `ms-course-journal` | `dd74102` | Do not migrate task state; issues are canonical, retain only unique durable knowledge |
| `course-wiki` | `6cd12cd` on `agent/rename-practice-task-docs` | Freeze as a migration source; keep until owned docs/contracts are complete and a separate wiki design is approved |

## Ownership rules

- The orchestrator owns versioned common agent workflow and safety templates.
- Each code repository owns its business rules, architecture decisions,
  contracts, operations, testing instructions, and developer documentation.
- Issues and linked pull/merge requests own task state and delivery history.
- A future central wiki may index or render project-owned knowledge, but must
  not become the only canonical copy of a service contract or business rule.

## Extracted from `prompts`

Template bundle `v1` retains the reusable parts of Git/issue/PR delivery,
bugfix, feature, refactor, coding, independent review, testing, migration,
contract, documentation, cache, and source-of-truth policy. It deliberately
excludes the journal workflow, hard-coded repository inventory, copy-paste
shell recipes, and generic framework layouts that are not supported by a
target repository's evidence.

Generation and drift checking use the existing guarded onboarding flow:

1. `make agent-template-check` validates and fingerprints the canonical bundle.
2. `make project-onboard SERVICE=name` generates a read-only proposal.
3. `make project-diff RUN_ID=id` shows repository-local drift.
4. Apply remains worktree-isolated and owner-approval-gated.

## Per-repository migration gate

For every target repository, record the source file and revision, owning
repository, canonical destination path, evidence for ownership, replaced links,
verification, and reviewer decision. A source repository is eligible for
archive only when all applicable items pass:

- useful content has an explicit owner and a repository-local destination;
- duplicated or obsolete content is marked `drop` with a reason;
- contracts and business rules were validated against current code;
- absolute and cross-repository links were replaced with stable relative or
  remote links;
- no active issue, plan, run, task, or onboarding operation depends on the
  source project;
- the target repositories pass their documented checks and independent review;
- the orchestrator project entry is archived and a restore rehearsal succeeds;
- remote repository archive or deletion receives separate explicit owner
  authorization.

## Current state

- Common policy extraction is implemented in
  `internal/onboarding/templates/v1` and generated as a shared/common layer.
- Onboarding rejects repositories connected as `policy`, `documentation`, or
  `archive`; these sources are analyzed and retired, not provisioned with an
  agent architecture.
- Project archive/restore is implemented without deleting history or discovery
  snapshots.
- No source repository has been remotely archived or deleted.
- Project-specific `prompts` and `wiki` content migration remains pending and
  must proceed repository by repository under the gate above.
