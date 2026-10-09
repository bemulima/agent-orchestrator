package contextretrieval

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
)

var ErrProjectAnalysisBudget = errors.New("project analysis companion budget unsatisfied")

func projectSourceSetDigest(sources []core.EvidenceSource) string {
	copy := append([]core.EvidenceSource{}, sources...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Identity < copy[j].Identity })
	return hashJSON(copy)
}

// ProjectJointBudget certifies the current emitted bundle. The core separately
// tracks irreversible pack costs. Historical companion costs and a joint
// interpretation of Expand.RemainingBudget are not certified by this ledger.
type ProjectJointBudget struct {
	SourceBytes          int            `json:"source_bytes"`
	ContextTokens        int            `json:"context_tokens"`
	ReservedPromptTokens int            `json:"reserved_prompt_tokens"`
	PerSourceBytes       map[string]int `json:"per_source_bytes"`
	PerFacetTokens       map[string]int `json:"per_facet_tokens"`
	Estimator            string         `json:"estimator"`
}

func projectStaticAdmission(plan core.RetrievalPlan, current bool) string {
	plan.Sources = append([]core.SourceAdmission{}, plan.Sources...)
	for i := range plan.Sources {
		plan.Sources[i].ExpectedSnapshot = ""
	}
	sort.Slice(plan.Sources, func(i, j int) bool { return plan.Sources[i].Identity < plan.Sources[j].Identity })
	policies := append([]core.PolicyRegistration{}, plan.TrustedPolicies...)
	sort.Slice(policies, func(i, j int) bool { return hashJSON(policies[i]) < hashJSON(policies[j]) })
	versions := plan.AdapterVersions
	if current {
		versions = map[string]string{}
		for id, r := range NewProjectAnalysisEngine().Resolvers {
			if r != nil {
				versions[id] = r.Version()
			}
		}
	}
	return hashJSON(struct {
		Sources       []core.SourceAdmission
		Policies      []core.PolicyRegistration
		Budget        core.Budget
		Limits        core.Limits
		Versions      map[string]string
		Task, Purpose string
	}{plan.Sources, policies, plan.Budget, plan.Limits, versions, plan.Task, plan.Purpose})
}

func projectEvidenceUnit(e core.EvidenceCandidate) string {
	return hashJSON(struct {
		Source, Snapshot, Path, Hash, Content string
		Span                                  core.EvidenceSpan
	}{e.SourceIdentity, e.SourceSnapshot, e.RelativePath, e.ContentHash, e.Content, e.Span})
}

func finalizeProjectScope(scope *ProjectAnalysisScope) {
	scope.AnalysisScope.Digest = ""
	scope.AnalysisScope.Digest = hashJSON(scope.AnalysisScope)
	scope.BindingDigest = ""
	scope.BindingDigest = hashJSON(*scope)
}

func projectBudgetMeasure(pack core.ContextPack, scope *ProjectAnalysisScope) error {
	b := ProjectJointBudget{SourceBytes: pack.BudgetUsed.CumulativeSourceBytes, ReservedPromptTokens: pack.RetrievalPlan.Budget.ReservedPromptTokens, PerSourceBytes: map[string]int{}, PerFacetTokens: map[string]int{}, Estimator: "canonical-pack-plus-companion-json-utf8-div4-ceil.v1"}
	if b.SourceBytes < pack.BudgetUsed.SourceBytes {
		b.SourceBytes = pack.BudgetUsed.SourceBytes
	}
	units := map[string]bool{}
	for _, e := range pack.Evidence {
		units[projectEvidenceUnit(e)] = true
		b.PerSourceBytes[e.SourceIdentity] += len(e.Content)
		x, _ := json.Marshal(e)
		for _, f := range e.FacetIDs {
			if f != "" {
				b.PerFacetTokens[f] += (len(x) + 3) / 4
			}
		}
	}
	for _, l := range scope.Layers {
		k := projectEvidenceUnit(l.Evidence)
		if !units[k] {
			units[k] = true
			b.SourceBytes += len(l.Evidence.Content)
			b.PerSourceBytes[l.Source] += len(l.Evidence.Content)
		}
		encoded, _ := json.Marshal(l)
		// Conservatively charge each same-source exact retained selector. This is
		// budget attribution only; it does not claim the layer satisfies a facet.
		for _, f := range append(append([]core.Facet{}, pack.RetrievalPlan.RequiredFacets...), pack.RetrievalPlan.OptionalFacets...) {
			if f.ID != "" && f.SourceIdentity == l.Source && f.Path != "" && f.ExpectedHash != "" {
				b.PerFacetTokens[f.ID] += (len(encoded) + 3) / 4
			}
		}
	}
	for _, proof := range scope.BoundaryProofs {
		encoded, _ := json.Marshal(proof)
		if proof.FacetID != "" {
			b.PerFacetTokens[proof.FacetID] += (len(encoded) + 3) / 4
		}
	}
	for _, gap := range scope.ReadAreaGaps {
		encoded, _ := json.Marshal(gap)
		for _, id := range gap.FacetIDs {
			b.PerFacetTokens[id] += (len(encoded) + 3) / 4
		}
	}
	packBytes, err := core.CanonicalJSON(pack)
	if err != nil {
		return err
	}
	scope.JointBudgetUsed = b
	for i := 0; i < 16; i++ {
		finalizeProjectScope(scope)
		body, err := json.Marshal(scope)
		if err != nil {
			return err
		}
		tokens := (len(packBytes) + len(body) + 2 + 3) / 4
		if tokens == scope.JointBudgetUsed.ContextTokens {
			return nil
		}
		scope.JointBudgetUsed.ContextTokens = tokens
	}
	return fmt.Errorf("%w: context accounting did not stabilize", ErrProjectAnalysisBudget)
}

func projectBudgetViolation(budget core.Budget, used ProjectJointBudget) string {
	if budget.MaxSourceBytes > 0 && used.SourceBytes > budget.MaxSourceBytes {
		return "SOURCE_BYTES"
	}
	if budget.MaxContextTokens > 0 && used.ContextTokens+used.ReservedPromptTokens > budget.MaxContextTokens {
		return "CONTEXT_TOKENS"
	}
	if budget.PerSourceBytes > 0 {
		for _, v := range used.PerSourceBytes {
			if v > budget.PerSourceBytes {
				return "PER_SOURCE_BYTES"
			}
		}
	}
	if budget.PerFacetTokens > 0 {
		for _, v := range used.PerFacetTokens {
			if v > budget.PerFacetTokens {
				return "PER_FACET_TOKENS"
			}
		}
	}
	return ""
}

func boundProjectBudget(pack core.ContextPack, scope ProjectAnalysisScope) (ProjectAnalysisScope, error) {
	all := append([]AnalysisLayer{}, scope.Layers...)
	sort.Slice(all, func(i, j int) bool { return hashJSON(all[i]) < hashJSON(all[j]) })
	scope.Layers = []AnalysisLayer{}
	scope.BudgetDiagnostics = []core.RetrievalDiagnostic{}
	scope.JointBudgetStatus = core.Complete
	scope.Limitations = append(scope.Limitations, "Joint ledger bounds this emitted pack and companion. Historical companion irreversible costs and joint Expand RemainingBudget are not certified; core pack accounting remains separate.")
	omit := func(l AnalysisLayer, reason string) {
		scope.JointBudgetStatus = core.Blocked
		scope.BudgetDiagnostics = append(scope.BudgetDiagnostics, core.RetrievalDiagnostic{Code: "ANALYSIS_COMPANION_BUDGET_UNSATISFIED", Status: core.Blocked, SourceIdentity: l.Source, RelativePath: l.Witness.Path, Message: "Read layer " + l.Layer + " omitted as a whole evidence unit: " + reason})
	}
	for _, l := range all {
		trial := scope
		trial.Layers = append(append([]AnalysisLayer{}, scope.Layers...), l)
		if err := projectBudgetMeasure(pack, &trial); err != nil {
			return ProjectAnalysisScope{}, err
		}
		if why := projectBudgetViolation(pack.RetrievalPlan.Budget, trial.JointBudgetUsed); why != "" {
			omit(l, why)
			continue
		}
		scope = trial
	}
	// Diagnostic and digest bytes themselves consume context. Removing complete
	// units, rather than truncating source, makes the final certificate honest.
	for {
		if err := projectBudgetMeasure(pack, &scope); err != nil {
			return ProjectAnalysisScope{}, err
		}
		why := projectBudgetViolation(pack.RetrievalPlan.Budget, scope.JointBudgetUsed)
		if why == "" {
			break
		}
		if len(scope.Layers) == 0 {
			return ProjectAnalysisScope{}, fmt.Errorf("%w: minimal report envelope exceeds %s", ErrProjectAnalysisBudget, why)
		}
		last := scope.Layers[len(scope.Layers)-1]
		scope.Layers = scope.Layers[:len(scope.Layers)-1]
		omit(last, why)
	}
	sort.Slice(scope.Layers, func(i, j int) bool {
		return scope.Layers[i].Source+scope.Layers[i].Layer+scope.Layers[i].Witness.Path < scope.Layers[j].Source+scope.Layers[j].Layer+scope.Layers[j].Witness.Path
	})
	sort.Slice(scope.BudgetDiagnostics, func(i, j int) bool {
		return strings.Compare(hashJSON(scope.BudgetDiagnostics[i]), hashJSON(scope.BudgetDiagnostics[j])) < 0
	})
	if err := projectBudgetMeasure(pack, &scope); err != nil {
		return ProjectAnalysisScope{}, err
	}
	return scope, nil
}
