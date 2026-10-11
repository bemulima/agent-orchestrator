# Shared agent policy

This file is the canonical, centrally managed global policy template. A
distributed copy is managed provenance and must not be edited independently.
Repository-specific facts belong in that repository's `AGENTS.md` and `.ai`
files; conditional procedures belong in `.agents/skills`.

## Evidence and scope

- Follow the instructions in scope, then establish facts from current source,
  manifests, contracts, tests, and versioned documentation.
- Preserve unrelated user changes. Do not invent endpoints, commands,
  dependencies, symbols, or architecture paths from examples.
- Keep changes within the authorized task and its verified ownership boundary.
  Report required scope expansion as a blocker instead of expanding it silently.
- Keep secrets out of source, logs, prompts, examples, and generated reports.

## Approval and delivery

- Treat repository writes, global configuration changes, external issue/PR
  publication, push, merge, deployment, and destructive operations as separate
  effects that require their applicable explicit owner authorization.
- Inspect the exact diff and verification evidence before proposing delivery.
- Preserve canonical repository knowledge where it is owned. Centrally managed
  assets do not replace repository-local facts or contracts.

## Worktree ownership

- Agents and workers must not create, move, remove, or manage Git worktrees
  directly.
- Only the approved `course-dev-orchestrator` isolation subsystem may manage
  worktree lifecycle.
- Workers operate only in the workspace assigned by the orchestrator.
- Do not create or switch branches unless the user explicitly authorizes it.

## Managed policy and skills

- `AGENTS.md` is the always-on local instruction entry point.
- `.agents/skills` contains conditional procedures; load a skill only when its
  task applies.
- `.ai` remains repository-owned knowledge: service, architecture, contracts,
  commands, and testing capability.
- Never replace an unlisted/local skill with a managed copy. Respect the
  managed-asset provenance manifest and report drift or conflicts.

## Future development tasks

Before any new development task, inspect repository and operation specific
workflow evidence. Use the Task Route future-task decision procedure from the
managed skill (or the repository's documented canonical source).

- READY_VERIFIED with matching scope, installed rules, permitted execution and
  no blocking gate: apply existing rules automatically, without new setup.
- PARTIALLY_IMPLEMENTED, BLOCKED or NOT_STARTED: explain what this task needs
  and wait for the owner's choice: finish only required setup, use an existing
  permitted ordinary development path, or stop. No writer subtask, migration,
  setup or delivery commit may start before that choice.
- CANARY_VERIFIED requires exact canary scope and current execution evidence;
  it never implies unrestricted readiness. Unproven coverage requires a choice.
- UNKNOWN: bounded read-only discovery, then disclose remaining uncertainty
  and wait. Never treat missing evidence as READY.
- Reuse an explicit choice for the same task, repository and requirements.
  Reevaluate when they change; do not ask to reinstall verified infrastructure.
- Owner choices cannot bypass production admission or another security gate.
  Ordinary development never silently selects an uncertified CDO backend.
- Advanced runtime certification is optional and resumes only with a fresh
  explicit owner decision. Distribution outside the approved repository needs
  an exact dry-run diff and separate owner approval before application.
