package contextretrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	evaluation "github.com/bemulima/agent-orchestrator/internal/contextretrieval/evaluation/capability"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type wave1ClosureCase struct {
	ID               string                                        `json:"id"`
	Hash             string                                        `json:"original_sha256"`
	Owner            string                                        `json:"owner"`
	Layers           []string                                      `json:"layers"`
	Requirements     map[string][]evaluation.CapabilityRequirement `json:"phase_requirements"`
	FacetClasses     map[string]evaluation.CapabilityClass         `json:"facet_classes"`
	OwnerRequirement *wave1OwnerGap                                `json:"external_owner_requirement"`
}
type wave1OwnerGap struct {
	Class      evaluation.CapabilityClass `json:"class"`
	Diagnostic string                     `json:"diagnostic"`
	Basis      string                     `json:"basis"`
	Scope      string                     `json:"scope"`
	AuditFile  string                     `json:"audit_file"`
	AuditHash  string                     `json:"audit_sha256"`
	Evidence   []struct {
		Source string `json:"source"`
		Path   string `json:"path"`
		Hash   string `json:"sha256"`
	} `json:"evidence"`
}
type wave1ClosureOverlay struct {
	Schema         string             `json:"schema"`
	OriginalDigest string             `json:"original_digest"`
	Original       map[string]string  `json:"original_cases"`
	Audits         map[string]string  `json:"audit_digests"`
	Cases          []wave1ClosureCase `json:"cases"`
}
type wave1ClosureResult struct {
	AbsenceProofs            map[string]*evaluation.ScopedAbsenceProof `json:"scoped_absence_proofs,omitempty"`
	Classification           string                                    `json:"classification"`
	OwnerCorrect             bool                                      `json:"owner_correct"`
	LayersCorrect            bool                                      `json:"layers_correct"`
	Prepare                  evaluation.CapabilityReport               `json:"prepare"`
	Expand                   evaluation.CapabilityReport               `json:"expand"`
	PrepareFacets            evaluation.CapabilityReport               `json:"prepare_all_required_facets"`
	ExpandFacets             evaluation.CapabilityReport               `json:"expand_all_required_facets"`
	ExternalOwnerRequirement *wave1OwnerGap                            `json:"external_owner_requirement,omitempty"`
	DuplicateRatio           float64                                   `json:"duplicate_ratio"`
	Failures                 []string                                  `json:"failures"`
	Passed                   bool                                      `json:"passed"`
}

func verifyWave1ClosureOverlay(directory string, o wave1ClosureOverlay) error {
	if o.Schema != "wave1-capability-acceptance/v2" || len(o.Cases) != 39 || len(o.Original) != 39 {
		return fmt.Errorf("closure schema/cardinality")
	}
	for name, want := range o.Original {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			return fmt.Errorf("original scenario changed: %s", name)
		}
	}
	keys := make([]string, 0, len(o.Original))
	for k := range o.Original {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		key, _ := json.Marshal(k)
		value, _ := json.Marshal(o.Original[k])
		pairs = append(pairs, string(key)+": "+string(value))
	}
	sum := sha256.Sum256([]byte("{" + strings.Join(pairs, ", ") + "}"))
	if hex.EncodeToString(sum[:]) != o.OriginalDigest {
		return fmt.Errorf("immutable39 aggregate digest mismatch")
	}
	for name, want := range o.Audits {
		raw, err := os.ReadFile(filepath.Join(directory, "closure-v2", "audits", name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			return fmt.Errorf("independent audit changed: %s", name)
		}
	}
	for _, c := range o.Cases {
		if gap := c.OwnerRequirement; gap != nil {
			if gap.Scope != "independent_ownership_audit/v1" || gap.Class != evaluation.MissingOwnerContract || gap.AuditHash != o.Audits[gap.AuditFile] || len(gap.AuditHash) != 64 || len(gap.Evidence) < 2 {
				return fmt.Errorf("external owner proof missing")
			}
			raw, err := os.ReadFile(filepath.Join(directory, "closure-v2", "audits", gap.AuditFile))
			if err != nil {
				return err
			}
			var audit struct {
				Ownership struct {
					Decisions []struct {
						Case       string `json:"case"`
						Diagnostic string `json:"diagnostic"`
						Class      string `json:"classification"`
					} `json:"required_owner_decisions"`
				} `json:"ownership_contract_audit"`
			}
			if err := json.Unmarshal(raw, &audit); err != nil {
				return err
			}
			found := false
			for _, d := range audit.Ownership.Decisions {
				found = found || (d.Case == c.ID && d.Diagnostic == gap.Diagnostic && d.Class == string(gap.Class))
			}
			if !found {
				return fmt.Errorf("owner gap does not match independent audit")
			}
		}
	}
	return nil
}
func classifyWave1Closure(o wave1ClosureCase, request core.RetrievalRequest, pack, expanded core.ContextPack, layers []string, engine *core.Engine) wave1ClosureResult {
	r := wave1ClosureResult{Failures: []string{}, ExternalOwnerRequirement: o.OwnerRequirement}
	r.OwnerCorrect = len(pack.RouteRef.Owners) == 1 && pack.RouteRef.Owners[0] == o.Owner
	got, want := append([]string{}, layers...), append([]string{}, o.Layers...)
	sort.Strings(got)
	sort.Strings(want)
	r.LayersCorrect = strings.Join(got, "\x00") == strings.Join(want, "\x00")
	absenceProofs := map[string]*evaluation.ScopedAbsenceProof{}
	allFacets := func(p core.ContextPack) []evaluation.CapabilityRequirement {
		req := []evaluation.CapabilityRequirement{}
		for _, f := range p.RetrievalPlan.RequiredFacets {
			class := evaluation.SupportedRequired
			if v, ok := o.FacetClasses[f.ID]; ok {
				class = v
			}
			q := evaluation.CapabilityRequirement{Selector: evaluation.Selector{Source: f.SourceIdentity, Facet: f.ID}, Class: class, Basis: "Original input or deterministic route/policy obligation; facet identity must have selected evidence."}
			if class != evaluation.SupportedRequired {
				q.Selector.Path = f.Path
			}
			switch class {
			case evaluation.UnsupportedSemantic:
				q.Diagnostic = "UNSUPPORTED_QUERY"
				q.Basis = "Explicit unsupported semantic obligation; no lexical substitution."
			case evaluation.SecurityExcluded:
				q.Diagnostic = "REQUIRED_EVIDENCE_EXCLUDED_BY_SECURITY"
				q.Basis = "Security omission ledger; no excluded content supplied."
			case evaluation.MissingBusinessContract:
				q.Diagnostics = []string{"REQUIRED_EVIDENCE_NOT_VERIFIED", "REQUIRED_EVIDENCE_NOT_FOUND_AFTER_COMPLETE_SEARCH"}
				q.Basis = "Independent frozen corpus contract-absence audit plus separate scoped complete resolver search."
				proof := absenceProofs[f.ID]
				if proof == nil {
					var sources []core.SourceAdmission
					for _, s := range request.Sources {
						if s.Identity == f.SourceIdentity {
							s.ReadPaths = []string{f.Path}
							sources = append(sources, s)
						}
					}
					snapshot, err := engine.Loader.Load(context.Background(), sources, request.Limits)
					if err != nil {
						r.Failures = append(r.Failures, "scoped absence acquisition failed")
						break
					}
					resolver := engine.Resolvers[f.Resolver]
					if resolver == nil {
						r.Failures = append(r.Failures, "scoped absence resolver unavailable")
						break
					}
					found, err := resolver.Resolve(context.Background(), p.RetrievalPlan, f, snapshot)
					if err != nil || len(found.Candidates) != 0 || len(found.Coverage) != 1 || len(snapshot.Sources) != 1 {
						r.Failures = append(r.Failures, "scoped absence proof failed")
						break
					}
					proof = &evaluation.ScopedAbsenceProof{Snapshot: snapshot.Sources[0].Snapshot, Scope: f.Path, Coverage: found.Coverage[0]}
					absenceProofs[f.ID] = proof
				}
				q.AbsenceProof = proof
			}
			req = append(req, q)
		}
		return req
	}
	pf, ef := allFacets(pack), allFacets(expanded)
	prepare, expand := append([]evaluation.CapabilityRequirement{}, o.Requirements["prepare"]...), append([]evaluation.CapabilityRequirement{}, o.Requirements["expand"]...)
	for _, req := range [][]evaluation.CapabilityRequirement{prepare, expand} {
		for n := range req {
			if req[n].Class == evaluation.MissingBusinessContract {
				req[n].AbsenceProof = absenceProofs[req[n].Selector.Facet]
			}
		}
	}
	r.Prepare = evaluation.EvaluateCapabilities(pack, prepare)
	r.Expand = evaluation.EvaluateCapabilities(expanded, expand)
	r.PrepareFacets = evaluation.EvaluateCapabilities(pack, pf)
	r.ExpandFacets = evaluation.EvaluateCapabilities(expanded, ef)
	for _, report := range []evaluation.CapabilityReport{r.Prepare, r.Expand, r.PrepareFacets, r.ExpandFacets} {
		r.Failures = append(r.Failures, report.Failures...)
	}
	if !r.OwnerCorrect {
		r.Failures = append(r.Failures, "WRONG_OWNER")
	}
	if !r.LayersCorrect {
		r.Failures = append(r.Failures, "WRONG_LAYER")
	}
	duplicates := 0
	for i, e := range expanded.Evidence {
		for _, prior := range expanded.Evidence[:i] {
			if prior.SourceIdentity == e.SourceIdentity && prior.SourceRevision == e.SourceRevision && prior.ContentHash == e.ContentHash && prior.RelativePath == e.RelativePath && prior.Symbol == e.Symbol && prior.EvidenceKind == e.EvidenceKind && prior.ClaimKey == e.ClaimKey && prior.ClaimValue == e.ClaimValue && prior.Span.StartLine <= e.Span.EndLine && e.Span.StartLine <= prior.Span.EndLine {
				duplicates++
				break
			}
		}
	}
	if len(expanded.Evidence) > 0 {
		r.DuplicateRatio = float64(duplicates) / float64(len(expanded.Evidence))
	}
	r.AbsenceProofs = absenceProofs
	if o.OwnerRequirement != nil && !validWave1OwnerGap(expanded, o.OwnerRequirement) {
		r.Failures = append(r.Failures, "external owner gap lacks current audited evidence or honest partial status")
	}
	r.Classification = "PASS"
	if r.ExpandFacets.Categories[evaluation.UnsupportedSemantic].Expected > 0 {
		r.Classification = "VALID_UNSUPPORTED"
	}
	if r.ExpandFacets.Categories[evaluation.SecurityExcluded].Expected > 0 || r.ExpandFacets.Categories[evaluation.MissingBusinessContract].Expected > 0 || o.OwnerRequirement != nil {
		r.Classification = "VALID_PARTIAL"
	}
	if expanded.Status == core.Blocked {
		r.Classification = "VALID_BLOCKED"
	}
	r.Passed = len(r.Failures) == 0
	return r
}
func TestClosureImmutableInputAndIndependentAudits(t *testing.T) {
	directory := filepath.Join("..", "..", "..", "test", "fixtures", "context-retrieval", "wave1")
	raw, err := os.ReadFile(filepath.Join(directory, "closure-v2", "acceptance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var o wave1ClosureOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	if err := verifyWave1ClosureOverlay(directory, o); err != nil {
		t.Fatal(err)
	}
}

// An external owner question is an independently versioned audit result, not an
// engine diagnostic. Current selected source hashes and a non-COMPLETE pack are
// required; its audit reference is checked before executing the original cases.
func validWave1OwnerGap(pack core.ContextPack, gap *wave1OwnerGap) bool {
	if gap == nil || gap.Scope != "independent_ownership_audit/v1" || gap.Class != evaluation.MissingOwnerContract || gap.Diagnostic != "BUSINESS_OWNERSHIP_DECISION_REQUIRED" || len(gap.AuditHash) != 64 || gap.Basis == "" || len(gap.Evidence) < 2 || pack.Status == core.Complete {
		return false
	}
	for _, proof := range gap.Evidence {
		found := false
		for _, e := range pack.Evidence {
			found = found || (e.SourceIdentity == proof.Source && e.RelativePath == proof.Path && e.ContentHash == proof.Hash && e.Freshness != core.StaleEvidence && e.Provenance != core.ExternalContent && e.Provenance != core.GeneratedContent)
		}
		if !found {
			return false
		}
	}
	return true
}

func TestClosureOwnerGapRequiresAuditedEvidence(t *testing.T) {
	gap := &wave1OwnerGap{Class: evaluation.MissingOwnerContract, Diagnostic: "BUSINESS_OWNERSHIP_DECISION_REQUIRED", Basis: "owner question"}
	c := wave1ClosureCase{Owner: "local:fixture", OwnerRequirement: gap, Requirements: map[string][]evaluation.CapabilityRequirement{}}
	pack := core.ContextPack{Status: core.Partial, RouteRef: core.RouteContext{Owners: []string{"local:fixture"}}}
	result := classifyWave1Closure(c, core.RetrievalRequest{}, pack, pack, nil, NewEngine())
	if result.Passed {
		t.Fatal("owner label bypassed proof gate")
	}
	gap.Scope = "independent_ownership_audit/v1"
	gap.AuditHash = strings.Repeat("a", 64)
	pack.Status = core.Complete
	if validWave1OwnerGap(pack, gap) {
		t.Fatal("owner gap downgraded false COMPLETE to a valid partial")
	}
}
