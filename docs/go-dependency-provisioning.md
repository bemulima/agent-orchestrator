# Trusted canonical Go dependency provisioning

This slice is certified only on disposable hardened-certification fixtures.
Production admission remains DENIED and PRODUCTION_SANDBOX_READY=NO.

## Trusted preparation

The owner/orchestrator supplies canonical assigned repository inputs, approved
SHA256 values for go.mod/go.sum, execution identity and trusted toolchain image.
The declared `go_dependency_prepare` command creates a fresh bundle; an existing
output is never overwritten. Model tools cannot invoke this preparation entrypoint.

Only input declarations are copied into a temporary trusted resolver container.
Its environment is empty except fixed Go settings: official proxy.golang.org,
sum.golang.org, GOENV/GOWORK=off, GOTOOLCHAIN=local, GOVCS=*:off. There is no direct
VCS fallback, credential/home/socket mount or repository code execution.
Network is enabled only in this trusted preparer. Automatic toolchain fetching,
go.work and replace directives are unsupported and rejected for this slice.
Incomplete inputs/resolution/checksums fail closed; go.sum changes during
preparation require approved corrected inputs, rather than silently changing source.

Go mod download all, native go mod verify and module graph enumeration generate
exact path/version/Sum/GoModSum records. The manifest additionally pins toolchain
version and OCI image ID, approved declaration hashes, every module/cache file
SHA256, cache identity, UTC preparation timestamp, repository and execution ID.
Prepared files are0444, directories0555. Links/special files are rejected;
preparation input is bounded to512MiB/20000 files and has a bounded command timeout.

## Execution

Trusted execution configuration supplies CDO_GO_DEPENDENCY_BUNDLE and
CDO_GO_DEPENDENCY_MANIFEST_SHA256; CDO_BROKER_IMAGE must equal the pinned image ID.
A repository with go.mod cannot enter this hardened path without preparation.
Admission verifies the trusted manifest pin, complete file inventory, declaration
hashes and toolchain identity before starting SDK/model execution.

Storage classes:
- IMMUTABLE_MODULE_INPUT: readonly /dependency/modules and manifest.
- PRIVATE_BUILD_CACHE: /tmp/go-build and private temporary test outputs in the
  existing per-execution bounded OCI tmpfs, never mounted into sibling executions.
- SOURCE_WORKSPACE: readonly snapshot/proposal view; source changes use broker.
- SDK_PRIVATE_STATE: existing separate8MiB/256-object SDK OCI state, unchanged.

Canonical Go command environment: GOPROXY=off, GOSUMDB=off, GONOSUMDB/GONOPROXY/
GOPRIVATE empty, GOMODCACHE=/dependency/modules, GOWORK/GOENV=off,
GOTOOLCHAIN=local, GOVCS=*:off, GOFLAGS=-mod=readonly -buildvcs=false -p=2.
Checksums were authenticated in preparation and source go.sum is pinned;
worker native checksum semantics and trusted file fingerprints preserve identity.
Explicit cache/proxy/Go flags/workspace/toolchain overrides and alternate modfile/
overlay modes are denied in dependency-backed command requests. The trusted
canonical verification environment does not accept model-selected dependency paths.
Command OCI network remains none with zero capabilities, builtin seccomp and
no-new-privileges. Missing cache input fails before admission; a deliberately
incomplete diagnostic cache makes offline Go fail (including readonly cache
allocation failure), never enables network fallback.

## Integrity and lifecycle

Manifest/content/source declaration fingerprints are checked before and after
commands and again before publication. Unknown/changed inputs produce typed
GO_DEPENDENCY_* failures and FAILED in the existing durable publication journal.
Verification is mandatory in every broker mode; invalidation never bypasses it.
Model writes to go.mod/go.sum require WorkPackage scope. Even an approved
proposed declaration change stops with GO_DEPENDENCY_REPREPARATION_REQUIRED;
a future owner-authorized trusted preparation cycle is required to continue.

OCI removal destroys private build cache on success, test failure, cancellation
and integrity failure. Trusted immutable bundles may be retained outside model
workspaces under .cache/go-dependencies, owned by the trusted preparer and
revalidated on every use. They are never shared writable model caches.

## Evidence and remaining work

Actual external fixture uses github.com/google/uuid v1.6.0 and Go1.25.1.
Direct four-role OCI checks plus actual SDK Layer/Contract/Composition/Reviewer
probes verify offline tests, readonly inputs, source scope and SDK state cleanup.
Evidence resides in .cache/sandbox-certification/go-dependencies-*.json.

Admission adds four separate Go dependency conditions. Passing this slice alone
cannot authorize production; remaining lifecycle/Temporal and release-bound
certification are later tasks. No product repository or pilot was executed.
