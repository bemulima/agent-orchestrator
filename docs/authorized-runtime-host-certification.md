# Authorized host runtime certification

This is the supported operator entrypoint for the pending complete runtime
certification. It is a visible local host operation authorized by the final owner
mandate. Run it in the macOS Terminal with normal owner permissions, outside the
restricted agent execution environment. It does not change that environment's
permissions or use another observer to evade its recorded EPERM.

The default script invocation prints a plan and performs no runtime work.
`--execute` is required. The script uses the existing trusted process observer;
an explicit observation, signal, socket or Docker denial ends the attempt.

## One operator action

Run this command in macOS Terminal. The evidence directory must not already exist.

```sh
python3 /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --execute \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261009-01 \
  --go-bundle /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/go-dependencies/go-deps-fixture-pcs74yoa \
  --go-bundle-manifest-sha256 9014d919474b6917fc254ab534e8d9d24b2ac0b0a87ca26400c8b8b5a8f3eb76 \
  --command-image sha256:a63ad55c3990b47bbe62b51de214e3668936f79b3b55800ef2e3bf63c7591653 \
  --sdk-image sha256:7bc55a3d9834c84e3d411e2450338fb466a536f736869087515beec72d68cdfe \
  --temporal-image auto-local
```

The two worker image IDs and dependency manifest above are preserved certified
inputs, not a claim that they are currently observable. Preflight checks them.
`auto-local` selects exactly one installed `temporalio/temporal:` image by its
immutable ID. No image is pulled automatically. Missing or ambiguous images
produce an explicit infrastructure failure before Temporal creation; a later
operator invocation can supply an exact local Temporal CLI image ID instead.

Required permissions are process observation through the existing trusted
`/bin/ps`, signals to a freshly created and positively owned disposable group,
AF_UNIX and loopback binds, and access to Docker Desktop's selected
`desktop-linux` canonical Unix endpoint. Temporal cancellation and exact-run
history reads apply only to the new disposable Temporal server. Port 17233 must
be free; an existing service is never stopped or changed to free it.

## Effects and evidence

After all capability checks pass, the script builds an immutable staging image
from captured exact bytes, builds the matching Temporal driver, runs all 19
complete-route cases, and runs `make sandbox-test`, `make verify`, Go vet/tests
and the diff check. SDK and backend containers receive no Docker socket.

The script creates only labelled disposable certification containers, fresh
fixture/evidence directories, local build/cache artifacts and staging images.
Temporal uses ephemeral storage. Cleanup verifies the exact full container ID,
image, ownership label and daemon before removal. It uses no reset, prune,
volume deletion, product repository, production database mutation, push or PR.

`runtime-host-result.json` records every stage, original failure details and
the required cleanup disposition. It remains DENIED/NO for production and
readiness even if the runtime/check stages pass. It performs zero real model
calls and creates or consumes no qualification grant. A source review of this
entrypoint is separate from the independent runtime review of actual evidence.

The harness's `runtime-unknown` case exercises interrupted cleanup observation.
It explicitly does not establish a real Docker daemon outage. The result must
retain that boundary; any additional daemon fault certification needs a scoped
supported fault mechanism and its own evidence, without altering unrelated
Docker services.

## Resume safely

Return the evidence directory to the same CDO task. Preserve old BLOCKED and
failed attempts. The task reviews exact runtime/history/cleanup evidence before
preparing a release-bound qualification and requesting any required final
identity approval. It does not reuse the expired historical grant or resume an
old model thread.

If an attempt reports UNKNOWN cleanup, do not rerun it or delete its evidence.
Keep its exact ownership ledger for reconciliation in the restored authorized
environment. No saved PID is a signal authority. After positive cleanup is
established, use a fresh evidence directory for a new certification attempt;
partial case results cannot be promoted into a full PASS.

The final real model canary and normal production admission remain later gated
stages. This command alone cannot mark the platform production ready.

## Process observer diagnosis before another certification attempt

Preserve the failed `authorized-host-final-20261009-01` attempt. Its null detail
cannot establish which snapshot row failed. A deterministic regression reproduced
a successful, empty-stderr `ps` being rejected on a Darwin kernel PID/PGID zero
record. The parser now retains that observation record; positive owned identities,
complete caller/observer anchors, strict parsing and UNKNOWN remain required.

Run only this read-only diagnostic in the same normal macOS Terminal, using the
Python executable which the owner already proved can invoke `/bin/ps`:

```sh
/opt/homebrew/opt/python@3.14/bin/python3.14 -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --observe-only
```

It runs the exact existing trusted observer once and prints bounded JSON. It
creates no owned execution group, socket, Docker resource, file or certification
attempt. Bytecode writes are disabled. Output includes Python/runtime context,
record counts and zero-ID counts, or a specific typed parse/IO/timeout reason.
No unrelated process identities, raw rows, command lines or environment values
are printed. `legacy_positive_ids_rule_would_reject=true` on PASS confirms that
the old parser would reject this actual host snapshot. Otherwise retain the
specific reason and investigate it; do not infer historical failure from a guess.

Do not run full certification until this diagnostic is PASS and its evidence is
reviewed in the same CDO task. After confirmation, the exact safe restart command
uses the preserved inputs, the same Python executable and a fresh output:

```sh
/opt/homebrew/opt/python@3.14/bin/python3.14 -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --execute \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261009-02 \
  --go-bundle /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/go-dependencies/go-deps-fixture-pcs74yoa \
  --go-bundle-manifest-sha256 9014d919474b6917fc254ab534e8d9d24b2ac0b0a87ca26400c8b8b5a8f3eb76 \
  --command-image sha256:a63ad55c3990b47bbe62b51de214e3668936f79b3b55800ef2e3bf63c7591653 \
  --sdk-image sha256:7bc55a3d9834c84e3d411e2450338fb466a536f736869087515beec72d68cdfe \
  --temporal-image auto-local
```

The restart preserves preflight first and rejects any actual OS/tool denial.
No old UNKNOWN attempt is overwritten or promoted to PASS. Model qualification
and production admission remain separately gated.

## Observer anchor UID remediation

The owner's next host observation reached parsing with ps exit0 and empty stderr,
then failed the observer anchor's combined missing/UID check. This is not EPERM,
and `cwd_matches_repository=false` does not affect absolute `/bin/ps` execution.

Read-only executable metadata on this Mac shows root-owned `/bin/ps` mode04755.
Darwin's `uid` field is the effective UID; the setuid observer can therefore be
UID0 while the Python caller remains UID501. The previous parser incorrectly
required the caller's effective UID for both anchors. It now derives observer
EUID only from the verified fixed executable's root ownership/setuid mode and
nonwritable namespace, checks that identity before/after observation, and keeps
the caller and all owned execution UID requirements unchanged.

Both anchors must be present. Observer PPID must equal captured caller PID;
caller PID/EUID/PPID must remain stable and match the snapshot. Failures distinguish
CALLER_ANCHOR_MISSING, OBSERVER_ANCHOR_MISSING, CALLER_UID_MISMATCH,
OBSERVER_UID_MISMATCH and PARENT_IDENTITY_MISMATCH. Evidence reports boolean
presence/UID/parent checks for only these anchors, without raw process records.

Apple's current ps source does not intentionally exclude its own PID from -ax.
A missing observer still fails UNKNOWN; no timing exception waives that check.
The former combined host reason alone cannot establish actual row presence;
confirm the fixed observer with the one read-only command above. Do not run
certification, Docker, Temporal or model qualification before observe-only PASS.

## Owned signal diagnosis — 2026-10-10

The actual `authorized-host-final-20261009-02` attempt
`f2b36ef5-34d3-4177-942a-1fde62ea07bf` passed host observation and then recorded
`OWNED_SIGNAL_CAPABILITY_UNKNOWN` with null detail. Its ledger proves Popen
returned child PID51908. Saved PGID/SID51908 are expected identities, not proof
that validation or signal execution occurred. The historical failing operation,
errno, signal attempt and final absence remain undetermined. Both old files are
preserved unchanged. Do not rerun either historical full command above.

The source now records phase, original type/code/errno, two identity checks,
syscall acceptance, WNOWAIT receipt and positive cleanup disposition without raw
process rows or exception messages. `killpg` intent recorded before the syscall
has `signal_attempted=null` until actual execution is observed. PASS requires
CLD_KILLED/SIGTERM for the exact child and real UID, reaping, and a complete
trusted snapshot proving PID/PGID absence. A natural four-second exit cannot prove
signal capability. Primary UNKNOWN remains UNKNOWN even if cleanup later proves
absence. Observer denial is latched; no alternate observer or fallback signal.

### One minimal host action

Run only this command in the normal authorized macOS Terminal. The output path
must be fresh. It first observes the old PID/PGID read-only through the existing
trusted observer. Any matching record, including PID reuse, prevents new process
creation and requires separate identity-based reconciliation; it sends no signal
and deletes nothing from the old attempt. A denied observation stops without retry.

```sh
/opt/homebrew/opt/python@3.14/bin/python3.14 -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --owned-signal-only \
  --reconcile-attempt /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261009-02 \
  --reconcile-attempt-id f2b36ef5-34d3-4177-942a-1fde62ea07bf \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/owned-signal-host-20261010-01
```

After positive old-group absence only, this creates one new Python child in a
new session/group, running `time.sleep(4)`. It validates exact PID/PPID/PGID/SID/
UID twice and sends at most one SIGTERM to that fresh group while the unreaped
child pins its identity. It observes WNOWAIT termination, reaps the exact Popen
child, and requires complete post-reap group absence. Failure waits only for that
same self-expiring child, at most six seconds, and records UNKNOWN if absence
cannot be positively observed. It never signals a saved PID or another process.

Other effects: one fresh private evidence directory with bounded JSON. No socket
bind, Docker, Temporal, full certification, model, grant, admission or deletion.
`owned-signal-only-result.json` and `owned-signal-diagnostics.json` retain the
original failure and any separate cleanup uncertainty. Runtime PASS is not
claimed by deterministic source tests. Return this fresh evidence to this task.

### Full certification restart command — gated, not executed in this slice

Do not run this until the minimal diagnosis above is PASS, both old and fresh
groups are positively absent, and the evidence has been reviewed. An UNKNOWN
diagnostic requires reconciliation first. This command uses a new directory and
preserved immutable inputs; production stays DENIED and model qualification
does not run. All capability preflights still apply.

```sh
/opt/homebrew/opt/python@3.14/bin/python3.14 -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --execute \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261010-03 \
  --go-bundle /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/go-dependencies/go-deps-fixture-pcs74yoa \
  --go-bundle-manifest-sha256 9014d919474b6917fc254ab534e8d9d24b2ac0b0a87ca26400c8b8b5a8f3eb76 \
  --command-image sha256:a63ad55c3990b47bbe62b51de214e3668936f79b3b55800ef2e3bf63c7591653 \
  --sdk-image sha256:7bc55a3d9834c84e3d411e2450338fb466a536f736869087515beec72d68cdfe \
  --temporal-image auto-local
```

## Waitid UID validation remediation — 2026-10-10

The isolated `owned-signal-host-20261010-01` attempt reached waitid-validation:
exact child PID50084, code2 (CLD_KILLED), status15 (SIGTERM), but raw UID0 versus
captured owner real/effective UID501. The only failed receipt predicate was UID.
Its old/fresh group cleanup is positively PROVEN_ABSENT; the UNKNOWN attempt is
preserved and is not relabelled PASS.

Darwin XNU zeroes siginfo and fills PID/code/status while leaving si_uid zero.
That field is unavailable ownership evidence. The ABI offsets/flags remain
unchanged. See [Apple's pinned waitid implementation](https://github.com/apple-oss-distributions/xnu/blob/xnu-11417.140.69/bsd/kern/kern_exit.c#L2966).

The trusted helper now reports backend, WNOWAIT and UID availability explicitly.
Darwin requires the documented raw zero; it never accepts that value as an owned
UID. Exact PID/CLD_KILLED/SIGTERM remain mandatory. Before reap, the existing
trusted observer must positively identify one zombie with matching owned PID,
PPID, PGID and effective UID, while caller identity remains stable. Missing or
wrong ownership evidence remains UNKNOWN. Native non-Darwin waitid UID matching
is unchanged. Reap must return -SIGTERM and complete post-reap absence must pass.

The observer includes zombie credentials. SID/PGID syscalls remain in the two
live identity checks: adding them on a Darwin zombie can return ESRCH. See
[Apple's process snapshot](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sysctl.c#L869)
and [getpgid/getsid](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_prot.c#L207).

### Next minimal host check only

Use this ONE command in the normal authorized macOS Terminal. Output must be
fresh. It retains read-only historical reconciliation, then creates one fresh
self-expiring group and sends at most one owned SIGTERM after two positive live
identity checks. It records receipt predicates, zombie identity booleans, reap
status and positive absence; foreign receipt PIDs are redacted. It runs no binds,
Docker, Temporal, complete certification, models or old-resource signals/deletion.
Do not run a full restart until this new evidence is PASS and reviewed.

```sh
/opt/homebrew/opt/python@3.14/bin/python3.14 -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --owned-signal-only \
  --reconcile-attempt /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261009-02 \
  --reconcile-attempt-id f2b36ef5-34d3-4177-942a-1fde62ea07bf \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/owned-signal-host-20261010-02
```

## Release context failure and retained Temporal ownership — 2026-10-10

The evidence directory `authorized-host-final-20261010-04` records attempt
`d92da144-20af-4a7f-b02f-d175383e526a`. The owner request names a different ID,
`307c7d31-7391-4026-9a33-1a7ca96f6c79`; do not equate these attempts. The diagnosis
uses the exact supplied directory, and any later mutation requires the intended
ownership identity to be confirmed. Historical UNKNOWN evidence is preserved.

`release-build-command.json` records Python3.14.6 child exit1, empty stdout and
`ValueError: name is too long` at `immutable_context` / tarfile USTAR serialization.
The manifest has endpoint filenames with 104-byte `.mmd` and 105-byte `.yaml`
leaves. They cannot fit USTAR's name field. Context serialization fails before
the Docker build invocation; no `build.log`, build result or new image identity
exists in that failed capture. This is a source serialization defect.

The builder now uses deterministic PAX headers without renaming or excluding
inputs. Before each entry write, the byte quota includes serialized headers,
padded file data and a final tar record reserve. Existing file/count/byte caps,
safe reads, manifest hashes, regular-file-only policy and image pins remain.
Two valid-fixture semantic regressions failed before this fix; 39 focused offline
builder/host-wrapper checks passed afterward. A read-only replay of the saved
capture preserved all702 exact paths/content hashes and produced identical bytes
twice. This proves serialization; Docker image build and runtime are NOT_RUN.
PAX supports long filenames through extended headers; see the
[Python tarfile format documentation](https://docs.python.org/3.14/library/tarfile.html#supported-tar-formats).

Cleanup diagnosis: all13 saved parent Docker calls succeeded through Temporal
`start`; no cleanup inspect/remove was attempted. The wrapper conservatively
latches `docker.denied` after any unclassified child build/harness exception and
records `DOCKER_DENIED_NO_RETRY`. This label does not establish Docker permission
denial, daemon outage or identity mismatch. The no-retry policy is preserved.
The exact container's current state and cleanup remain UNKNOWN; its REGISTERED
ownership ledger does not prove current presence or absence.

### ONE read-only owner observation

Run only this command in the normal authorized macOS Terminal:

```sh
/opt/homebrew/opt/python@3.14/bin/python3.14 -I -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/host-release-cleanup-diagnosis-20261010/observe-owned-temporal.py
```

The diagnostic artifact validates the saved attempt/ledger/runtime pins, reads
the selected `desktop-linux` context, verifies daemon identity before and after
one metadata-only inspect of exact full container ID
`c5a12db6f36abe2588ee453bc5ce23af99e23013248de29036270f7e5bd0c975`, and prints bounded
JSON. Image, ownership label and name must match the ledger. It invokes only
`context show`, `context inspect`, `info` and `inspect` at the saved endpoint and
executable; no evidence writes, container mutation, signals, Temporal calls,
models or certification occur. Inspect does not request environment or mounts.
Every subprocess is mocked in seven passing offline diagnostic checks; the
actual operator observation has NOT_RUN in the agent environment.

`PROVEN_ABSENT` requires a precise full-ID absence response and positive matching
daemon observations. Connection/permission errors, changed daemon, wrong
ownership or missing observations remain UNKNOWN. `PRESENT_OWNED` proves only
presence and identity; cleanup is still required and needs a resource-specific
owner decision through the existing trusted cleanup mechanism. The diagnostic
does not delete anything or promote the old attempt to PASS. Return the bounded
JSON for review; do not rerun certification yet.

### Next full certification command — prepared only, currently blocked

Run only after the source fix is independently reviewed, the attempt-ID
discrepancy is resolved, and the old exact owned Temporal resource is positively
absent with reviewed evidence. If it is present or UNKNOWN, this gate is closed.
The new directory must not exist. This command is NOT executed in this slice;
the original inputs are retained, budget20/enforce and productionDENIED remain.

```sh
CODEX_SOL_MAX_RUNS_5H=20 CODEX_BUDGET_MODE=enforce \
  /opt/homebrew/opt/python@3.14/bin/python3.14 -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --execute \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261010-05 \
  --go-bundle /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/go-dependencies/go-deps-fixture-pcs74yoa \
  --go-bundle-manifest-sha256 9014d919474b6917fc254ab534e8d9d24b2ac0b0a87ca26400c8b8b5a8f3eb76 \
  --command-image sha256:a63ad55c3990b47bbe62b51de214e3668936f79b3b55800ef2e3bf63c7591653 \
  --sdk-image sha256:7bc55a3d9834c84e3d411e2450338fb466a536f736869087515beec72d68cdfe \
  --temporal-image sha256:ad4c82c97bd12b417d1ea942610dbcd511afb250c4d5ed26c694009533df447e
```

## Exact Temporal reconciliation owner decision — 2026-10-10

The owner has explicitly confirmed attempt
`d92da144-20af-4a7f-b02f-d175383e526a` and approved cleanup of only the saved full
container ID, immutable image and ownership label below. This resolves the prior
ID discrepancy for this resource. It does not change the historical UNKNOWN.

### One reviewed operator cleanup command

If the restricted tool environment denies cleanup, stop that attempt. Do not
escalate, change permissions or invoke another tool/path. Run this one command
in the normal authorized macOS Terminal with a fresh output directory:

```sh
CODEX_SOL_MAX_RUNS_5H=20 CODEX_BUDGET_MODE=enforce \
  /opt/homebrew/opt/python@3.14/bin/python3.14 -I -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --reconcile-temporal-only \
  --reconcile-attempt /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261010-04 \
  --reconcile-attempt-id d92da144-20af-4a7f-b02f-d175383e526a \
  --approved-container-id c5a12db6f36abe2588ee453bc5ce23af99e23013248de29036270f7e5bd0c975 \
  --approved-container-image sha256:ad4c82c97bd12b417d1ea942610dbcd511afb250c4d5ed26c694009533df447e \
  --approved-container-label e58e96da-d8c4-460b-b0a3-489d3687379c \
  --approved-docker-identity-sha256 d8abc9be36f2bceb13b67abbd724d8d4263a69709e8b9bc4707b1c91fecbc08e \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/temporal-reconciliation-host-20261010-01
```

The isolated entrypoint uses the existing trusted `cleanup_temporal` and `Docker`
adapter. It requires explicit approval selectors to match the owner-bound saved
ledger, a canonical saved executable/endpoint, and selected `desktop-linux`
context. The independently reviewed SHA-256 of the preserved `docker-identity.json`
above pins executable, endpoint and daemon metadata before any subprocess. It
matches the hash recorded by the earlier diagnosis; do not replace it with a
digest computed from changed metadata. Exact full ID, immutable image, label and saved name are inspected before
mutation; daemon identity is rechecked immediately before the existing exact-ID
`rm -f`, which stops/removes only that container without a volume-removal option.
Cleanup PASS requires exact full-ID absence and a matching daemon observation
afterward. Missing/denied observations remain UNKNOWN, ownership retained, and
there is no fallback or retry after Docker failure.

Effects are bounded to that exact disposable container and one new private
receipt directory. No volume/prune/reset, unrelated service mutation, process
group probe, SDK/backend Docker access, model, grant or full certification.
The original attempt is read only. Its metadata hashes are checked before/after
cleanup; the separate `temporal-reconciliation-receipt.json` is exclusively
created, fsynced, read only, and never overwritten by the entrypoint. A rerun
requires a new receipt directory. UNKNOWN receipts remain history.

### Cleanup control flow correction

The wrapper no longer sets Docker denial merely because its child build failed.
The trusted builder completes capture/context serialization before its first
Docker invocation. A local failure emits a bounded receipt with the fresh parent
invocation token, typed local phase and `docker_calls_attempted=false`. The
wrapper verifies that owner-bound receipt before allowing normal exact owned
cleanup. Source/build remains FAIL even when cleanup succeeds.

Missing, stale, malformed or runtime-phase receipts retain cleanup UNKNOWN with
`CHILD_RUNTIME_UNKNOWN_NO_RETRY`; they are never fabricated as Docker denial.
Actual parent Docker failures retain their independent no-retry latch. Complete
runtime child uncertainty remains a cleanup blocker. Deterministic PAX, exact
source bytes, strict limits and immutable image binding are preserved.

### Certification resume gate

Only after independent source/command review PASS and a separate exact cleanup
receipt with `status=PASS`, `exact_resource_absent=true`, and
`original_evidence_unchanged=true` may the prepared full certification command
above target `authorized-host-final-20261010-05`. That directory must still be
fresh. Do not automatically execute it in the restricted agent environment.
Production remains DENIED/readinessNO; budget20/enforce; model calls0.

## Post-removal lowercase absence remediation — 2026-10-10

The actual normal-host reconciliation in `temporal-reconciliation-host-20261010-01`
validated the selected context, endpoint, daemon and exact owned container, then
successfully ran the single authorized `rm -f`. The final exact-ID inspect returned
exit1, stdout `[]`, and `error: no such object: <full approved ID>`. The strict
absence parser previously accepted only capitalized Docker diagnostics, so this
documented lowercase form incorrectly latched `DOCKER_OPERATION_UNKNOWN`.

The parser now additionally accepts precisely `error: no such object: ` plus the
exact full lowercase 64-hex ID. It still requires exit1 and empty/`[]` stdout;
wrong/truncated IDs, extra diagnostics, permission errors, connection errors and
other exit codes never prove absence. There is no general substring match or
case normalization. The old UNKNOWN receipts and original attempt are unchanged.
The historical attempt has no successful daemon observation after its final
inspect, so it must not be retroactively promoted to PASS.

### One read-only normal-host confirmation command

The restricted agent environment already denied access to the approved socket;
do not repeat that known denial or use escalation/another tool to bypass it.
The following command only observes the approved exact ID and rechecks the
approved daemon before/after absence. It shares the trusted Docker adapter and
all independently pinned saved-identity checks with reconciliation. It never
stops/removes a container, including if the exact ID is unexpectedly present.
It writes only a fresh private evidence directory and a separate exclusive,
fsynced mode0400 receipt. No process probes, Temporal execution, model calls or
full certification are performed. Run once in the authorized macOS Terminal:

```sh
CODEX_SOL_MAX_RUNS_5H=20 CODEX_BUDGET_MODE=enforce \
  /opt/homebrew/opt/python@3.14/bin/python3.14 -I -B /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/scripts/certify-runtime-host.py --observe-temporal-absence-only \
  --reconcile-attempt /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/authorized-host-final-20261010-04 \
  --reconcile-attempt-id d92da144-20af-4a7f-b02f-d175383e526a \
  --approved-container-id c5a12db6f36abe2588ee453bc5ce23af99e23013248de29036270f7e5bd0c975 \
  --approved-container-image sha256:ad4c82c97bd12b417d1ea942610dbcd511afb250c4d5ed26c694009533df447e \
  --approved-container-label e58e96da-d8c4-460b-b0a3-489d3687379c \
  --approved-docker-identity-sha256 d8abc9be36f2bceb13b67abbd724d8d4263a69709e8b9bc4707b1c91fecbc08e \
  --output /Volumes/ZX10/Developments/microservices/course-dev-orchestrator/.cache/sandbox-certification/temporal-absence-host-20261010-01
```

Do not repeat removal. Full certification for `authorized-host-final-20261010-05`
remains prepared only until this independent read-only receipt has `status=PASS`,
`read_only=true`, `exact_resource_absent=true`, and `original_evidence_unchanged=true`
and source/command review PASS. Production remains DENIED; budget20/enforce.
