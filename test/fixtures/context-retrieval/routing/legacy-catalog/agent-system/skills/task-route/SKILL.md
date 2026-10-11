---
name: task-route
description: Route a task to evidence-backed architectural responsibilities, concrete repository targets, contracts, and a meaningful verification boundary.
---

# Task route

Answer **where in this repository architecture the task belongs**. This skill
does not create task IDs, build the execution DAG, implement business code, or
expand the authorized scope.

## Evidence procedure

1. Read the repository `AGENTS.md` and identify the repository's ownership and
   local always-on constraints.
2. Read `.ai/service.yaml`, `.ai/architecture.yaml`, and `.ai/commands.yaml`
   when present. Inspect only relevant `.ai/contracts/**` files. These are
   local repository facts; do not substitute central profile examples for
   missing local evidence.
3. Select the applicable central architecture profile from verified stack and
   repository evidence. For a multi-architecture repository, state the chosen
   variant and evidence. If no profile applies, report that uncertainty.
4. Inspect source tree, imports/call sites, interfaces, registrations, tests,
   and migrations as needed. A profile path is only a candidate: name a
   concrete directory or symbol only after confirming it exists and owns the
   behavior. Do not infer that a port has one conventional location.
5. Identify existing contracts to inspect and the narrowest meaningful test
   boundary. State adjacent routes that should remain untouched.
6. Return a compact routing result using
   [`references/routing-result.example.yaml`](references/routing-result.example.yaml).
   Include evidence and confidence. No synthetic task ID is permitted.

## Routing rules

- Route by architectural responsibility, not by the file extension alone.
- A domain invariant routes to `backend.domain`; an application/business
  process to `backend.usecase`.
- Incoming HTTP behavior routes to `backend.transport.http`; message consumer
  or transport behavior to `backend.transport.message`.
- SQL/storage implementation routes to
  `backend.infrastructure.persistence`; outgoing HTTP calls to
  `backend.infrastructure.client`; publishers/adapters to
  `backend.infrastructure.messaging`.
- Schema evolution routes to `backend.migration`. Dependency injection,
  route/subscription registration, and process wiring route to
  `backend.composition` and are shared hot-spots.
- Next.js route entry points, module internals, shared BFF/UI, app runtime, and
  i18n follow `nextjs.common`; select `student` or `admin` only from repository
  evidence.
- If a behavior spans independent routes, recommend the
  `contract-plan` skill. Do not split work or define task identifiers here.
- If evidence conflicts, a target is missing, the test boundary is unclear, or
  composition is shared, report a blocker or uncertainty rather than guessing.

## Scope boundary

Return routes, concrete targets, contracts to inspect, preferred verification
boundary, avoid-list, evidence, and confidence. Do not create a task, dependency,
branch, worktree, commit, contract redesign, or implementation.
