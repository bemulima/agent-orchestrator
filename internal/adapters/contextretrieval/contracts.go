package contextretrieval

import (
	"context"
	"strings"

	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/contractref"
)

type ContractResolver struct{}

func (ContractResolver) ID() string      { return "contract" }
func (ContractResolver) Version() string { return "project.v1" }

func (r ContractResolver) Resolve(ctx context.Context, plan core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	if facet.QueryKind != core.QueryContract {
		return unsupportedResult(facet, r.ID()), nil
	}
	if err := ctx.Err(); err != nil {
		return core.RetrievalResult{}, err
	}
	if facet.Path == "" && facet.Text == "" && facet.Symbol == "" {
		return unavailableFacet(facet, core.NotVerified, "CONTRACT_SELECTOR_REQUIRED", "A contract owner path, symbol or exact reference is required."), nil
	}
	selected := snapshot
	selected.Documents = nil
	methodUnverified := map[string]bool{}
	reference := facet.Text
	if reference != "" {
		if method, path, valid := contractref.HTTP(reference); valid {
			reference = method + " " + path
		} else if event, valid := contractref.EventSubject(reference); valid {
			reference = event
		} else {
			return unavailableFacet(facet, core.RequirementUnsupported, "CONTRACT_REFERENCE_UNSUPPORTED", "The contract reference is not an exact HTTP or event reference; use a declared owner path or symbol."), nil
		}
	}
	for _, doc := range snapshot.Documents {
		if doc.Source.Identity != facet.SourceIdentity || facet.Path != "" && doc.RelativePath != facet.Path {
			continue
		}
		if facet.Path == "" && !(strings.HasPrefix(doc.RelativePath, ".ai/contracts/") || strings.HasPrefix(doc.RelativePath, ".ai/architecture/") || strings.HasSuffix(doc.RelativePath, ".go")) {
			continue
		}
		if reference != "" && !containsContractReference(doc.Content, reference) {
			// Authored YAML separates HTTP method/path, so the exact normalized
			// path may still be a literal match. This is lexical evidence only.
			_, path, http := contractref.HTTP(reference)
			if !http || !containsContractReference(doc.Content, path) {
				continue
			}
			methodUnverified[doc.RelativePath] = true
		}
		if facet.Symbol != "" && !strings.HasSuffix(doc.RelativePath, ".go") && !strings.Contains(doc.Content, facet.Symbol) {
			continue
		}
		selected.Documents = append(selected.Documents, doc)
	}
	var result core.RetrievalResult
	for _, doc := range selected.Documents {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		provenance := core.ProjectSource
		if strings.HasSuffix(doc.RelativePath, ".go") && facet.Symbol != "" {
			symbolFacet := facet
			symbolFacet.QueryKind = core.QueryDefinition
			symbolFacet.Path = doc.RelativePath
			definitions, err := (GoResolver{}).Resolve(ctx, plan, symbolFacet, selected)
			if err != nil {
				return result, err
			}
			result.Diagnostics = append(result.Diagnostics, definitions.Diagnostics...)
			for _, definition := range definitions.Candidates {
				candidate := CandidateFromDocument(doc, facet, r.ID(), r.Version(), definition.Span, "contract", provenance)
				candidate.Symbol = definition.Symbol
				candidate.Authority = core.Authority{ClaimType: facet.ClaimType, Role: "declared_contract", Basis: "existing Go declaration; no freeze or type-aware compatibility certification"}
				candidate.Limitations = append(candidate.Limitations, definition.Limitations...)
				candidate.Limitations = append(candidate.Limitations, "Contract retrieval does not prove freeze approval, baseline validity or provider/consumer compatibility.")
				result.Candidates = append(result.Candidates, candidate)
			}
			continue
		}
		if strings.HasPrefix(doc.RelativePath, ".ai/") {
			// Reuse strict architecture validation rather than accepting a
			// malformed declaration as a contract.
			metadataFacet := facet
			metadataFacet.QueryKind = core.QueryMetadata
			metadataFacet.Path = doc.RelativePath
			metadata, err := (MetadataResolver{}).Resolve(ctx, plan, metadataFacet, selected)
			if err != nil {
				return result, err
			}
			result.Diagnostics = append(result.Diagnostics, metadata.Diagnostics...)
			if len(metadata.Candidates) == 0 {
				continue
			}
			provenance = core.TrustedProjectMetadata
		}
		candidate := CandidateFromDocument(doc, facet, r.ID(), r.Version(), core.EvidenceSpan{}, "contract", provenance)
		candidate.Symbol = facet.Symbol
		candidate.Authority = core.Authority{ClaimType: facet.ClaimType, Role: "declared_contract", Basis: "existing caller-selected owner contract; freeze status not inferred"}
		candidate.Limitations = append(candidate.Limitations, "Contract retrieval does not prove freeze approval, baseline validity or provider/consumer compatibility.")
		if reference != "" || facet.Symbol != "" {
			candidate.Limitations = append(candidate.Limitations, "Contract reference association is exact lexical evidence, not type-aware resolution.")
		}
		if methodUnverified[doc.RelativePath] {
			candidate.ExactMatch = false
			candidate.ClaimValue = ""
			candidate.Limitations = append(candidate.Limitations, "Only the HTTP path matched; the requested method is NOT_VERIFIED.")
			result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "CONTRACT_HTTP_METHOD_NOT_VERIFIED", Status: core.Partial,
				SourceIdentity: facet.SourceIdentity, RelativePath: doc.RelativePath, FacetID: facet.ID, Message: "Lexical path evidence does not establish the requested HTTP method."})
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	coverage := CoverageForFacet(snapshot, facet, len(result.Candidates))
	if len(result.Diagnostics) > 0 {
		coverage.Complete, coverage.Status = false, core.Partial
		if len(result.Candidates) == 0 {
			coverage.RequirementState = core.NotVerified
		}
		if len(methodUnverified) > 0 {
			coverage.RequirementState = core.NotVerified
		}
	}
	result.Coverage = []core.CoverageResult{coverage}
	capResult(&result, plan.Limits.MaxResults)
	return result, nil
}

// A normalized contract literal must not match a longer subject or endpoint.
// This remains a lexical association and never implies runtime registration.
func containsContractReference(content, reference string) bool {
	referenceByte := func(value byte) bool {
		return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' ||
			strings.ContainsRune("_./-$*>{}", rune(value))
	}
	for start := 0; start < len(content); {
		index := strings.Index(content[start:], reference)
		if index < 0 {
			return false
		}
		index += start
		end := index + len(reference)
		if (index == 0 || !referenceByte(content[index-1])) && (end == len(content) || !referenceByte(content[end])) {
			return true
		}
		start = index + 1
	}
	return false
}

func unavailableFacet(facet core.Facet, state core.RequirementState, code, message string) core.RetrievalResult {
	status := core.Partial
	if state == core.RequirementUnsupported {
		status = core.Unsupported
	}
	return core.RetrievalResult{Coverage: []core.CoverageResult{{SourceIdentity: facet.SourceIdentity, Stage: facet.Resolver,
		FacetID: facet.ID, Status: status, RequirementState: state, Complete: false, Reasons: []string{code}}},
		Diagnostics: []core.RetrievalDiagnostic{{Code: code, Status: status, SourceIdentity: facet.SourceIdentity,
			FacetID: facet.ID, RelativePath: facet.Path, Message: message}}}
}
