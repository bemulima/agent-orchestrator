# Architecture Manifest v1

Architecture Manifest v1 is the repository-local, evidence-backed description
of one service and its operations. Its schema identifier is
`architecture/v1`. The manifests are descriptive input for discovery and the
owner architecture views; they are not a deployment plan, an authorization
document, or a source of secrets.

Machine-readable schemas:

- [`architecture-v1-service.schema.json`](schemas/architecture-v1-service.schema.json)
- [`architecture-v1-operation.schema.json`](schemas/architecture-v1-operation.schema.json)

## Evidence discipline

Every statement has `value`, a `confidence` between `0` and `1`, and one or
more `evidence` records. Every evidence record names a repository-relative
`source_path`; `symbol`, line span, and checksum are optional refinements.
Use the literal value `unknown` when checked-in material cannot demonstrate a
fact. Do not replace an unknown with an assumption.

Top-level `evidence` is also required and must contain at least one record.
Objects are closed: unknown properties are invalid. The only intentionally
open objects are the `schema` maps in input/result/response schema references.
Those maps describe payload shape, not credentials. Credential-like property
names (`password`, `token`, `secret`, `credential`, API/access/private keys)
are rejected there as well.

## Service manifest

A service document has `kind: "service"`, `schema: "architecture/v1"`, a
stable `id`, and a positive `manifest_revision`. Required sections are:

- `identity`: human name and service kind;
- `purpose`, `responsibilities`, `capabilities`, and `business_rules`;
- `owned_resources`, `inbound_interfaces`, and `outbound_dependencies`;
- `endpoint_groups`, contract/event references, and `operation_manifests`;
- top-level evidence and confidence.

`operation_manifests` contains repository-relative manifest references or
stable operation identifiers. It does not duplicate operation details.
Contract references always include transport, code, direction, and an
evidence-backed description.

## Operation manifest

An operation has `kind: "operation"`, a stable `id`, its owning `service_id`,
and one of exactly five operation types:

| Type | Identity |
| --- | --- |
| `http` | `identity.http.method` and `identity.http.path` (a proven nonstandard uppercase method is valid) |
| `nats_request_reply` | `identity.nats.subject` and role; optional queue/mode metadata |
| `nats_event_subscriber` | `identity.nats.subject` and role; optional queue/mode metadata |
| `worker` | `identity.worker.name` |
| `scheduled` | `identity.scheduled.name` and schedule |

All five use the same evidence-backed sections:

- `access`: audience, authentication, authorization, and idempotency policy;
- `trigger` and `input` (path parameters, query, headers, optional body);
- `business_task`, ordered `business_process`, and `business_rules`;
- `implementation`: router, handler, use cases, domain services, repositories;
- `data_access` and `external_interactions`;
- `side_effects`;
- `output`: responses, optional result schema, emitted events;
- `errors`, top-level evidence, and confidence.

The model intentionally describes effects and policy without granting either.
It never contains passwords, tokens, credentials, private keys, connection
strings, or secret values. Schema maps may describe fields and types but must
not introduce credential-like property names.

## Compatibility and presentation

Manifest v1 is additive to the existing topology and `CURRENT` projection.
Mermaid remains a deterministic presentation and is not part of this model.
Consumers must preserve unknown/unsupported operation kinds as validation
errors rather than silently treating them as HTTP. A revision change is a
new manifest revision, not an in-place reinterpretation of an older record.

## Architecture Control Center lifecycle

`CURRENT` is the verified, immutable catalog assembled from local service
manifests and discovery. `TARGET` is a separate, typed proposal and never
rewrites a CURRENT manifest, discovery snapshot, or source code. Each TARGET
is bound to the exact CURRENT fingerprint from which it was created; its
evidence, deterministic diff, graph impact, and unresolved areas travel with
the proposal.

The owner flow is ordered: create or revise a `draft` against its bound
CURRENT; submit and explicitly approve, reject, or request changes against the
exact proposal revision and fingerprint; then create local command, issue-draft
or Project Plan artifacts from an approved target through the existing planning
pipeline. That integration never publishes an external issue or starts work.

Finally, pure verification compares the immutable approved TARGET with a
fresh CURRENT catalog. Every requested change is `MATCHED`,
`PARTIALLY_IMPLEMENTED`, `DRIFT`, `NOT_IMPLEMENTED`, or
`VERIFICATION_PENDING`; semantic facts that cannot be proved stay explicit
unknowns. The owner UI exposes this alongside the read-only
`PLATFORM → SERVICE → OPERATION` view under `/api/v1/architecture/targets`.
