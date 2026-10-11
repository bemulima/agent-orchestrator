# Future-task owner decision

## Read-only discovery

Read repository AGENTS.md, applicable .ai facts, installed managed provenance
and skills, architecture profiles, relevant source/tests and existing evidence.
Use the canonical manifest's workflow_capabilities when available. It describes
central implementation availability, not target readiness. Distribution asset
states (missing/current/stale/conflicting) describe provenance, not execution.
No advanced runtime or model call is needed to classify or ask the owner.

Record the exact repository, task identity, required capabilities, applicable
profile/routes, coverage evidence, permitted execution path and safety gates.
Evaluate each required capability for this operation; do not extrapolate Go
availability canaries to general Go, Next.js or another repository. An absent
local installation with a documented unconfigured stack is NOT_STARTED; an
uninspected or ambiguous installation is UNKNOWN.

## Decision barrier

- **READY_VERIFIED**: all required scope, evidence, repository, permission and
  installation checks match. Apply existing rules automatically. No setup.
- **CANARY_VERIFIED**: preserve the canary classification. Exact covered and
  permitted operations need an explicit task-specific READY classification from
  current evidence before the helper can proceed. The helper conservatively
  waits for owner choice on a CANARY_VERIFIED input. Wider operations require
  an owner choice.
- **PARTIALLY_IMPLEMENTED / BLOCKED**: explain ready parts, missing parts and
  precisely which are required. Ask: «1. Сначала завершить только необходимые
  части агентной системы; 2. Выполнить задачу существующим разрешённым способом
  разработки; 3. Остановить задачу». WAIT for an explicit answer.
- **NOT_STARTED**: explain the proposed Task Route → Contracts → Layer Agents
  → Verification workflow. Ask: «1. Настроить минимальную необходимую систему;
  2. Продолжить обычную разработку без установки; 3. Отложить задачу». WAIT.
- **UNKNOWN**: bounded read-only discovery first. If unresolved, disclose what
  is unknown and ask before any write. UNKNOWN never authorizes agent setup.

During WAIT, no implementation, setup, writer agent, migration or delivery
commit for the new task. Silence or elapsed time is not a choice.

Choice 1 authorizes only known, required missing capabilities with a permitted
setup path. Verify them, refresh evidence, then proceed with the main task.
Choice 2 authorizes only an established permitted ordinary development path
and existing project rules. It does not grant CDO canary, legacy or production
backend permission. If no safe path exists, explain the blocker and ask for a
separate decision. Choice 3 stops. Reuse a task-submission override or existing
explicit choice; bind it to repository, task identity and capability requirements.
Changed requirements invalidate that choice. Do not re-ask unchanged approvals.

Production DENIED and other security prohibitions override all three choices.
Optional sandbox certification cannot become automatic setup for ordinary work.

## Contract-first execution

For authorized implementation: route responsibilities, freeze required minimum
shared contracts, assign exact non-overlapping file/symbol scopes, then run
independent workers from one frozen baseline. Require meaningful semantic RED
before implementation and independent GREEN before verified shard commits.
Shared contracts and siblings remain read-only. Workers never manage worktrees
or create commits; the trusted orchestrator owns lifecycle and delivery.
Reach the barrier before serialized composition; composition performs approved
wiring only. Integrate deterministically and use an independent read-only
reviewer. Unsupported routes or contract changes stop for owner replanning.

## Machine-readable decision

The agentcontrol EvaluateFutureTask helper is pure and binds owner choices to
repository, task and requirement fingerprint. Every requirement includes an
exact evidence_fingerprint: trusted callers hash scope, profiles, evidence
content, execution requirements, repository/revision, task operation and current
permission/verification evidence, and refresh it before the decision. Missing
or changed fingerprints fail closed; the helper does not inspect sources.
RunFutureTaskAction rejects a
guarded callback before any write when the decision is unresolved. It must be
called by a dispatcher to guard that dispatcher; it does not retrofit every
legacy API. Always-on policy is the entry barrier for interactive agents.
