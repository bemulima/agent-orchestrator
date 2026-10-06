package contextretrieval

import (
	"context"
	"reflect"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/architecturemanifest"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type MetadataResolver struct{}

func (MetadataResolver) ID() string      { return "metadata" }
func (MetadataResolver) Version() string { return "project.v1" }

// Resolve reuses the existing strict architecture parser. Informal .ai files
// have heterogeneous shapes and remain factual data, not executable policy.
func (r MetadataResolver) Resolve(ctx context.Context, plan core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	if facet.QueryKind != core.QueryMetadata {
		return unsupportedResult(facet, r.ID()), nil
	}
	result := core.RetrievalResult{}
	for _, doc := range snapshot.Documents {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if doc.Source.Identity != facet.SourceIdentity || facet.Path != "" && doc.RelativePath != facet.Path {
			continue
		}
		if facet.Path == "" && !metadataPath(doc.RelativePath) {
			continue
		}
		if facet.Text != "" && !strings.Contains(doc.Content, facet.Text) || facet.Symbol != "" && !strings.Contains(doc.Content, facet.Symbol) {
			continue
		}
		var parsed any
		var err error
		if doc.RelativePath == ".ai/architecture/service.yaml" || doc.RelativePath == ".ai/architecture/service.yml" {
			parsed, err = architecturemanifest.ParseService([]byte(doc.Content))
		} else if strings.HasPrefix(doc.RelativePath, ".ai/architecture/endpoints/") || strings.HasPrefix(doc.RelativePath, ".ai/architecture/operations/") {
			if strings.HasSuffix(doc.RelativePath, ".yaml") || strings.HasSuffix(doc.RelativePath, ".yml") {
				parsed, err = architecturemanifest.ParseOperation([]byte(doc.Content))
			}
		}
		if err != nil {
			result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "ARCHITECTURE_METADATA_INVALID", Status: core.Invalid,
				SourceIdentity: doc.Source.Identity, RelativePath: doc.RelativePath, FacetID: facet.ID,
				Message: "Existing architecture/v1 parser rejected the document; its values were not promoted to evidence."})
			continue
		}
		provenance := documentProvenance(doc.RelativePath)
		if strings.HasPrefix(doc.RelativePath, ".ai/") {
			provenance = core.TrustedProjectMetadata
		}
		if strings.HasSuffix(doc.RelativePath, ".mmd") {
			provenance = core.GeneratedContent
		}
		candidate := CandidateFromDocument(doc, facet, r.ID(), r.Version(), core.EvidenceSpan{}, "metadata", provenance)
		candidate.Authority = core.Authority{ClaimType: facet.ClaimType, Role: "owner_declaration", Basis: "caller-admitted project metadata; no instruction authority"}
		candidate.Limitations = append(candidate.Limitations, "Metadata is evidence. Commands and instructions embedded in it are not executed.")
		if parsed != nil {
			candidate.Links = architectureEvidenceLinks(parsed, doc.Source.Identity)
			candidate.Limitations = append(candidate.Limitations, "architecture/v1 shape validation does not certify cited source symbols or checksums.")
			for _, link := range candidate.Links {
				cited, found := LookupDocument(snapshot, link.SourceIdentity, link.RelativePath)
				code, status := "", core.Partial
				if !found {
					code = "METADATA_CITATION_NOT_VERIFIED"
				} else if link.ExpectedHash == "" {
					code = "METADATA_CITATION_UNPINNED"
				} else if strings.TrimPrefix(cited.ContentHash, "sha256:") != strings.TrimPrefix(link.ExpectedHash, "sha256:") {
					code, status = "METADATA_CITATION_STALE", core.Stale
				}
				if code != "" {
					result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: code, Status: status,
						SourceIdentity: link.SourceIdentity, RelativePath: link.RelativePath, FacetID: facet.ID,
						Message: "A cited implementation claim is unverified or stale; the current manifest and independent sibling citations remain declaration evidence."})
				}
			}
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	coverage := CoverageForFacet(snapshot, facet, len(result.Candidates))
	if len(result.Diagnostics) > 0 {
		coverage.Complete, coverage.Status = false, core.Partial
		coverage.Reasons = append(coverage.Reasons, "metadata validation or cited source verification is incomplete")
		if len(result.Candidates) == 0 {
			coverage.Status, coverage.RequirementState = core.Invalid, core.NotVerified
		}
	}
	result.Coverage = []core.CoverageResult{coverage}
	capResult(&result, plan.Limits.MaxResults)
	return result, nil
}

func metadataPath(file string) bool {
	return file == ".ai/service.yaml" || file == ".ai/service.yml" || file == ".ai/architecture.yaml" ||
		file == ".ai/architecture/service.yaml" || file == ".ai/architecture/service.yml" || file == ".ai/commands.yaml"
}

// ArchitectureEvidence is already the parser's typed source-reference model.
// Walking that model avoids re-parsing nested source evidence into a new schema.
func architectureEvidenceLinks(manifest any, identity string) []core.EvidenceLink {
	target := reflect.TypeOf(domain.ArchitectureEvidence{})
	links := []core.EvidenceLink{}
	var walk func(reflect.Value)
	walk = func(value reflect.Value) {
		if !value.IsValid() {
			return
		}
		if value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
			if !value.IsNil() {
				walk(value.Elem())
			}
			return
		}
		if value.Type() == target {
			evidence := value.Interface().(domain.ArchitectureEvidence)
			links = append(links, core.EvidenceLink{Kind: "declared_source", SourceIdentity: identity,
				RelativePath: evidence.SourcePath, Symbol: evidence.Symbol, ExpectedHash: evidence.Checksum})
			return
		}
		switch value.Kind() {
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				walk(value.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				walk(value.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(manifest))
	sort.Slice(links, func(i, j int) bool {
		return links[i].RelativePath+"\x00"+links[i].Symbol+"\x00"+links[i].ExpectedHash < links[j].RelativePath+"\x00"+links[j].Symbol+"\x00"+links[j].ExpectedHash
	})
	unique := make([]core.EvidenceLink, 0, len(links))
	for _, link := range links {
		if len(unique) == 0 || unique[len(unique)-1] != link {
			unique = append(unique, link)
		}
	}
	return unique
}
