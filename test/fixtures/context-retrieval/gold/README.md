# Offline retrieval gold

The JSON files use `retrieval-gold/v1`. Each case declares its task, synthetic
source bytes, required evidence labels, forbidden labels, expected coverage and
diagnostics before executing retrieval. Labels identify source/path/symbol/kind;
the runner never copies its actual selection into the expected relevance set.

`prepare` and `expand` execute the real local adapter engine against private
temporary sources. `quality` passes explicitly pinned candidates through the
real validator, authority analysis, deduplication and pack budget manager. These
quality cases isolate states that one local current snapshot cannot emit, such
as two revisions of one path; they do not certify the filesystem reader.
`routing_coverage` calls the existing planner and companion coverage collector.
Planner coverage cases report actual stage counters separately from ContextPack
selection precision, because the legacy routing index is not a context pack.

Every result retains numerator and denominator for recall and precision. A ratio
with an empty denominator is reported as 1 with zero counts; aggregate ratios
are computed from sums, not averaged per-case scores. Latency uses elapsed
monotonic time and is operational; it is not part of a semantic digest. Token
counts use the engine's explicit estimator, including the canonical envelope.
Source counts are source/revision occurrences within cases, summed across cases.

`required_contracts` measures raw absence as well as silent absence. The negative
missing-contract fixture intentionally contributes to the raw missing-contract
rate; its matching explicit diagnostic must make the silent count zero. Expected
conflict keys are independent inputs, so suppressed conflict output fails even
when the engine emits no conflict object. An independently declared incomplete
mandatory search fails if its status becomes COMPLETE, even if the engine's
coverage erroneously claims completion.

Safety gates require must-find recall 1.00, zero forbidden evidence/write-owner
scope violations, zero silent required-contract omission, zero stale runtime
payloads, zero suppressed expected conflicts, zero false COMPLETE cases, and
precision at least 0.80. Evaluator unit tests inject faults into observations and
prove that every gate fails; they also reject all-source relevance labels.

Fixtures contain only synthetic placeholder data. Symlinks, FIFOs, unreadable
files and source mutation are constructed inside disposable directories; no
user repository is a mutable test fixture. The runner rejects secret paths,
symlink ancestors, nonregular and hardlinked fixture files before decoding and
requires a strict single JSON document. Setup is bounded to 32 MiB/10,000 files
per operation and 256 cases per suite. An unreadable-source case requires an
unprivileged host: a privileged process that can read mode-000 files will fail
that expectation rather than report an unexecuted scenario as passing.

Run the existing `context-evaluate --fixtures-dir test/fixtures/context-retrieval/gold`
offline CLI or `go test ./internal/contextretrieval/evaluation ./internal/adapters/contextretrieval`.
The suite makes no model, network, database or Temporal call.
