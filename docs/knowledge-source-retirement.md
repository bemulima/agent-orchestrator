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
- All 37 code/provisioning repositories in the approved scope now own a
  self-contained agent architecture and repository-local documentation. Their
  agent rules do not depend on sibling `prompts`, `journal`, or `wiki` paths.
- The only journal result marked partial was re-triaged. Its two remaining
  checks are intentional external integration suites now owned by the target
  repositories: PostgreSQL integration for `ms-go-student` and opt-in HTTP/NATS
  integration for `ms-ts-html-validator`. Both are documented alongside their
  current commands; no open task depends on the journal copy.
- The operational onboarding runbook no longer instructs operators to connect,
  scan, or provision the three source repositories. Historical source names and
  reviewed revisions remain only as provenance.
- Annotated tag `archive/pre-retirement-2026-08-13` is published for both
  source repositories and resolves to the reviewed revisions above: `2a16785`
  for `ms-course-promts` and `dd74102` for `ms-course-journal`.
- A final remote-main check found that historical onboarding PRs had later
  added `AGENTS.md` and generated `.ai` discovery/service files to both source
  repositories, contrary to their retirement roles. Only those onboarding
  files were removed in `ms-course-promts` commit `b12d5a6` and
  `ms-course-journal` commit `0c858b8`; the source policy and journal content
  was preserved. Annotated tag `archive/final-pre-retirement-2026-08-13`
  resolves to those final revisions.
- GitHub repositories `bemulima/ms-course-promts` and
  `bemulima/ms-course-journal` are archived and therefore read-only. Both retain
  `main`, the final annotated snapshot tag, and complete Git history; neither
  had an open issue or pull request at archive time.
- Docker/PostgreSQL recovery completed without deleting or recreating the
  durable volume. Migration `015_project_lifecycle` was the only pending
  migration and was applied transactionally before the rehearsal.
- Both catalog entries completed `analyzed -> archived -> analyzed -> archived`.
  Each has two `project.archived` audit events, one `project.restored` event,
  final status `archived`, and preserved `archived_from_status: analyzed`.
- Catalog and GitHub retirement are complete for `prompts` and `journal`.
  Physical deletion of either GitHub repository, local checkout, orchestrator
  managed clone, database history, or snapshot remains out of scope without a
  new explicit authorization. `wiki` remains active but frozen and is not part
  of this retirement action.

## Journal retirement index

The journal contains no canonical task state. Issues, repository history,
tests, and repository-owned documentation supersede these ten task directories:

| Journal directory | Retirement decision |
|---|---|
| `2025/12/11/legacy` | Historical lifecycle evidence; current student, sandbox, and gateway tests own executable coverage |
| `2025/12/12/legacy` | Completed commit reports; Git history is canonical |
| `2025/12/14/refactor-ts-architecture` | Completed; current TypeScript repository architecture and tests are canonical |
| `2025/12/14/test-refactor-all-services` | Re-triaged; external integration gates are explicitly owned by `ms-go-student` and `ms-ts-html-validator` |
| `2025/12/15/analysis-business-rules` | Superseded by verified project-local business and contract documentation |
| `2025/12/15/docs-split-hoppscotch-gateway` | Completed; gateway docs and collections are canonical |
| `2025/12/16/feature-ms-getway-split-admin-client` | Completed under the corrected `ms-gateway` name; current routing docs are canonical |
| `2025/12/16/test-e2e-wiki-gateway` | Historical run only; repository-local E2E suites own executable checks |
| `2025/12/21/refactor-rbac-postgres` | Completed; RBAC migrations, tests, and docs are canonical |
| `2025/12/22/feature-admin-user-list` | Completed; user-service API/tests and admin UI docs are canonical |

This index is a disposition record, not a request to modify the journal.
