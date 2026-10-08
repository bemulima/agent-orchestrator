# Release-bound production certification

Production admission uses a trusted readonly certificate with an out-of-band
SHA256 pin. Model requests/results cannot select the certificate or runtime
identity. Only literal positive conditions, all evidence pins, a supported role,
and exact current runtime identity can pass. Missing evidence remains DENIED.

`runner/src/release.ts` compares all declared source/build/image/policy identities.
The observer runs inside the installed backend, verifies installed regular bytes
and symlink targets against the build inventory, rejects /app bind overlays,
checks the immutable source label and observes current Docker/server/image IDs.
Runtime unavailable is typed mismatch. Seccomp2, no-new-privileges1 and empty
capability effective/bounding sets are required before observing release identity.
Explicit hardened/production mode calls the brokered private-state execution path;
startup rejection never falls back to the legacy path. Resume remains unsupported.

`scripts/build-production-release.py` captures dirty production source by exact
per-file hashes, freezes a separate snapshot and builds only those bytes with
`docker/ReleaseDockerfile`. It does not use HEAD as source identity or commit work.
The image contains an installed binary/runner inventory and actual SDK/CLI/Go
versions. Image IDs/digests and the source manifest form the build chain.
Floating build bases are currently resolved by Docker during the build; the
resulting image digest is the execution pin, not a claim of bitwise reproducible
rebuilds from floating tags. Release-bound certification must retain resolved
builder/base identities before claiming reproducible regeneration.

The first real-model qualification introduces an ordering decision: the existing
mandatory conditions include real_model_canary; general production authorization
cannot precede that evidence. A separate narrowly scoped one-use qualification
grant must be explicitly settled before the first model canary. No such exception
is implemented or activated by this partial implementation. Do not set the evidence
true merely to start the first run. Current production readiness remains NO.


## Owner qualification decision — 2026-10-08

Owner authorizes one qualification execution for the fixed source37a92f8f...
and runner imagefd259c87..., preserving general production DENIED.
The historical BLOCKED certificate8a574b23... is immutable.
`cdo_release_qualification.py` implements a separate trusted control record,
not a modification of that pinned image. The grant binds full runtime identity,
exact disposable fixture/WorkPackage/release attempt, nonce, scope and expiration.
Readonly artifact/controller pins detect stale grants. All existing mandatory
conditions remain literal true except prior real-model-canary evidence.
Atomic exclusive/fsynced consumption is keyed by owner release attempt, so
reissuing an ID cannot permit a second model execution. Failure before consumption
retains the unused grant; consumption is never released automatically.

12 focused tests prove stale/replay/expiry/scope/UNKNOWN failure and one winner
among16 concurrent attempts. The active real grant remains unconsumed because
final exact-route prerequisite evidence is missing. No final canary was run.

Automatic approval review rejected the exact backend SDK startup probe's host
Docker socket mount as broad daemon authority conflicting with owner socket
prohibition. The probe did not execute. No proxy/indirect workaround was used.
Socket-free installed inventory and readonly command Go fixture checks are safe
alternatives; they do not certify full SDK/Temporal production routing.
An explicit owner runtime transport decision is needed before those tests proceed.
Production admission/readiness/pilot remainDENIED/NO/NO.

## Safe runtime transport staging — 2026-10-08

Owner authorized a separate trusted host controller and prohibited Docker socket
mounts in every backend, runner, SDK and command container. The new
`runner/bin/cdo_runtime_controller.py` exposes six typed operations: create,
start, inspect, diagnostics, cancel and cleanup. A trusted host registration
selects one of two finite credential-free recipes: readonly fixture `go test
./...` or the actual SDK CLI `--version`. No request can supply flags, argv,
mounts, networks, labels, container IDs, image tags or daemon administration.

Requests bind execution/WorkPackage UUIDs, release identity, role, immutable image,
scope, fixed resource profile, controller/protocol identities and two deadlines.
Per-execution HMAC authentication, fsynced nonce reservation and locked durable
state reject replay, concurrent duplicate calls and restart replay. A private
ownership marker binds the host state directory as well as the execution; a
failed create in a different controller cannot acquire another controller's
cleanup authority. Cleanup checks the full container ID and exact OCI policy
before removal, then requires positive absence. UNKNOWN invalidates the execution
and never starts a fallback. Docker executable, Unix endpoint and daemon ID are
trusted host configuration and cannot be selected through the API.

The staging HTTP transport is loopback-only. It authenticates responses, limits
concurrent handlers to four and enforces an absolute five-second request-read
deadline. Model containers receive neither runtime keys nor the controller
socket. All finite recipe containers use network none, seccomp builtin, empty
capabilities, no-new-privileges, readonly inputs and bounded private tmpfs.

This foundation is **not the production coordinator route**. Production/hardened
entry rejects `RUNTIME_TRANSPORT_NOT_CERTIFIED` before temporary state,
subprocesses or model execution. The new release image omits Docker CLI. Existing
host certification code is retained; it is not an indirect Docker proxy available
to the backend. The observer that previously called Docker inside the backend
cannot supply positive runtime attestation for this staged image.

Three new mandatory release identities bind installed controller bytes, installed
transport bytes and the canonical protocol descriptor. Old certificates cannot
authorize changed transport code. A fresh immutable candidate must be approved
by its exact source/image identities before any model execution; the expired old
grant remains unconsumed and is not extended or replaced in this stage.

Remaining gates include the complete host coordinator route, authenticated active
host-runtime/daemon attestation, backend/Temporal lease cancellation and controller
crash reconciliation, certified SDK networking, full four-role compatibility,
explicit automatic transport-design PASS, exact candidate owner approval and the
final real model/independent/Temporal/release qualification. Finite unit or OCI
probe PASS does not satisfy these gates. Production admission remains DENIED,
production readiness NO, real repository pilot NO, budget20/enforce.
