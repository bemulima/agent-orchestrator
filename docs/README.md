# Documentation index

Repository code, migrations, tests, configuration, and the documents below are the source of truth for `course-dev-orchestrator`.

- [Portable CURRENT graph](architecture-graph-v1.md): deterministic exporter, immutable declaration pins and independently declared fleet scope.
- [Architecture conventions](architecture-conventions.md): dependency direction, safety invariants, persistence, workflows, agents, publication, and project lifecycle.
- [Implementation plan](implementation-plan.md): staged scope, acceptance criteria, and explicit non-goals.
- [Progress](progress.md): verified implementation history, current state, remaining work, and exact next task.
- [Repository onboarding runbook](repository-onboarding-runbook.md): operational inventory and connection procedure for this local platform installation.
- [Onboarding PRs](onboarding-prs.md): historical reviewed onboarding publication set.
- [Platform work items](platform-work-items.md): prioritized platform gaps.
- [Knowledge source retirement](knowledge-source-retirement.md): source revisions, ownership split, archive gates, and current non-destructive status.
- [Live plan health-handler tests](live-plan-health-handler-tests.md): bounded execution evidence for the referenced plan.
- [Backup and restore](backup-restore.md): D4 operator commands, immutable set format, and isolated PostgreSQL restore proof.

Machine-readable mirrors for agent use live under `.ai/contracts`. The canonical shared policy distributed to other repositories lives in `internal/onboarding/templates/v1`; the root `.ai/rules/common.md` must remain byte-identical to that embedded common-rules file.

- [Routing and contract control prerequisites](agent-control-plane.md): canonical catalog, routing metadata and contract verification APIs used by context retrieval.

- [Context retrieval audit and design](agent-context-retrieval-audit.md): existing retrieval foundations, evidence and security gaps, reusable architecture, Student pilot, and staged rollout.
- [Local context retrieval](agent-context-retrieval.md): implemented read-only core, coverage, trust and freshness, canonical pack, offline Prepare/Expand CLI and deterministic Gold evaluation.

- [Go context retrieval Wave 3](go-context-retrieval-wave3.md): opt-in v1.2, cumulative companion and joint budgets, source-bound syntax proofs, authored-area diagnostics and synthetic Gold.
