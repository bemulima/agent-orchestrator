---
name: contract-plan
description: Freeze the minimum shared contracts needed before independent routed workers implement a cross-layer change.
---

# Contract plan

Use this procedure only when Task Route identifies multiple affected routes
that independent workers must implement. **Skip it** when one isolated route
can safely complete the change.

## Procedure

1. Start from evidence-backed affected routes and repository-local contracts.
   Do not infer a boundary from a profile example or copy `.ai` facts into the
   central control plane.
2. Identify which boundaries are genuinely shared by independent workers.
   Freeze only shared interfaces and observable behavior, never private helpers
   or implementation details.
3. Locate existing canonical ownership. Preserve compatible contracts; define
   only the minimum missing or changed boundary. Place a Go contract inside
   the profile-declared surface for its owning route. Use a neutral contract
   layer only when the repository architecture explicitly defines one. Record
   producers, consumers, types, invariants, errors, compatibility, and evidence
   as relevant.
   A contract owner is not automatically an implementation route. Follow the
   profile's boundary_ownership rule and repository interface evidence, and
   record contract_owner_routes separately from implementation routing. An
   owner-only route requires profile permission and source evidence; it does not
   receive a worker unless behavior in that route is independently requested.
   Keep the application API and repository boundary distinct. In go.canonical,
   HTTP consumes the usecase application API; usecase and persistence consume
   the domain repository boundary. A repository port must not expose application
   DTOs that force a forbidden persistence-to-usecase import. The future usecase
   worker owns mapping between those boundaries; contract freeze defines only
   their behavior-free declarations.
4. Choose only contract types needed by the routes:
   - backend domain types/invariants, application command/result, repository or
     external-client ports, HTTP request/response/error mapping, event/message
     envelope, database/migration semantics;
   - frontend model/type/schema, API adapter, usecase, UI props/behavior,
     i18n input/key, shared UI props, or BFF request/response.
5. Verify the proposed semantics against current source, owned manifests,
   migrations, consumers, and tests. Apply the profile's dependency direction:
   standard-library imports are handled separately, same-module imports follow
   route dependencies, and third-party imports require profile or project
   dependency evidence. Mark unresolved choices as blockers.
6. Publish one reviewed shared baseline with its source revision and checksum.
   This is the freeze point. Independent workers may start only after it is
   approved and their route/scope is assigned by the orchestrator.
7. Workers implement the frozen contract. They must not silently redesign it.
   If implementation requires a contract or out-of-scope change, stop and
   report a blocker with evidence. Replanning and re-freezing happen outside
   the worker.

## Blockers

Use one of these stable codes and attach evidence:

- `CONTRACT_CHANGE_REQUIRED`
- `OUT_OF_SCOPE_CHANGE_REQUIRED`
- `DEPENDENCY_NOT_READY`
- `ARCHITECTURE_CONFLICT`
- `TEST_BOUNDARY_MISSING`
- `BASELINE_STALE`

`BASELINE_STALE` means the approved source revision or contract content changed
after freeze. Do not continue against a stale baseline. The control plane's
minimal validation vocabulary is documented in the Agent Control Plane code;
this skill does not define a persisted ContractPack or database schema.
