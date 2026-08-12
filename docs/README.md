# Documentation index

Repository code, migrations, tests, configuration, and the documents below are the source of truth for `course-dev-orchestrator`.

- [Architecture conventions](architecture-conventions.md): dependency direction, safety invariants, persistence, workflows, agents, publication, and project lifecycle.
- [Implementation plan](implementation-plan.md): staged scope, acceptance criteria, and explicit non-goals.
- [Progress](progress.md): verified implementation history, current state, remaining work, and exact next task.
- [Repository onboarding runbook](repository-onboarding-runbook.md): operational inventory and connection procedure for this local platform installation.
- [Onboarding PRs](onboarding-prs.md): historical reviewed onboarding publication set.
- [Platform work items](platform-work-items.md): prioritized platform gaps.
- [Knowledge source retirement](knowledge-source-retirement.md): source revisions, ownership split, archive gates, and current non-destructive status.
- [Live plan health-handler tests](live-plan-health-handler-tests.md): bounded execution evidence for the referenced plan.

Machine-readable mirrors for agent use live under `.ai/contracts`. The canonical shared policy distributed to other repositories lives in `internal/onboarding/templates/v1`; the root `.ai/rules/common.md` must remain byte-identical to that embedded common-rules file.
