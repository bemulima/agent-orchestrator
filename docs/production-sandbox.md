# Production sandbox hardening

## Admission status

`PRODUCTION_SANDBOX_READY=NO`. The functional canary remains `CANARY_ONLY`.
This stage implements scoped requests, bounded runner failures and a probeable
OCI command boundary candidate. It does not certify the SDK execution backend.
`CDO_SANDBOX_EXECUTION_MODE=production` refuses launch before constructing Codex.
No untrusted request or model result can override this admission gate.

## Actual previous runtime

The audited image was `cdo-canary-20261004-worker:composition-remediation-v8`.
Container UID/GID100:101, cap effective/bounding/ambient0, no-new-privileges1,
outer seccomp0/unconfined, no memory/CPU/PID/tmpfs limits. The shared `/data`
volume and disposable source checkout were writable in the trusted container;
Codex auth was a readonly bind outside model command visibility. Docker socket
and host home were not mounted. The trusted container has platform/service
credential environment names; their values were never captured.

The actual managed command used a temporary root, minimal readonly `/bin`,
`/etc`, `/lib`, `/sbin`, `/usr`, tool runtime, readonly module cache and assigned
workspace. Shared `/tmp` and the configured verification TMPDIR were writable.
Other source/worktrees/auth/home/socket were invisible. The runner explicitly
filters command environment; a direct `codex sandbox` invocation without that
trusted environment policy inherits its caller environment and is not an
acceptable production entrypoint. This distinction is recorded in the audit.

Bubblewrap retained new-session, die-with-parent and user/PID/network namespaces.
Its helper installed command seccomp before execution. The known `/proc` mount
was removed by the narrow fallback, so `/proc/self/status` was unavailable in
that command. Exact original/effective arguments are in the audit evidence.

## Threat classification of the previous runtime

| Threat | Status | Evidence/limit |
|---|---|---|
| Read other repository/worktree | PARTIALLY_BLOCKED | Direct paths invisible; shared temporary/cache files remain a cross-worker channel |
| Read Codex auth/host home/SSH/Docker socket | BLOCKED | Not exposed to managed command profile; no secret contents read |
| Read unrelated environment credentials | BLOCKED | Trusted SDK command allowlist; direct CLI inheritance is unsafe |
| Internet/unrelated localhost/Docker network access | BLOCKED | Managed profile disables networking and unshares network namespace |
| Worktree lifecycle/source checkout mutation | PARTIALLY_BLOCKED | Metadata/source hidden or readonly; command policy is not a standalone Git syscall filter |
| Frozen/sibling/composition path writes | NOT_BLOCKED | Old coder profile granted the whole assigned tree; detection happened afterward |
| Symlink/absolute/parent escape | PARTIALLY_BLOCKED | Existing adapter verification; full old adversarial runtime certification absent |
| Mount/create privileged namespace | PARTIALLY_BLOCKED | Capability-free command/helper confinement; outer container seccomp unconfined |
| Inspect/signal unrelated processes | PARTIALLY_BLOCKED | User/PID isolation present; no full adversarial certification of old runner tree |
| Background daemons/resource exhaustion | NOT_BLOCKED | No per-execution cgroup or storage quota |
| Persist outside owned paths | NOT_BLOCKED | Shared writable temporary directories, unbounded cache growth |

## Trusted/untrusted boundary

The approved orchestrator produces `AgentSandboxScope`; it is not parsed from
model output. Contract Agent receives only approved contract files. Layer
assessment is readonly; RED receives only test files; implementation receives
only owned production files. Composition receives approved cmd files, with
separate test-only RED and wiring-only implementation scopes. Reviewers receive
no writable workspace paths. Other roles retain the legacy readonly profile.

The adapter validates exact paths, canonical roots, protected names, symlinks,
hardlinks and role alignment. Trusted CLI overrides make the workspace readonly
and grant exact files; no caller-supplied network/capability/mount/seccomp flags
are accepted. SDK0.144.6 flattens configuration keys; filenames are quoted as
complete dotted TOML keys. Static managed requirements remain upper bounds.
These configuration unit tests do not substitute for kernel-level SDK probes.

## OCI command candidate

Machine policy: `docker/sandbox/profile.v1.json`.
Trusted argument builder: `runner/bin/cdo_oci_profile.py`.
Each disposable command has its own container PID/mount/network namespaces,
readonly root and assigned workspace, UID/GID100:101, `cap_drop=ALL`,
no-new-privileges, Docker builtin seccomp and network=none. Only exact existing
owned files receive writable binds. No home/auth/SSH/source/sibling/Docker
mount is admitted. Environment is reset with `env -i`; global/system Git config
points to a readonly empty runtime file. The command image removes Git and its
helpers; a readonly deny shim returns126 for every invocation, including absolute
paths, scratch Git init and global-config environment overrides. This closes the
literal Git-tool gap independently of missing remotes/credentials. Trusted
orchestrator Git utilities remain in its separate runtime.

The builtin seccomp allowlist permits ordinary file IO, memory management,
thread/process creation without privileged namespaces, clocks, synchronization,
signals within the process namespace and ordinary IPC. It blocks privileged
namespace/mount, ptrace/kernel administration categories subject to Docker's
versioned defaults. Socket creation can occur, but network=none prevents
external/unrelated service connections. Docker29.8.1 was recorded for these
probes; an explicit pinned allowlist or renewed version-specific certification
is required before promotion. See [Docker seccomp documentation](https://docs.docker.com/engine/security/seccomp/).
No syscall exception, privileged mode or CAP_SYS_ADMIN was added. Docker reports
seccomp/cgroupns only; AppArmor label is unavailable. Accepting an AppArmor flag
without an observed enforcing label does not establish LSM enforcement.

Limits:2 CPUs,2GiB memory/no additional swap,128 simultaneous PIDs,256 descriptors,
no core files,16MiB per file,128 writable files and256MiB ephemeral tmpfs. Scratch
is executable for Go tests, with nosuid/nodev, and disappears with the container.
Go compiler concurrency is2 and module acquisition is offline. No PostgreSQL
network capability is granted; real database verification remains a trusted
orchestrator responsibility as in the proven workflow.

The trusted SDK runner has a20-minute deadline,1MiB output bounds,512KiB result
bound,256 command starts and1MiB command output bound. Overflow cancels execution
and reports failure; Unix cancellation kills the process group and bounds pipe
wait. Escaped setsid descendants still require per-execution OCI cgroup teardown.
The candidate argument builder alone is not a production command broker; its
wall-time/output/descendant guarantees must be attached to SDK execution.

## Proc fallback

Only an exact known `bwrap: Can't mount proc on /proc` or `/newroot/proc` denial
is recognized. Exactly one `--proc /proc` pair may be removed. User, PID and
network isolation must be present; all other flags remain unchanged. Namespace,
compiler and generic permission errors never trigger a weaker launch. Tests
prove missing isolation and unexpected proc arguments fail closed.

## Verification and blockers

`make sandbox-test`, runner tests and full repository verification cover the
trusted scopes, role/phase separation, traversal/link rejection, resource failures
and fallback. `scripts/probe-production-sandbox.py --image <trusted-image>
--output <evidence.json>` runs credential-free actual image probes, allowed source
writes/Go tests and independent host result verification. It creates fixture
files, not Git worktrees. Model execution is not simulated by this command probe.

Remaining blockers:

1. Docker builtin seccomp blocks current Bubblewrap user-namespace bootstrap.
   The narrow proc fallback cannot and must not remove namespace isolation.
2. The verified OCI command candidate is not connected to Codex SDK commands or
   filesystem tools; the existing SDK path still has shared scratch/caches.
3. Exact existing-file binds deny atomic replacement and do not support new-file
   creation. A bounded staging/publication protocol must preserve exact scope,
   frozen hashes, RED tests and orchestrator-only commits before attaching it.
4. Per-execution cgroup teardown, cumulative subprocess/storage accounting and
   SDK interruption behavior need end-to-end adversarial verification.
5. No minimal model canary has run under a certified production boundary.
6. The OCI candidate retains Docker readonly sysfs/image visibility; a minimal
   runtime mount view and approved offline dependency/reference inputs need
   end-to-end verification. Sysfs network device names are not namespace-local;
   network isolation probes therefore check namespace-local interface flags, IPv4/IPv6 routes and actual connects. LinuxKit adds dormant tunnel devices; all non-loopback interfaces were down and there were no non-loopback routes. The earlier name-only probe failure is preserved in evidence.

The next slice is the trusted SDK command/filesystem broker and its bounded
publication protocol, followed by a minimal disposable agent canary. No real
repository pilot is authorized or safe at this stage. Functional Plan/Freeze/
Shard/RED/GREEN/barrier/composition/assembly orchestration was not redesigned.

Final command-image probe:42 invariants PASS, allowed Go test PASS, independent host file verification PASS. Four actual role-profile probes PASS. This is a command fixture, not a model run. The random absent-PID signaling probe only demonstrates ESRCH; it does not certify signaling against a known unrelated process or daemon cleanup. Resource configuration checks do not certify cumulative storage/subprocess limits or all limit-breach behavior. Those remain production blockers.


## 2026-10-05 SDK certification slice

The previous unattached-candidate blocker is narrowed: owner-authorized
`hardened-certification` now runs the pinned SDK with an isolated CODEX_HOME,
`include_local=false` environments and a single authenticated loopback HTTP MCP
broker. Native shell/apply_patch/view_image are absent from the actual registry.
Stdio MCP cannot start without a local environment; archived failing probes led
to HTTP transport. MCP tools are namespaced/deferred; annotations describe the
closed-world bounded operations and approval is scoped to those trusted methods.

The broker snapshots bounded regular source inputs (protected directories,
credentials, links and caches excluded), creates a root-owned readonly proposal
view and executes model argv as UID100 in the credential-free OCI profile.
Only trusted proposal operations can replace that view. Source edits go through
exact-path validation, original hash preconditions, directory descriptors with
O_NOFOLLOW, bounded UTF-8 regular files, deterministic ordering and atomic
replacement. New files in existing approved parents work; deletion requires an
explicit trusted delete capability. Reviewer write scope is empty. Source
publication is1MiB aggregate/256KiB per file/128 files; proposals are private.

Each command shares only its own execution's tmpfs/cache. Kernel PID/memory
limit events and scratch file limits are checked by trusted code; inspection
failure poisons the execution and denies publication. Command/output/time/
proposal-limit failures remove the container. The trusted detached HTTP broker
monitors runner parent death, closes OCI PID1's sole stdin writer, removes the
container and private control/staging. Normal, failure, setsid child, context
cancellation and process-group SIGKILL probes passed. Full Temporal cancellation
and all daemon outage/crash cases are not certified.

Two metered actual AgentRunner model runs used gpt-5.6-sol/low at20/enforce.
Both proved semantic RED, approved proposal, GREEN, denied probes, atomic
publication and independent OCI GREEN. The trusted Git adapter created only
bare commit objects (no branch/ref/worktree/delivery). Final baseline
4bfb53342f28afc5d3a1d8fb856c109e03744c07; verified commit
 e99814dda1ad9f818e436048c749af69868927d1. Four role profiles also passed through
the actual SDK/runtime using a deterministic fake model transport.

**Certification remains PARTIAL; production admission stays closed.** An actual
adversarial command launched300 sequential children while the broker command
counter recorded1. Simultaneous PID bounds do not enforce cumulative fork/clone
starts. SDK goal/session/skill metadata outside OCI is not fully quota-certified.
Resume requests explicitly fail HARDENED_RESUME_NOT_CERTIFIED; trusted bounded
checkpoint import is missing. Readonly dependency/reference provisioning and
release binding also need certification. The production gate treats every
missing certification as failure and never converts UNKNOWN to PASS. No pilot,
product execution, full functional replay or production deployment occurred.

Evidence: workspace `.cdo-sandbox-certification-20261005/evidence`.
Commands: `sandbox_sdk_probe`, `sandbox_broker_lifecycle`,
`sandbox_sdk_cleanup`, `sandbox_model_certification` in the command manifest.


## 2026-10-05 final certification continuation — SDK storage blocker

Owner revised resource policy: exact sequential fork/clone counting is optional; concurrent PID exhaustion must remain blocked. No privileged instrumentation added. Production remains denied.

Optional SDK goals, orchestrator skills and user-input tools are disabled in trusted configuration. The actual SDK catalog now contains only cdo tools, MCP discovery/read-resource and unconditional update_plan. Hardened resume returns HARDENED_RESUME_NOT_SUPPORTED; lack of resume support itself is not a readiness blocker.

A deterministic actual SDK probe made20 update_plan calls with controlled600KB explanations. It successfully persisted16911936 bytes/67 private files outside broker limits. No model API was called. The handler emits a session event before the TypeScript caller receives it. The pinned SDK exposes no pre-dispatch tool-registration allowlist hook; the current host private SDK state has no hard filesystem quota. This is a concrete mandatory storage/filesystem-closure blocker. It is not a source path escape. Production cannot be authorized by existing scoped broker/OCI evidence.

Python cleanup now distinguishes a definitely absent container from Docker daemon errors (typed DOCKER_CONTAINER_STATE_UNKNOWN). Publication writes a durable journal outside ephemeral control state; PENDING/FAILED attempts cannot be republished, including after private state removal. A two-file failure after the first replacement is tested: partial disposable content remains, attempt is invalid, no successful runner result or trusted commit may follow. Trusted resource evidence now includes before/after cpu.stat/cpu.max/memory/pids and scratch bytes/files.

Explicit hardened mode is recognized and denied by admission; it cannot fall through to legacy. Admission terminology is ADMISSION_GATE_IMPLEMENTATION=PASS (negative gate tests), PRODUCTION_ADMISSION_DECISION=DENIED. No final production-routed real model call occurs while mandatory closure is failed. External dependency provisioning, full Temporal cleanup, complete adversarial suite and authorizing release artifact are not certified in this blocked continuation. No product repository/pilot, worktree lifecycle, source commit, push/merge/PR or rollout.


Final continuation checks: make verify PASS (24 TypeScript/22 Python tests, Go vet/test, diff check); all four actual SDK role probes PASS;19 OCI lifecycle/resource assertions PASS; actual SDK cancellation/runner-crash cleanup PASS. Candidate executable built with trusted build TMPDIR under repository cache. Final Docker budget query stalled and was terminated; subsequent bounded Docker-version/container-list reads timed out. Current runtime/cleanup/budget refresh is UNKNOWN, not PASS. Last verified budget20/enforce count2; this continuation made0 real-model calls and changed no budget settings. No authorizing release artifact or final production-routed model canary exists.

## 2026-10-05 SDK private-state containment

The certification SDK adapter now launches the pinned 0.144.6 CLI in a unique
OCI namespace per execution. `/execution/sdk-private` is the sole writable
regular-file storage: tmpfs `size=8388608,nr_inodes=256`, UID100/GID101,
noexec/nosuid/nodev. The inode limit counts filesystem objects, including
folders and symlinks. Readonly rootfs, readonly `/tmp` and `/dev/shm`, dropped
capabilities, no-new-privileges and Docker builtin seccomp remain mandatory.
No source, host home, sibling state or Docker socket is mounted. Docker log
storage is disabled. Schema/supervisor and optional authentication are separate
readonly references; authentication is never copied into mutable SDK state.

HOME/CODEX_HOME/TMPDIR/TMP/TEMP and all XDG writable roots are assigned inside
that private mount. Runtime mount inspection rejects unknown writable mounts.
Kernel proc/device/IPC mounts are classified as TRUSTED_RUNNER_STATE, rather
than SDK regular-file storage. Runtime metadata inventories cover session JSONL,
SQLite databases/WAL/SHM, installation ID, temporary aliases/locks and bundled
skills. Paths and allocation metrics are retained; private contents are not.
The JavaScript SDK schema temporary directory is trusted bounded input under
per-execution host control state, cleaned with that execution.

Kernel quotas prevent allocation beyond the bound. The supervisor observes
statvfs to latch typed byte/inode exhaustion, kills the CLI, and reports bounded
initial/peak/final metrics. Observation does not enforce the storage quota.
The orchestrator verifies removal before a completion result can be returned.
SDK failure/overflow invalidates the existing durable publication journal.
Broker parent-death cleanup removes both OCI namespaces and invalidates the
attempt. Failed/PENDING attempts cannot be published again after state removal.

The trusted SDK transport uses host.docker.internal on Docker Desktop; the
broker listener accepts authenticated requests using a fresh execution token.
Only this trusted transport crosses that bridge. Model command OCI networking
remains `none`; source publication remains exact-scope brokered. This is still
certification-only execution; production launch is denied.

Separate evidence counters report SDK_PRIVATE_STATE_BYTES/FILES, SCRATCH_BYTES,
STAGING_BYTES (broker state JSON), cumulative PROPOSED_PUBLICATION_BYTES,
PUBLISHED_BYTES and RESULT_BYTES. Immutable source snapshot storage is separate.
The 8MiB limit allows about1.86 times the largest measured normal-role peak
including the additional forbidden private-path read probes;
256 inodes allow more than twice its normal peak of111 objects.

Admission now requires distinct sdk_private_state_isolated/bounded/cleanup/
source_escape_blocked conditions. The source invariant is
ALL_MODEL_SOURCE_MUTATIONS_BROKERED; isolated bounded SDK internal persistence
is permitted. These flags alone cannot authorize production. Go dependency
provisioning, remaining Temporal/lifecycle certification and release-bound
certification remain later slices. No production routing or pilot is enabled.

Evidence: `.cache/sandbox-certification/sdk-state-*.json` and
`SDK_PRIVATE_STATE_CONTAINMENT_RESULT.md` in that same evidence directory.
