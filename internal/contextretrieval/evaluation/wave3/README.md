# Wave 3 independent synthetic Gold

This package keeps the 36 independently frozen real-source scenario IDs and
category lineage. The public fixture source is independently authored: no target
source bodies, prompt templates, private literals, or execution artifacts are
included. `lineage.json` records only original source identity, relative path and
SHA-256 commitments alongside synthetic selectors. A hash commitment does not
certify a Git object or make the synthetic source identical to the real service.

The original occurrence denominators are 75 Prepare, 180 Expand, 84 tests,
77 contracts and 140 companion layer obligations. Additional exact synthetic
role anchors exercise metadata-backed paths. Every original obligation is kept.
The corpus tests generic declaration shapes, request/result ports, contract
source, test source, hash binding, cumulative expansion and read-only neighbors.
Its source is never compiled or run as a provider, sandbox or validator.

Gold checks mandatory ContextPack evidence separately from companion read
layers and from the path footprint and overlap. Source spans bind to the
independent fixture body manifest. A selected test is source evidence rather
than an execution receipt. Every positive pack retains the explicit unsupported
Git-pin limitation, while required supported evidence must be present.

Negative controls preserve wrong-hash, freshness, cache forgery, absent business
contract, untrusted template, execution boundary, budget, truncated acquisition,
authority conflict, unsafe path and failed-expansion limitations. They never
waive a missing supported obligation to get a passing score.

`go test ./internal/contextretrieval/evaluation/wave3 -count=1` runs this corpus
without network or service execution. The committed JSON is self-contained and
does not require private proofs or the original normalized selector file.

`generate_synthetic.py` is an optional authoring tool. Both the external frozen
selector JSON and output directory must be supplied explicitly:

```sh
python3 internal/contextretrieval/evaluation/wave3/generate_synthetic.py \
  --inputs frozen-labels.json --output generated-wave3-fixtures
```

It reads only the supplied selector metadata, constructs independent bodies,
and writes `cases.json`, `lineage.json` and `safe-body-manifest.json` into that
output directory. The design report goes to stdout; no proof directory is
inferred. Target paths in the input are provenance strings and are never opened.
The authored body and label sets are reproducible; array ordering may differ
from the committed corpus after the documented admission-support correction.
Keep the committed fixtures frozen when using the tool for a new candidate.
