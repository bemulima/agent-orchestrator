package contextretrieval

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type ArchitectureGraphResolver struct{}

func (ArchitectureGraphResolver) ID() string      { return "architecture" }
func (ArchitectureGraphResolver) Version() string { return "project.v1" }

// Resolve reads an explicitly selected existing portable graph artifact. It
// never builds a graph, searches for a new owner or admits neighboring roots.
func (r ArchitectureGraphResolver) Resolve(ctx context.Context, _ core.RetrievalPlan, facet core.Facet, snapshot core.SnapshotResult) (core.RetrievalResult, error) {
	if facet.QueryKind != core.QueryArchitecture {
		return unsupportedResult(facet, r.ID()), nil
	}
	if err := ctx.Err(); err != nil {
		return core.RetrievalResult{}, err
	}
	if facet.Path == "" {
		return unavailableFacet(facet, core.NotVerified, "ARCHITECTURE_GRAPH_NOT_PROVIDED", "A caller-admitted existing graph artifact path is required."), nil
	}
	doc, found := LookupDocument(snapshot, facet.SourceIdentity, facet.Path)
	if !found {
		return core.RetrievalResult{Coverage: []core.CoverageResult{CoverageForFacet(snapshot, facet, 0)}}, nil
	}
	graph, err := ParsePinnedArchitectureGraph([]byte(doc.Content))
	if err != nil {
		result := unavailableFacet(facet, core.NotVerified, "ARCHITECTURE_GRAPH_INVALID", "The existing graph failed schema, semantic digest, stable identity or declaration pin validation.")
		result.Coverage[0].Status = core.Invalid
		result.Diagnostics[0].Status = core.Invalid
		return result, nil
	}
	candidate := CandidateFromDocument(doc, facet, r.ID(), r.Version(), core.EvidenceSpan{}, "architecture", core.GeneratedContent)
	candidate.Authority = core.Authority{ClaimType: facet.ClaimType, Role: "pinned_declaration_projection", Basis: "existing architecture-graph.v1 identities and captured declaration pins"}
	candidate.Limitations = []string{"Graph neighbors are read-only references; they do not admit sources or authorize writes.",
		"Declaration pins describe captured architecture. They do not certify current implementation symbols, callers or a live complete fleet."}
	result := core.RetrievalResult{}
	if len(graph.References) == 0 {
		candidate.Freshness = core.Unverified
		result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "GRAPH_DECLARATIONS_UNAVAILABLE", Status: core.Partial,
			SourceIdentity: facet.SourceIdentity, FacetID: facet.ID, Message: "The graph contains no independently verifiable captured declarations."})
	}
	for _, reference := range graph.References {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		for _, pin := range reference.DeclarationPins {
			candidate.Links = append(candidate.Links, core.EvidenceLink{Kind: "captured_declaration", SourceIdentity: pin.SourceIdentity,
				RelativePath: pin.Path, ExpectedHash: pin.ContentSHA256})
			cited, available := LookupDocument(snapshot, pin.SourceIdentity, pin.Path)
			if !available {
				if candidate.Freshness != core.StaleEvidence {
					candidate.Freshness = core.Unverified
				}
				result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "GRAPH_DECLARATION_NOT_VERIFIED", Status: core.Partial,
					SourceIdentity: pin.SourceIdentity, RelativePath: pin.Path, FacetID: facet.ID,
					Message: "The captured declaration pin was not independently verified against an admitted current source."})
			} else if strings.TrimPrefix(cited.ContentHash, "sha256:") != pin.ContentSHA256 {
				candidate.Freshness = core.StaleEvidence
				result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "GRAPH_DECLARATION_CONTENT_STALE", Status: core.Stale,
					SourceIdentity: pin.SourceIdentity, RelativePath: pin.Path, FacetID: facet.ID,
					Message: "The captured declaration content hash differs from the admitted current document."})
			} else if capturedBlobHash(cited.Content, len(pin.BlobOID)) != pin.BlobOID {
				candidate.Freshness = core.Invalidated
				result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "GRAPH_DECLARATION_BLOB_INVALID", Status: core.Invalid,
					SourceIdentity: pin.SourceIdentity, RelativePath: pin.Path, FacetID: facet.ID,
					Message: "The captured Git blob OID does not match the admitted declaration bytes."})
			} else if !graphRevision.MatchString(cited.Source.Revision) {
				candidate.Limitations = append(candidate.Limitations, "CAPTURED_GIT_REVISION_UNVERIFIED: current declaration bytes match, but a local content snapshot does not certify the captured Git commit.")
				result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "CAPTURED_GIT_REVISION_UNVERIFIED", Status: core.Partial,
					SourceIdentity: pin.SourceIdentity, RelativePath: pin.Path, FacetID: facet.ID,
					Message: "Current declaration content matches the captured pin; the admitted source is a content snapshot, so the captured Git revision remains unverified."})
			} else if cited.Source.Revision != pin.CommitSHA {
				candidate.Freshness = core.StaleEvidence
				result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "ARCHITECTURE_GRAPH_SOURCE_REVISION_MISMATCH", Status: core.Stale,
					SourceIdentity: pin.SourceIdentity, RelativePath: pin.Path, FacetID: facet.ID,
					Message: "The caller-admitted Git revision differs from the captured declaration commit."})
			}
		}
		for _, source := range snapshot.Sources {
			if source.Identity == reference.SourceIdentity && reference.ReferenceKind != "external" && graphRevision.MatchString(source.Revision) && source.Revision != reference.CommitSHA {
				candidate.Freshness = core.StaleEvidence
				result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: "ARCHITECTURE_GRAPH_SOURCE_REVISION_MISMATCH", Status: core.Stale,
					SourceIdentity: source.Identity, FacetID: facet.ID, Message: "Captured graph source revision differs from the caller's source snapshot."})
			}
		}
		if (reference.IsDirty || !reference.SourceCurrent) && candidate.Freshness != core.StaleEvidence {
			candidate.Freshness = core.Unverified
		}
	}
	for _, edge := range graph.Edges {
		// Stable graph reference IDs are links, not assumed source identities.
		candidate.Links = append(candidate.Links, core.EvidenceLink{Kind: "graph_relation", EvidenceID: edge.EdgeID})
	}
	for _, diagnostic := range graph.Diagnostics {
		result.Diagnostics = append(result.Diagnostics, core.RetrievalDiagnostic{Code: diagnostic.Code, Status: core.Partial,
			SourceIdentity: facet.SourceIdentity, RelativePath: diagnostic.Path, FacetID: facet.ID,
			Message: "Captured graph reports an unresolved coverage or provenance condition."})
	}
	result.Candidates = []core.EvidenceCandidate{candidate}
	coverage := CoverageForFacet(snapshot, facet, 1)
	if len(result.Diagnostics) > 0 {
		coverage.Complete, coverage.Status = false, core.Partial
		coverage.Reasons = append(coverage.Reasons, "captured graph has unresolved diagnostics or source drift")
	}
	result.Coverage = []core.CoverageResult{coverage}
	return result, nil
}

func capturedBlobHash(content string, length int) string {
	object := append([]byte(fmt.Sprintf("blob %d\x00", len(content))), []byte(content)...)
	if length == 40 {
		digest := sha1.Sum(object)
		return hex.EncodeToString(digest[:])
	}
	digest := sha256.Sum256(object)
	return hex.EncodeToString(digest[:])
}

var graphRevision = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
var graphContentHash = regexp.MustCompile(`^[a-f0-9]{64}$`)
var graphProducer = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ParsePinnedArchitectureGraph decodes the existing graph model and reuses its
// canonical digest, stable IDs and declaration-pin validation. It is an input
// adapter, not another graph builder or another graph schema.
func ParsePinnedArchitectureGraph(raw []byte) (domain.ArchitectureGraph, error) {
	var graph domain.ArchitectureGraph
	if len(raw) == 0 || len(raw) > 8<<20 {
		return graph, fmt.Errorf("bounded architecture graph required")
	}
	if err := uniqueJSONKeys(json.NewDecoder(bytes.NewReader(raw)), 0); err != nil {
		return graph, err
	}
	var shape any
	if err := json.Unmarshal(raw, &shape); err != nil {
		return graph, err
	}
	if err := existingJSONShape(shape, reflect.TypeOf(graph)); err != nil {
		return graph, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&graph); err != nil {
		return graph, fmt.Errorf("decode existing architecture graph: %w", err)
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return graph, fmt.Errorf("trailing architecture graph data")
	}
	if graph.SchemaVersion != domain.ArchitectureGraphSchemaV1 || graph.Mode != domain.ArchitectureCatalogModeCurrent ||
		!graphProducer.MatchString(graph.Producer.RepositoryID) || !graphRevision.MatchString(graph.Producer.CommitSHA) || !graphContentHash.MatchString(graph.ContentSHA256) ||
		graph.References == nil || graph.Edges == nil || graph.Diagnostics == nil {
		return graph, fmt.Errorf("existing graph schema, mode or producer invalid")
	}
	claimed := graph.ContentSHA256
	graph.ContentSHA256 = ""
	canonical, err := architecturecatalog.CanonicalGraphJSON(graph)
	if err != nil {
		return graph, err
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != claimed {
		return graph, fmt.Errorf("architecture graph semantic digest mismatch")
	}
	graph.ContentSHA256 = claimed
	references := map[string]domain.ArchitectureGraphReference{}
	for _, reference := range graph.References {
		if reference.SourceIdentity == "" || reference.ManifestID == "" ||
			reference.ReferenceID != architecturecatalog.StableReferenceID(reference.SourceIdentity, reference.ManifestID) ||
			(reference.ReferenceKind != "" && reference.ReferenceKind != "repository" && reference.ReferenceKind != "external") {
			return graph, fmt.Errorf("invalid graph reference identity")
		}
		if _, duplicate := references[reference.ReferenceID]; duplicate {
			return graph, fmt.Errorf("duplicate graph reference")
		}
		for _, pin := range reference.DeclarationPins {
			if !architecturecatalog.ValidGraphPin(pin) || (reference.ReferenceKind != "external" && (pin.SourceIdentity != reference.SourceIdentity || pin.CommitSHA != reference.CommitSHA)) {
				return graph, fmt.Errorf("invalid reference declaration pin")
			}
		}
		switch reference.RepositoryRole {
		case domain.RepositoryRoleService, domain.RepositoryRoleFrontend, domain.RepositoryRoleInfrastructure, domain.RepositoryRoleContent,
			domain.RepositoryRolePolicy, domain.RepositoryRoleDocumentation, domain.RepositoryRoleArchive, domain.RepositoryRoleUnknown:
		default:
			return graph, fmt.Errorf("invalid graph repository role")
		}
		if reference.ReferenceKind == "external" && (!strings.HasPrefix(reference.SourceIdentity, "external:") || len(reference.DeclarationPins) == 0 || reference.CommitSHA != reference.DeclarationPins[0].CommitSHA) {
			return graph, fmt.Errorf("invalid external reference provenance")
		}
		references[reference.ReferenceID] = reference
	}
	edgeIDs := map[string]bool{}
	for _, edge := range graph.Edges {
		if _, exists := references[edge.SourceReferenceID]; !exists {
			return graph, fmt.Errorf("graph edge source unavailable")
		}
		if edge.TargetReferenceID != "" {
			if _, exists := references[edge.TargetReferenceID]; !exists {
				return graph, fmt.Errorf("graph edge target unavailable")
			}
		}
		if edge.EdgeID == "" || edge.EdgeID != architecturecatalog.StableEdgeID(edge.SourceReferenceID, string(edge.Relation), edge.TargetReferenceID, edge.ExternalTarget,
			edge.OperationID, edge.Transport, edge.Contract, edge.Direction) || edgeIDs[edge.EdgeID] {
			return graph, fmt.Errorf("invalid or duplicate stable edge identity")
		}
		switch edge.Relation {
		case domain.ArchitectureCatalogRelationOutboundDependency, domain.ArchitectureCatalogRelationOperationInteraction, domain.ArchitectureCatalogRelationContract, domain.ArchitectureCatalogRelationEvent:
		default:
			return graph, fmt.Errorf("unsupported existing graph relation")
		}
		edgeIDs[edge.EdgeID] = true
		for _, pin := range edge.DeclarationPins {
			if !architecturecatalog.ValidGraphPin(pin) {
				return graph, fmt.Errorf("invalid edge declaration pin")
			}
		}
	}
	for _, diagnostic := range graph.Diagnostics {
		if (diagnostic.Severity != "BLOCKED" && diagnostic.Severity != "FAIL") || diagnostic.Code == "" || diagnostic.Message == "" {
			return graph, fmt.Errorf("invalid captured graph diagnostic")
		}
	}
	if graph.Inventory != nil {
		if !architecturecatalog.ValidGraphPin(graph.Inventory.Pin) {
			return graph, fmt.Errorf("invalid graph inventory pin")
		}
		// The graph artifact pins the inventory record; raw inventory bytes
		// must be supplied separately to VerifyInventory for certification.
	}
	return graph, nil
}

// The domain's JSON tags define required/optional field spellings. Checking
// those tags also prevents Go's case-insensitive struct decoding from accepting
// omitted required fields, without introducing another architecture schema.
func existingJSONShape(value any, target reflect.Type) error {
	if target.Kind() == reflect.Pointer {
		if value == nil {
			return fmt.Errorf("null architecture graph object")
		}
		return existingJSONShape(value, target.Elem())
	}
	switch target.Kind() {
	case reflect.Struct:
		object, valid := value.(map[string]any)
		if !valid {
			return fmt.Errorf("architecture graph object required")
		}
		fields := map[string]reflect.StructField{}
		for i := 0; i < target.NumField(); i++ {
			field := target.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			if tag[0] == "-" || tag[0] == "" {
				continue
			}
			fields[tag[0]] = field
			child, found := object[tag[0]]
			optional := len(tag) > 1 && tag[1] == "omitempty"
			if !found {
				if optional {
					continue
				}
				return fmt.Errorf("required graph field %s missing", tag[0])
			}
			if err := existingJSONShape(child, field.Type); err != nil {
				return err
			}
		}
		for name := range object {
			if _, known := fields[name]; !known {
				return fmt.Errorf("unknown or incorrectly spelled graph field")
			}
		}
	case reflect.Slice, reflect.Array:
		array, valid := value.([]any)
		if !valid {
			return fmt.Errorf("graph array required")
		}
		for _, child := range array {
			if err := existingJSONShape(child, target.Elem()); err != nil {
				return err
			}
		}
	default:
		if value == nil {
			return fmt.Errorf("non-null graph scalar required")
		}
		if target.Kind() == reflect.Int {
			if number, ok := value.(float64); !ok || number < 0 {
				return fmt.Errorf("nonnegative graph counter required")
			}
		}
	}
	return nil
}

func uniqueJSONKeys(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("architecture graph JSON depth exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			name, valid := key.(string)
			if err != nil || !valid || seen[name] {
				return fmt.Errorf("duplicate or malformed architecture graph key")
			}
			seen[name] = true
			if err := uniqueJSONKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := uniqueJSONKeys(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid architecture graph JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}
