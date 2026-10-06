# Routing and contract control prerequisites

`agent-system/` supplies the canonical versioned asset manifest, global policy, task-route and contract-plan source assets, and Go/Next profiles. `internal/agentcontrol` validates the catalog and derives deterministic checksums. Runtime planning loads the canonical catalog from the explicitly selected CDO directory; the Docker image includes these assets. No distribution apply command is added by this publication.

The planner preserves one task per project while attaching evidence-backed RoutingResult and ContractPlan metadata. Positive, negative, conditional and conflicting route signals remain explicit. Contract ownership can name an owner-only domain route without selecting it as an implementation responsibility. Profile dependency direction, concrete contract locations and existing evidence govern ownership validation.

`internal/contractbaseline` provides deterministic ContractPlan fingerprints, bounded content/import verification and secret-content detection used by retrieval. Its required domain declarations are ContractReference and ContractBaseline models; this publication excludes shard/fanout models, migrations and execution workflows. Approval fingerprints retain SHA-256 of input, NUL separator and output. Legacy version-zero plans remain compatible.

The package tests cover catalog validation, routing polarity, contract ownership, strict import checks, invalidation, serialization and approval fingerprint compatibility. The control primitives do not launch a freeze, shard worker, fanout, model call or foreign repository write.
