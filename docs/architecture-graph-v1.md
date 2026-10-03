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
