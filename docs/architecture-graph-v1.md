# Portable Architecture CURRENT graph v1

CDO owns `architecture-graph.v1` and the existing stable service/relation IDs.
The JSON Schema is [architecture-graph.v1.schema.json](schemas/architecture-graph.v1.schema.json).
The checked-in exporter fixture under `internal/architecturecatalog/testdata/`
is synthetic test data; it is not fleet evidence.

`make architecture-export-build` produces the separate read-only
`.cache/bin/architecture-export` command. It reads the persisted CURRENT
selection and exact regular-file Git blobs, writes compact JSON to stdout,
and never rebuilds topology, scans working-tree file content, checks out a
branch, or changes a source repository. The existing DATABASE_URL and
REPOSITORY_ALLOWED_ROOTS/REPOSITORY_STORAGE_PATH configuration governs access.
The binary requires Git build provenance (`vcs.revision`, `vcs.modified=false`)
from committed CDO source. A dirty/development binary cannot label itself as
a pinned producer. Producer code SHA and artifact storage SHA are independent;
external storage pins are captured after publishing the immutable artifact.

References are the actual CURRENT service/repository nodes, sorted by
`reference_id`. Each retains canonical source identity, manifest ID (or the
literal `repository` for uncovered sources), role, owner commit SHA, coverage,
CURRENT state, dirty state and declaration pins. Edges retain `edge_id`, the
CDO relation enum, declaring source and resolved target references, literal
external target, operation, transport, contract and direction. Inbound edges
still identify the declaring owner as source; direction determines traffic
orientation. Resolved relations can also retain their authored external
literal. Distinct parallel operation/contract edges remain distinct.

Declaration pins contain `source_identity`, full `commit_sha`, relative `path`,
Git `blob_oid`, and raw-byte `content_sha256`. Git lazy fetching is disabled for every object read; a missing promisor object remains unavailable without a network operation. References carry the validated
service/operation declaration bundle; paired edges carry both endpoint
bundles. These are conservative declaration provenance bundles. Merged local
evidence paths alone cannot identify the owning repository, so the exporter
does not assign those paths to guessed owners. Missing immutable files or
metadata, digest mismatch, dirty/non-CURRENT sources, incomplete declarations,
and unresolved targets produce explicit BLOCKED diagnostics. No owner
responsibility or required-dependency flags are inferred.

`completeness` preserves existing CDO counters. Diagnostics use severity,
code, optional reference/edge/path, and message. They sort by severity, code,
reference, edge, path and message, with exact duplicates removed. Pins sort
by source identity, path, blob OID, commit and content hash. Object keys sort
recursively; arrays retain those defined orders. `content_sha256` is SHA-256
of compact UTF-8 JSON for the whole semantic payload with that field omitted.
String escaping matches JSON.stringify; UTF-8 text and U+2028/U+2029 are
literal, HTML characters are not escaped. The optional output newline does
not enter this semantic digest; external artifact pins hash the exact stored
bytes, including a newline when present.

## Independently declared fleet scope

The default scope is only the captured CURRENT topology. Its populated nodes
cannot prove fleet completeness, so absent inventory emits
`FLEET_SCOPE_UNPROVEN`. The optional graph `inventory` contains the verified
CDO document pin and sorted source identities. The source document follows
[architecture-graph-inventory.v1.schema.json](schemas/architecture-graph-inventory.v1.schema.json):
`schema_version: architecture-graph-inventory.v1` and `source_identities`, a
nonempty sorted unique list of actual persisted canonical repository IDs.

An owner-approved inventory must be committed in the CDO producer revision.
Use `.cache/bin/architecture-export --inventory-root <CDO-object-store>
--inventory-path <owner-inventory-path>`. The resolver reads that file at the
independently pinned producer commit, verifies CDO owner identity, object type,
blob/content digests and strict document shape (including duplicate-key rejection), and compares its identities
exactly with exported sources. A mismatching/unverified inventory stays
BLOCKED and is not promoted into the artifact. A matching inventory removes
only the scope diagnosis; unresolved edges or missing owner files still block.

No actual full-fleet inventory or graph is supplied by this change. Prior
37-root local rollout verification allowed untracked architecture files and
does not prove commit/blob pins. Capture an owner-reviewed CDO topology/source
set with each exact owner SHA, resolve declarations from immutable objects,
and explicitly review the resulting inventory and downstream Central lock
update before claiming authoritative platform gate evidence.

## Exact owner fleet inputs

The separate `--fleet-inputs <lock.json> --fleet-roots <roots.json>` mode uses
[architecture-fleet-inputs.v1.schema.json](schemas/architecture-fleet-inputs.v1.schema.json).
The normalized lock contains exactly 42 repositories sorted by canonical
`source_identity`; each supplies `repository_id`, canonical HTTPS `remote_url`,
full published `commit_sha`, owner `profile`, `service_id`, `repository_role`
and declarations sorted by path with exact `blob_oid` and `content_sha256`.
The root map contains exactly the same source identities mapped to local Git
object stores. `REPOSITORY_ALLOWED_ROOTS` (comma separated) and optional
`REPOSITORY_STORAGE_PATH` constrain access. No DATABASE_URL is required.

Every service and operation is parsed by the existing architecture/v1 validator
from its locked regular-file Git blob. Each service has exactly one declaration,
and its complete operation list must match the lock. Dirty working-tree files,
branch tips and database selections do not affect this mode. Duplicate JSON keys,
unknown fields, incomplete inventories, mutable revision names, forbidden paths
(including every test-results component), blob drift, and missing objects fail
export. The producer still requires clean committed build provenance.

The graph retains architecture-graph.v1 IDs, relation semantics and canonical
serialization. Its `fleet_inputs` field binds the canonical semantic lock digest
and all 42 source identities; local roots and input whitespace do not enter the
digest. Owner declarations at those exact commits select CURRENT in this mode.
The persisted exporter remains available for database CURRENT snapshots. Explicit
unresolved owner targets remain BLOCKED until authoritative endpoint evidence
resolves them. The lock is input evidence, independent of graph artifact storage.

Pinned projection does not manufacture unknown consumers for provider-only
contracts or unknown subscribers for published events. Fully unknown operation
scaffold interactions with zero confidence remain unasserted semantic metadata;
`operation_semantic_debt` records their count. Any named or partly known outbound
assertion remains an edge and stays BLOCKED when unresolved. Reviewed exact
external resource literals can resolve only direct relations with positive
confidence and owner evidence. Such references carry `reference_kind: external`,
identity `external:<transport>:<literal>`, stable IDs and the union of the owning
committed declaration pins. The reference commit is the first canonical owner
pin commit, rather than an invented commit in an external repository. Repository
inventory continues to contain exactly 42 Git source identities independently
of those external references.

Opaque provided/consumed interface labels and published/subscribed event labels
without an exact counterpart do not assert an unknown endpoint in pinned mode.
`excluded_interface_metadata` counts those unmatched descriptors; their original
owner declaration pins remain attached to repository references. Exact matched
contract/event tuples still produce relations. Explicit outbound interactions
with unknown targets stay BLOCKED. Fleet-specific compatibility checks account
for concrete client calls and event operations independently of these labels.

Fleet mode can additionally use paired `--inventory-root` and `--inventory-path`
to bind the independently committed architecture-graph-inventory.v1 document at
the clean producer revision. The inventory contains only the sorted 42 owner
source identities, so it can be committed before reading owner source SHAs and
storing the graph elsewhere. An unavailable, invalid or mismatching supplied
inventory fails or remains BLOCKED; external references do not expand that scope.


### Pinned owners outside the fleet

A reviewed outside owner can be supplied in optional `external_owners`, sorted
by real Git `source_identity`. These rows use the same exact published commit
and service/operation declaration pin bundle as repository rows, with required
`classification: PINNED_EXTERNAL_OWNER_INPUT`. An external owner's `profile` is
optional; the 42 repository profiles remain required. Source identities and
service IDs must be unique across both cohorts. The root map contains exactly
both cohorts. The canonical fleet digest includes the external owner rows.

The exporter resolves an explicitly authored target only by the exact outside
service ID and emits its reference as `reference_kind: external_owner` with the
required classification and real immutable declaration pins. This is distinct
from resource references (`reference_kind: external`).
`fleet_inputs.external_source_identities` records the sorted outside cohort.
Inventory, completeness and semantic debt counts continue to describe only the
42 fleet repositories. Outside declarations participate in exact relationship
matching and retain their explicit unresolved dependency diagnostics.

Provider compatibility and ancestor source/document evidence are independently
verified inputs to the compatibility gate. They are not service/operation
declarations and are not included in graph declaration bundles. Supplying an
outside owner does not imply successful compatibility or readiness.
