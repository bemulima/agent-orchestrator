# Go context retrieval Wave 3

`GO_RETRIEVAL_PROFILE_V3 = READY` for the explicit
`source-bound-analysis.v1.2` read-only API and CLI mode. Independent acceptance
covers the 36 frozen real-source scenarios, supported mandatory evidence,
198 Gold scenarios, Wave 1/2 compatibility and credential-free verification.
Git-object pins remain unsupported; source retrieval grants no execution or
write authority.

The reviewed change boundaries are recorded in
[the Stage 1 contract](go-context-retrieval-wave3-contract.json),
[its freshness and budget supplement](go-context-retrieval-wave3-contract-supplement.json),
[the bounded source-equivalence contract](go-context-retrieval-wave3-boundary-contract.json),
[the authored-area diagnostic contract](go-context-retrieval-wave3-metadata-contract.json)
and [the current-reference supplement](go-context-retrieval-wave3-current-reference-contract.json).
The [core retrieval documentation](agent-context-retrieval.md) defines source
admission, ContextPack v1, deterministic replay and the offline commands.

## Version selection and CLI

Default retrieval and R1 routing keep their existing behavior. `--analysis-scope`
without `--analysis-version` selects the existing `source-bound-analysis.v1.1`
mode. Version 1.2 requires both flags:

```text
--analysis-scope --analysis-version source-bound-analysis.v1.2
```

An explicit version without `--analysis-scope`, or an unknown version, is
rejected. The frozen v1.1 APIs and report shape remain unchanged. ContextPack
stays at its existing v1 schema; the v1.2 report is a separate companion.

Run the executable from the CDO repository root so its canonical catalog can
be loaded and checked against the supplied route. Each admitted source needs
one current `--root identity=absolute-root` mapping. The root is supplied at
invocation time and is absent from serialized source admission. Additional
identities cannot be admitted through a cached pack or metadata.

For the following examples, set `RETRIEVAL_SOURCE_ID` to an identity already
present in `request.json` and `RETRIEVAL_SOURCE_ROOT` to that source's explicitly
admitted absolute root. Repeat `--root` for each admitted neighbor. Supply the
existing current route, and include the optional contract plan and R1 coverage
companion when available:

```sh
.cache/bin/course-dev-orchestrator-context context-prepare \
  --request-json request.json --route-json route.json \
  --contract-plan-json contract-plan.json \
  --routing-coverage-json routing-coverage.json \
  --root "$RETRIEVAL_SOURCE_ID=$RETRIEVAL_SOURCE_ROOT" \
  --analysis-scope --analysis-version source-bound-analysis.v1.2 \
  --analysis-report-json prepare-analysis.json \
  --format json > prepare-pack.json

.cache/bin/course-dev-orchestrator-context context-expand \
  --request-json request.json --route-json route.json \
  --contract-plan-json contract-plan.json \
  --routing-coverage-json routing-coverage.json \
  --root "$RETRIEVAL_SOURCE_ID=$RETRIEVAL_SOURCE_ROOT" \
  --base-pack-json prepare-pack.json --expand-json expand.json \
  --analysis-scope --analysis-version source-bound-analysis.v1.2 \
  --analysis-report-json expanded-analysis.json \
  --format json > expand-delta.json
```

`--analysis-report-json` is optional and requires analysis mode. Reports use
exclusive creation with mode `0600`; each invocation needs a new report path.
Prepare writes a ContextPack to stdout. Expand writes a ContextDelta containing
`pack`, delta status and diagnostics; use its `pack` as the next base. Markdown
output also retains blocked delta diagnostics. Without the optional R1 coverage
input, historical routing acquisition can remain unknown or partial.

These commands dispatch before configuration loading and do not load model,
database or Temporal credentials. Input JSON and source descriptors retain
the core strict parsing, path, secret exclusion, file and read-budget checks.
Saving or publishing a pack is an explicit caller action: evidence may contain
admitted source bytes. The public synthetic corpus contains independently
authored fixture bodies.

## Bounded adjacency and acquisition labels

The [acquisition correction](go-context-retrieval-wave3-acquisition-contract.json)
keeps caller provenance separate from the reason a declaration was acquired.
A generated whole-file seed may acquire adjacent declarations before the
context pass. It cannot erase their independently verified same-file adjacency
to an exact current original or successfully expanded caller, and cannot
originate a proof itself.

Version 1.2 counts at most 1000 distinct adjacent context identities globally,
in sorted order, including declarations already acquired by whole-file seeds.
Repeated callers cannot reset this counter. Eligibility requires the same
current source, file and hash plus an actual trusted acquired key. Shape,
package, import, module and ambiguity checks still apply after the counter.
An omitted context produces `ANALYSIS_HTTP_CLIENT_CONTEXT_LIMIT` with PARTIAL
status in the emitted Prepare and Expand companion. The existing core
acquisition map, queue, depth and default v1.1 behavior remain unchanged.

## Cumulative companion and budgets

The v1.2 APIs are `PrepareProjectContext` and `ExpandProjectContext`. The latter
checks the base, current caller admission and live source snapshots, then asks
the core engine to replay Prepare and the accepted expansion history. Only
after trusted replay does it construct a fresh companion from exact selectors
retained in the returned cumulative RetrievalPlan. Retained diagnosed misses
remain visible; a rejected expansion attempt cannot add a companion selector.
Explicit caller provenance is separately derived from the original request and
successful expansion history, matched against that returned plan by selector
ID, source, path, symbol, query and current hash. Route-generated seeds can
remain in cumulative acquisition but cannot originate a current caller pin.

The companion binds `context_pack_digest`, `initial_selectors_digest`,
`cumulative_selectors_digest`, `expansion_history_digest` and `binding_digest`.
Its intrinsic read-scope digest is distinct from the pack's original route
reference. These bindings avoid a circular pack/route/companion digest and keep
the original immutable pack replay contract intact. An unchanged admission
with changed source bytes returns stale-base failure; an admission change
remains a scope-change failure. Recomputing a forged cache digest cannot grant
evidence or reset the core counters.

ContextPack evidence, companion layers and their distinct source-path overlap
are measured separately. A layer witness may live outside the selected pack's
path footprint. Its source/hash/span chain supports the stated read
attribution; it does not prove a resolved runtime caller or semantic relevance
for every acquired syntax candidate.

`joint_budget_used` accounts for the current emitted pack and companion. The
source-byte ledger starts at the core cumulative source cost and adds exact
companion evidence units absent from the pack. Deduplication uses source,
snapshot, path, hash, span and content. It never refunds historical pack costs.
Per-source and per-facet bounds are enforced; layer costs are conservatively
charged to same-source retained exact selectors. This cost attribution does
not claim that a layer satisfies each charged selector.

The context estimate includes canonical pack JSON and serialized companion
JSON, including repeated serialized evidence, proofs, gaps, diagnostics,
ledgers and digests. It uses UTF-8 bytes divided by four, rounded up, with the
reserved prompt tokens checked once against the context allowance. This is a
deterministic estimate rather than a model tokenizer.

Whole companion layer units are selected or omitted in deterministic order.
Omissions produce `ANALYSIS_COMPANION_BUDGET_UNSATISFIED` and a `BLOCKED`
`joint_budget_status`; required selectors remain present. If the pack plus the
minimum report certificate cannot fit, the API returns
`ErrProjectAnalysisBudget`. It does not emit an oversized compliant report.
The ledger certifies this current bundle. Historical companion costs and a
joint interpretation of Expand `remaining_budget` are not certified; the core
retains its separate cumulative pack accounting.

## Bounded source and boundary proofs

Version 1.2 accepts explicit current Go declaration anchors, including
qualified package selectors and value declarations. Its bounded intent parser
accepts read requests and source descriptions while rejecting explicit or
conditional mutations and ambiguous independent instructions. Exact non-Go
document anchors require current hashes; document bytes do not initiate Go
AST traversal or become executable instructions.

Existing interface-boundary proofs remain limited to their established kinds.
The additional `application-local-result-concrete.v1` proof is restricted to
an original `application-command-result` requirement whose proposed file is
verified absent within admission. Exact caller anchors must uniquely identify
an ordinary application method returning a same-package named struct and
`error`, plus an anchored consumer with the exact same-module imported
concrete receiver field and matching field-call syntax. Module, owner, result
and consumer hashes are replayed independently. Ambiguity, aliases, generics,
shadowing, foreign modules and malformed proofs fail closed.

The original facet ID, proposed path, requirement kind, contract owner and
freeze requirements remain intact. Actual source declarations can satisfy the
bounded read-evidence requirement without creating the proposed file. This
does not establish compiler compatibility, interface satisfaction, business
contract approval, runtime wiring or execution certification. Git-object
revision certification also remains explicitly unsupported. Selected tests
are source evidence; retrieval does not execute a provider, validator or
sandbox.

The frozen core quality stage canonicalizes `Authority.Role` and
`Authority.Basis` from the evidence `ClaimType` and provenance. The resolver's
`source_equivalent_application_result` role therefore does not survive as that
role in the final pack. Inspect the pack's limitations, including evidence
limitations, and the companion's `boundary_proofs` for the bounded conceptual
equivalence and its original requirement references. A canonical factual role
does not grant business freeze, write ownership or runtime certification.
`boundary_proofs` and `read_area_gaps` are body-free reference records;
companion layer evidence can contain admitted source bytes and remains subject
to the same output handling and joint-budget checks.

## Authored read-area diagnostics

Canonical target paths, current literal authored layer paths and verified
strict operation references can establish a read area. Coarse metadata does
not establish a finer business role or protocol direction. Literal composition
paths also accept bounded named entrypoint records and role-to-path mappings;
invalid structures still discard that document's declarations transactionally.

An authored infrastructure area can receive descriptive HTTP client attribution
from an exact retained caller's outgoing HTTP syntax, or a single concrete type
reference from that caller's parameter or an adjacent ordinary struct field.
The terminal witness is the uniquely resolved current concrete struct with a
direct named field of imported `net/http.Client` type. Local module/package,
current hashes, admission and complete package parsing must agree. The chain
labels adjacent declarations `SAME_FILE_CONTEXT`; this is source/type syntax,
without interface satisfaction, call graph or runtime certification. Generated
selectors cannot originate this proof. Aliases, generics, embedded promotion,
ambiguous types, foreign modules and incomplete packages fail closed. Incoming
DTO refinement requires actual incoming HTTP AST in the referencing caller;
an HTTP-shaped directory alone supplies no such DTO proof. See the frozen
[residual correction contract](go-context-retrieval-wave3-residual-contract.json).

Concrete context uses the existing bounded `SAME_FILE_CONTEXT` acquisition;
omitted declarations cannot bypass its 1000-declaration limit. Blank fields,
duplicate import bindings and unverified ancestor module boundaries cannot
prove a terminal. Multiple distinct concrete terminals produce
`ANALYSIS_HTTP_CLIENT_TYPE_AMBIGUOUS` instead of choosing one. New infrastructure
function witnesses require an ordinary outgoing HTTP constructor call; a
generic, aliased, embedded or wrapped type cannot bypass concrete type checks
through a nested `http.Client` spelling. Raw entrypoint globs are rejected
before path normalization. These checks do not extend source admission.

A strict current
operation `implementation.use_cases` reference requires the same source,
safe literal source path, a unique declared symbol in the verified admitted
package and an explicit current caller hash for that file. Its metadata and
source must share the admitted snapshot and admission digest. A nonempty
authored checksum must match the current source; a mismatch never falls back
to an unpinned interpretation.

The strict schema also permits an absent authored checksum. Version 1.2 can
then prove the current literal role using the independently verified caller,
source and metadata hashes, while emitting
`ANALYSIS_OPERATION_REFERENCE_UNPINNED` with status `PARTIAL`. The layer's
relation and limitations disclose current-snapshot attribution only. The
observed current source hash is not an authored historical checksum, and no
Git-object or earlier-revision certification is inferred. Missing caller
provenance, ambiguous symbols or incomplete package parsing cannot establish
this proof. As with existing current literal layer declarations, it proves a
read attribution in the admitted current snapshot.

Either verified branch produces
`backend.usecase`, `implementation.use_cases:<sourceDir>` and the additive
`use_cases:<sourceDir>` alias. The verified reference attributes its exact
current source file, including an explicit adjacent helper. The directory in
the alias is provenance labeling and cannot admit arbitrary sibling files.

For explicit exact hash-bound non-test Go caller selectors retained after
trusted cumulative replay,
`read_area_gaps` records unavailable authored attribution. Each record binds
the source, snapshot, admission digest, source anchor, selector IDs, catalog,
profile and consulted metadata hashes. `metadata_diagnostics` links it through
`ANALYSIS_AUTHORED_READ_AREA_MISSING`. Source evidence can be found while that
authored-area dimension remains `NOT_VERIFIED`; `attribution_status` becomes
`PARTIAL` when gaps are present.

The record's coverage describes the actual admitted read-area metadata search
scope. Typed area-search blockers include malformed literal area structures,
unresolved verified profiles, relevant loader limits and strict operation
references that cannot be verified for the anchor file. They prevent a claim
of complete scoped absence. General statement-citation diagnostics remain
visible in `metadata_diagnostics`; an unrelated stale, unpinned or unreadable
citation is not an area declaration and does not by itself invalidate this
scoped search. The current-reference unpinned diagnostic also remains visible
after positive current area attribution. Independently mandatory citation or
business-contract evidence still has to pass its own requirement checks.

This is never repository-wide absence proof. A gap's diagnostic chain is
consulted metadata plus a current source anchor, without an invented positive
layer. Neither source behavior nor role prose supplies an unauthored
directory-to-role mapping.

All admitted sources stay forbidden for writes. Read custodians are distinct
from implementation ownership; neighbor evidence retains its own source and
cannot become a writable owner. A missing-area diagnostic cannot waive a
business contract, owner review, freeze, execution gate or source-read failure.
Repository metadata changes remain the metadata owner's separately authorized
work; this CDO mode does not apply pending owner proposals or accept a new
`read_areas` schema automatically.

## Frozen acceptance and reproducible checks

The real-source expectation set retains 36 scenarios and these original
occurrence denominators:

| Category | Required occurrences |
| --- | ---: |
| Prepare anchors | 75 |
| Expand anchors | 180 |
| Test declarations | 84 |
| Contract evidence | 77 |
| Companion layer obligations | 140 |

Keep raw found/required reporting against all 140 layer obligations. The
capability partition is 138 supported dimensions and two unresolved
repository-author proof dimensions for registry and durable storage read
areas. Both source witnesses stay mandatory; their metadata-owner proposals
are pending and unapplied. Final replay retrieves all 138 supported dimensions;
the other two remain typed, hash-bound `NOT_VERIFIED` author-area gaps for S04
and S07. They are not counted as found. Capability-aware classification is
34 `PASS` and two `VALID_PARTIAL` cases with every other supported obligation
satisfied. All 36 raw packs remain `PARTIAL`, including the explicit unsupported
Git-object pin. A source or intent failure cannot be waived by this partition.

The committed [independent synthetic Gold](../internal/contextretrieval/evaluation/wave3/README.md)
is self-contained. Its lineage records original identities, paths and hash
commitments; fixture bodies are independently authored. It exercises generic
declaration, boundary, metadata, cumulative expansion and adversarial shapes.
It does not replace fresh native and physical CLI replay against the actual
admitted snapshots, or prove identity with private source bodies. Fixture
generation is not required for verification.

With dependencies already installed in the repository-local caches, run the
synthetic corpus without module downloads:

```sh
env -i PATH="$PATH" GOPROXY=off \
  XDG_CACHE_HOME="$PWD/.cache" GOCACHE="$PWD/.cache/go-build" \
  GOMODCACHE="$PWD/.cache/gomod" GOBIN="$PWD/.cache/bin" \
  go test ./internal/contextretrieval/evaluation/wave3 -count=1
```

The root Makefile automatically includes `.env`. For credential-free full
verification, create a disposable cache copy removing only that exact initial
include block. No environment file is opened or changed by this step:

```sh
python3 - <<'PY'
from pathlib import Path
source = Path('Makefile').read_text()
prefix = 'ifneq (,$(wildcard .env))\ninclude .env\nexport\nendif\n'
if not source.startswith(prefix):
    raise SystemExit('Makefile include block changed; review the verification copy')
target = Path('.cache/wave3-verify.mk')
target.parent.mkdir(parents=True, exist_ok=True)
target.write_text(source[len(prefix):])
Path('.cache/wave3-npm-global-empty').write_text('')
PY

env -i PATH="$PATH" GOPROXY=off NEXT_TELEMETRY_DISABLED=1 \
  XDG_CACHE_HOME="$PWD/.cache" GOCACHE="$PWD/.cache/go-build" \
  GOMODCACHE="$PWD/.cache/gomod" GOBIN="$PWD/.cache/bin" \
  npm_config_userconfig=/dev/null \
  npm_config_globalconfig="$PWD/.cache/wave3-npm-global-empty" \
  npm_config_cache="$PWD/.cache/npm" \
  make -f .cache/wave3-verify.mk \
  COMPOSE='docker compose --env-file /dev/null' context-test context-gold verify
```

This uses the declared verification targets, local Go/Node dependencies and
Compose's configuration check with synthetic defaults. It does not start the
stack or run production integration tests. Missing offline dependencies or
tools are failed prerequisites, not passing checks. `context-gold` evaluates
the preserved core Gold fixtures; `context-test` includes the additive Wave 3
tests. Full verification and independent acceptance passed on the frozen
implementation and were repeated on the exact publication tree.

## Verified Wave 3 result

Original expectations and denominators remain unchanged. Actual admitted
source snapshots cover 1912 distinct paths across the four targets and their
read-only neighbors; content hashes and current revision labels are verified.
Private AI Prompt and Sandbox source bodies are absent from public fixtures.

| Measure | Found / required |
| --- | ---: |
| Prepare anchors | 75 / 75 |
| Expand anchors | 180 / 180 |
| Test declarations | 84 / 84 |
| Contract evidence | 77 / 77 |
| Supported companion layers | 138 / 138 |
| Raw companion layers | 138 / 140 |
| ContextPack mandatory human labels | 416 / 416 |
| Core required facets | 490 / 490 |

Minimum per-case ContextPack source/path precision is 0.8888889, above the
0.80 requirement. Pack path footprint contains witnesses for 121 of the 140
original layer obligations. Pack and companion contain respectively 315 and
400 distinct source/path occurrences across cases, with overlap 172; 224
unresolved syntax candidates are reported separately. Companion-only witnesses
do not count as selected pack paths or runtime call-graph evidence.

All 36 final joint-budget certificates and 360 native Prepare/Expand stage
bindings pass. Physical CLI replay matches the complete native JSON objects,
including 720 deterministic CLI operations (360 initial + 360 repeats) and
144 forged/malformed cache controls.
Independent review reports P0=0/P1=0 across all 15 readiness checks.

The 162 prior Gold scenarios and 36 additive synthetic scenarios total
198/198 PASS. There are 245 additional named focused analysis controls.
Wave 1 retains 39 scenarios (33 PASS, three VALID_PARTIAL, three
VALID_UNSUPPORTED); Wave 2 retains all 27 scenarios and its 198 plan facets,
82 companion layers, 62 Prepare anchors, 111 Expand anchors and 56 tests.
Every accepted prior Prepare/Expand pack and Wave 2 companion remains
byte-identical. Full verification includes agent policy, gofmt, vet, Go tests,
runner/UI checks, UI build and Compose configuration without starting a stack.

All critical safety counters are zero. Original default R1, v1.1 behavior,
ContextPack v1, routing/contract schemas and approval fingerprints remain
compatible. No target runtime code, per-service RAG or vector database was
added. Retrieval proves admitted source evidence only: it does not certify
provider, validator, Sandbox, student execution or business completion.
Wave 3 closes here; another wave requires a separate user instruction.
