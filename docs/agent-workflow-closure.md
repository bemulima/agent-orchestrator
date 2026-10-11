# Agent development workflow closure

Owner decision: 2026-10-11. Runtime/production certification is deferred.
PRODUCTION_ADMISSION_DECISION=DENIED; PRODUCTION_SANDBOX_READY=NO; budget=20/enforce.
No certification attempt, host probe, Docker/Temporal action or model qualification
is scheduled by this closure. Resumption requires a fresh explicit owner decision.

## Capability evidence and scope

The single machine-readable inventory is
[agent-system/manifest.yaml](../agent-system/manifest.yaml), workflow_capabilities.
It uses the six owner-defined status values. Managed distribution carries that
central evidence into its existing generated provenance manifest; it does not
overwrite .ai facts or certify target repositories.

Task routing, contract planning, architecture profile definitions, shard planning
and the pure managed asset proposal/approval model are implemented. Filesystem
distribution remains PARTIALLY_IMPLEMENTED: the prior preservation review
retains four source P1 findings (sensitive target reads, source admission before
I/O, symlink races, unbounded target inventory/diff). They are not waived or
remediated by this closure. Real target plan/apply is not READY. Production
runtime is blocked.
The Go availability canaries prove contract freeze, three layer workers, fan-out,
barrier, serial composition and independent verification only in their saved
disposable scope. General WorkPackage execution remains partial: Go commands
and availability-specific intents do not establish arbitrary Go or frontend
worker support. Next.js has profiles/routing, with execution support incomplete.
Ordinary configured local runner source exists; current runtime/auth availability
is UNKNOWN because this closure does not probe it. An already established safe
ordinary development path can be used with the applicable task authorization.

Existing composition canary receipt:
`.cdo-live-freeze-canary-20261001-a74e/evidence/20261004-composition-required/http-remediation-final/FINAL_COMPOSITION_REMEDIATION_RESULT.json`
(relative to the workspace). Its baseline is
`f4a809842678c4bad2605c0852d9fd12a3ced98d` and integration assembly is
`a61d04223b6885be0cf5769d1b42fff9fddb4c4a`; reviewer PASS,
INTEGRATION_VERIFIED, CANARY_ONLY, production readiness NO. The saved canary
reports overlap of two workers; it does not prove generic production execution.

The latest exact Temporal absence receipt is
`.cache/sandbox-certification/temporal-absence-host-20261010-01/temporal-reconciliation-receipt.json`: receipt
`48751a6f-e655-4447-8acc-e4d4b3847a26`, PASS, read-only, zero mutations,
exact absence true, original evidence unchanged. The original UNKNOWN attempt
and historical failures remain unchanged. This closes that exact ownership
observation; it does not certify the full runtime.

## How future tasks start

Repository AGENTS.md loads the canonical
[future-task decision procedure](../agent-system/skills/task-route/references/future-task-decision.md).
READY uses existing rules. PARTIAL/BLOCKED asks finish required setup / use an
existing permitted development path / stop, and waits. NOT_STARTED asks minimum
setup / ordinary development / defer, and waits. UNKNOWN gets bounded read-only
discovery, then waits if unresolved. Existing explicit choices are reused only
for the same repository, task and requirements. All security gates remain.

The pure decision model and guarded callback are tested with actual fixture
writes: unresolved decisions cause zero writes and zero callback invocations.
Owner choices also bind required SHA-256 evidence fingerprints; changed scope,
profile, source/live evidence or permissions invalidates the saved choice.
The helper guards dispatchers that invoke it; existing legacy APIs are not
claimed to have been retrofitted. Interactive agents use the always-on policy.

Examples:

| New task | Decision |
| --- | --- |
| Go operation with matching READY evidence and permitted execution | Apply existing rules; no installation |
| Go operation requiring a missing worker capability | Ask the three concrete choices; wait before any writer |
| Next.js repository documented as unconfigured | NOT_STARTED; ask minimal setup / ordinary / defer |
| Uninspected repository | Bounded read-only discovery; UNKNOWN never allows setup |
| Operation requiring the uncertified production backend | BLOCKED while admission DENIED; ordinary choice cannot bypass it |

## Contract-first parallelism

Task Route → minimum shared contracts → reviewed frozen baseline → exact
WorkPackages → independent scoped semantic RED/implementation/GREEN → verified
shards → barrier → serialized approved wiring → deterministic integration →
independent read-only reviewer. Contracts and sibling scopes are read-only.
Workers never manage worktrees or commit; trusted orchestration owns both.
Unsupported routes and contract changes require owner replanning.

## Distribution and publication

Canonical global policy and reusable skills use the existing asset distributor
and its exact proposal fingerprint. The local always-on checkpoint is active.
Global/unrelated-repository installation is not applied by this task. Its exact
dry-run diff is retained for a separate owner decision; the global installer
remains fail-closed. No .ai facts or unlisted local skills are replaced.

Publication uses the existing source-preservation branch
`ai/task-course-dev-orchestrator-cleanupprese`, parent
`592f9e49e7823c4c32153676f15b5bfa91254ac6`, for finished policy/source-only
changes. It preserves that branch's published RAG source and the shared main
checkout/index. Source-only preservation is not production admission or main
promotion. Exact commits, verification and remote receipts are retained in
`.cache/agent-workflow-closure/20261011-01`.

Ambiguous RAG documentation changes, testing evidence deletions and mixed-owner
shared metadata remain untouched. Local main diverges from origin/main; no
blind push, merge or rebase is authorized by ownership alone. Unseparable
partial runtime work remains in an immutable checkpoint, not READY publication.

## Deferred resource task

`CDO_RESOURCE_RETENTION_AND_GARBAGE_COLLECTION` is a separate backlog item:
exact ownership inventory, resource report, dry-run cleanup, per-execution
cleanup, evidence retention and safe cache policies. No global Docker prune.
No resource deletion or GC implementation occurs in this closure.
