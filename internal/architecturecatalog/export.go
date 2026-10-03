package architecturecatalog

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

var graphGitSHA = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
var graphSHA256 = regexp.MustCompile(`^[a-f0-9]{64}$`)
var graphRepositoryID = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Export projects only captured CURRENT records. Pins must be independently
// resolved from immutable objects; absent or mismatching pins stay diagnostic.
// Edge pins are declaration bundles for both endpoints, not inferred ownership
// of the catalog's merged, repository-relative evidence paths.
func Export(catalog domain.ArchitectureCatalog, sources []domain.ArchitectureCatalogSource, producer domain.ArchitectureGraphProducer, pins []domain.ArchitectureGraphPin, diagnostics []domain.ArchitectureGraphDiagnostic, inventories ...VerifiedInventory) (domain.ArchitectureGraph, error) {
	if catalog.Mode != domain.ArchitectureCatalogModeCurrent || !graphRepositoryID.MatchString(producer.RepositoryID) || !graphGitSHA.MatchString(producer.CommitSHA) {
		return domain.ArchitectureGraph{}, fmt.Errorf("CURRENT mode and independently pinned producer required: %w", domain.ErrValidation)
	}
	if err := ValidateStableGraphIDs(catalog); err != nil {
		return domain.ArchitectureGraph{}, err
	}
	// Bind the transport projection to the actual supplied owner declarations,
	// rather than accepting a fabricated but internally consistent edge tuple.
	captured, err := (Builder{}).Build(context.Background(), sources)
	if err != nil {
		return domain.ArchitectureGraph{}, err
	}
	actualFingerprint, err := fingerprint(catalog)
	if err != nil {
		return domain.ArchitectureGraph{}, err
	}
	if actualFingerprint != captured.Fingerprint || catalog.Fingerprint != captured.Fingerprint {
		return domain.ArchitectureGraph{}, fmt.Errorf("catalog differs from captured owner declarations: %w", domain.ErrConflict)
	}
	byProject := make(map[string]domain.ArchitectureCatalogSource, len(sources))
	for _, source := range sources {
		id := source.Topology.Project.ID
		if _, exists := byProject[id]; exists {
			return domain.ArchitectureGraph{}, fmt.Errorf("duplicate export source: %w", domain.ErrConflict)
		}
		byProject[id] = source
	}
	if len(byProject) != len(catalog.Platform.Services) {
		return domain.ArchitectureGraph{}, fmt.Errorf("export source set differs from CURRENT: %w", domain.ErrConflict)
	}
	pinByKey := make(map[string]domain.ArchitectureGraphPin)
	for _, pin := range pins {
		if !ValidGraphPin(pin) {
			return domain.ArchitectureGraph{}, fmt.Errorf("invalid immutable declaration pin: %w", domain.ErrValidation)
		}
		key := pin.SourceIdentity + "\x00" + pin.CommitSHA + "\x00" + pin.Path
		if _, exists := pinByKey[key]; exists {
			return domain.ArchitectureGraph{}, fmt.Errorf("duplicate immutable declaration pin: %w", domain.ErrConflict)
		}
		pinByKey[key] = pin
	}
	repositoryCompleteness := emptyCompleteness()
	for _, service := range catalog.Platform.Services {
		if !byProject[service.Source.ProjectID].PinnedExternalOwner {
			addCompleteness(&repositoryCompleteness, service.Completeness)
		}
	}
	result := domain.ArchitectureGraph{SchemaVersion: domain.ArchitectureGraphSchemaV1, Mode: catalog.Mode, Producer: producer,
		References: []domain.ArchitectureGraphReference{}, Edges: []domain.ArchitectureGraphEdge{}, Completeness: repositoryCompleteness,
		Diagnostics: append([]domain.ArchitectureGraphDiagnostic{}, diagnostics...)}
	bundles := make(map[string][]domain.ArchitectureGraphPin)
	for _, service := range catalog.Platform.Services {
		source, exists := byProject[service.Source.ProjectID]
		if !exists {
			return domain.ArchitectureGraph{}, fmt.Errorf("CURRENT source missing: %w", domain.ErrConflict)
		}
		manifestID := "repository"
		if service.Manifest != nil {
			manifestID = service.Manifest.ID
		}
		identity := source.Topology.Project.SourceIdentity
		if service.Source.ReferenceID != StableReferenceID(identity, manifestID) || source.Topology.Snapshot.CommitSHA != service.Source.CommitSHA {
			return domain.ArchitectureGraph{}, fmt.Errorf("CURRENT source identity mismatch: %w", domain.ErrConflict)
		}
		ref := domain.ArchitectureGraphReference{ReferenceID: service.Source.ReferenceID, SourceIdentity: identity, ManifestID: manifestID,
			RepositoryRole: service.Source.RepositoryRole, CommitSHA: service.Source.CommitSHA, Covered: service.Covered,
			SourceCurrent: service.Source.SourceCurrent, IsDirty: service.Source.IsDirty, DeclarationPins: []domain.ArchitectureGraphPin{}}
		if source.PinnedExternalOwner {
			ref.ReferenceKind = "external_owner"
			ref.Classification = domain.ArchitectureFleetExternalOwnerClassification
		}
		add := func(code, message, file string) {
			result.Diagnostics = append(result.Diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: code, ReferenceID: ref.ReferenceID, Path: file, Message: message})
		}
		if !ref.Covered {
			add("SERVICE_MANIFEST_MISSING", "Source has no validated owner service manifest.", "")
		}
		if !ref.SourceCurrent {
			add("SOURCE_NOT_CURRENT", "Source does not match the captured CURRENT record.", "")
		}
		if ref.IsDirty {
			add("SOURCE_DIRTY", "Captured discovery includes uncommitted source state.", "")
		}
		if !graphGitSHA.MatchString(ref.CommitSHA) {
			add("SOURCE_COMMIT_INVALID", "Source lacks a full immutable commit SHA.", "")
		}
		if ref.Covered && len(source.Topology.Report.ArchitectureManifests) == 0 {
			add("DECLARATION_METADATA_MISSING", "Validated declaration metadata is unavailable.", "")
		}
		seen := make(map[string]bool)
		foundService := false
		operationIDs := make(map[string]bool)
		for _, metadata := range source.Topology.Report.ArchitectureManifests {
			if seen[metadata.Path] {
				return domain.ArchitectureGraph{}, fmt.Errorf("duplicate declaration metadata: %w", domain.ErrConflict)
			}
			seen[metadata.Path] = true
			if metadata.Kind == "service" && metadata.ID == manifestID {
				foundService = true
			}
			if metadata.Kind == "operation" {
				operationIDs[metadata.ID] = true
			}
			pin, exists := pinByKey[identity+"\x00"+ref.CommitSHA+"\x00"+metadata.Path]
			if !exists {
				add("DECLARATION_PIN_MISSING", "Declaration has no independently resolved immutable Git pin.", metadata.Path)
				continue
			}
			if pin.ContentSHA256 != metadata.Checksum {
				add("DECLARATION_CONTENT_MISMATCH", "Immutable declaration bytes differ from discovery metadata.", metadata.Path)
				continue
			}
			ref.DeclarationPins = append(ref.DeclarationPins, pin)
		}
		if ref.Covered && !foundService {
			add("SERVICE_DECLARATION_METADATA_MISSING", "Owner service declaration has no matching metadata.", "")
		}
		for _, operation := range source.Operations {
			if !operationIDs[operation.ID] {
				add("OPERATION_DECLARATION_METADATA_MISSING", "Owner operation declaration has no matching metadata.", operation.ID)
			}
		}
		if service.Completeness.MissingDeclaredOperationManifestCount > 0 || service.Completeness.OperationsMissingManifests > 0 || service.Completeness.OperationsBlocked > 0 || service.Completeness.MissingGroupOperationCount > 0 {
			add("DECLARATION_COVERAGE_INCOMPLETE", "Captured owner declaration coverage is incomplete.", "")
		}
		ref.DeclarationPins = sortedGraphPins(ref.DeclarationPins)
		bundles[ref.ReferenceID] = ref.DeclarationPins
		result.References = append(result.References, ref)
	}
	for _, relation := range catalog.Platform.Relations {
		edge := domain.ArchitectureGraphEdge{EdgeID: relation.EdgeID, Relation: relation.Type, SourceReferenceID: relation.SourceReferenceID,
			TargetReferenceID: relation.TargetReferenceID, ExternalTarget: relation.ExternalTarget, OperationID: relation.OperationID,
			Transport: relation.Transport, Contract: relation.Contract, Direction: relation.Direction,
			DeclarationPins: sortedGraphPins(append(append([]domain.ArchitectureGraphPin{}, bundles[relation.SourceReferenceID]...), bundles[relation.TargetReferenceID]...))}
		if edge.TargetReferenceID == "" {
			result.Diagnostics = append(result.Diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: "EDGE_TARGET_UNRESOLVED", EdgeID: edge.EdgeID, Message: "Owner-declared target is unresolved in the captured CURRENT scope."})
		}
		result.Edges = append(result.Edges, edge)
	}
	if len(result.References) == 0 {
		result.Diagnostics = append(result.Diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: "GRAPH_SCOPE_EMPTY", Message: "Captured CURRENT scope contains no sources."})
	}
	// Scope completeness requires the exact independently resolved CDO document.
	if len(inventories) > 1 {
		return domain.ArchitectureGraph{}, fmt.Errorf("multiple fleet inventories: %w", domain.ErrValidation)
	}
	scopeProven := false
	if len(inventories) == 1 && inventories[0].inventory != nil {
		candidate := inventories[0].inventory
		actual := make([]string, 0, len(result.References))
		for _, ref := range result.References {
			if ref.ReferenceKind != "external_owner" {
				actual = append(actual, ref.SourceIdentity)
			}
		}
		sort.Strings(actual)
		if strings.Join(actual, "\x00") == strings.Join(candidate.SourceIdentities, "\x00") {
			copied := *candidate
			copied.SourceIdentities = append([]string{}, candidate.SourceIdentities...)
			result.Inventory = &copied
			scopeProven = true
		} else {
			result.Diagnostics = append(result.Diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: "FLEET_INVENTORY_SCOPE_MISMATCH", Message: "Pinned owner inventory differs from the captured CURRENT source identity set."})
		}
	}
	if !scopeProven {
		result.Diagnostics = append(result.Diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: "FLEET_SCOPE_UNPROVEN", Message: "Export covers the captured CURRENT topology; authoritative full-fleet scope requires a separately reviewed pinned inventory."})
	}
	sort.Slice(result.References, func(i, j int) bool { return result.References[i].ReferenceID < result.References[j].ReferenceID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].EdgeID < result.Edges[j].EdgeID })
	sort.Slice(result.Diagnostics, func(i, j int) bool {
		return diagnosticKey(result.Diagnostics[i]) < diagnosticKey(result.Diagnostics[j])
	})
	dedup := result.Diagnostics[:0]
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Severity != "BLOCKED" && diagnostic.Severity != "FAIL" {
			return domain.ArchitectureGraph{}, fmt.Errorf("invalid export diagnostic: %w", domain.ErrValidation)
		}
		if len(dedup) == 0 || diagnosticKey(dedup[len(dedup)-1]) != diagnosticKey(diagnostic) {
			dedup = append(dedup, diagnostic)
		}
	}
	result.Diagnostics = dedup
	bytes, err := CanonicalGraphJSON(result)
	if err != nil {
		return domain.ArchitectureGraph{}, err
	}
	sum := sha256.Sum256(bytes)
	result.ContentSHA256 = hex.EncodeToString(sum[:])
	return result, nil
}

func ValidGraphPin(pin domain.ArchitectureGraphPin) bool {
	return strings.TrimSpace(pin.SourceIdentity) != "" && graphGitSHA.MatchString(pin.CommitSHA) && graphGitSHA.MatchString(pin.BlobOID) && graphSHA256.MatchString(pin.ContentSHA256) &&
		pin.Path != "" && !strings.ContainsAny(pin.Path, "\\\x00\r\n") && !strings.HasPrefix(pin.Path, "/") && path.Clean(pin.Path) == pin.Path && pin.Path != "." && pin.Path != ".." && !strings.HasPrefix(pin.Path, "../")
}
func sortedGraphPins(pins []domain.ArchitectureGraphPin) []domain.ArchitectureGraphPin {
	sort.Slice(pins, func(i, j int) bool { return graphPinKey(pins[i]) < graphPinKey(pins[j]) })
	result := make([]domain.ArchitectureGraphPin, 0, len(pins))
	for _, pin := range pins {
		if len(result) == 0 || graphPinKey(result[len(result)-1]) != graphPinKey(pin) {
			result = append(result, pin)
		}
	}
	return result
}
func graphPinKey(pin domain.ArchitectureGraphPin) string {
	return strings.Join([]string{pin.SourceIdentity, pin.Path, pin.BlobOID, pin.CommitSHA, pin.ContentSHA256}, "\x00")
}
func diagnosticKey(value domain.ArchitectureGraphDiagnostic) string {
	return strings.Join([]string{value.Severity, value.Code, value.ReferenceID, value.EdgeID, value.Path, value.Message}, "\x00")
}

// CanonicalGraphJSON emits recursively sorted object keys and compact UTF-8.
// Graph payloads contain only strings, booleans and integers. String escaping
// follows JSON.stringify (no HTML escaping, literal UTF-8 including U+2028).
func CanonicalGraphJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	var write func(any) error
	stringJSON := func(value string) []byte {
		var b bytes.Buffer
		b.WriteByte('"')
		for _, r := range value {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\t':
				b.WriteString(`\t`)
			case '\n':
				b.WriteString(`\n`)
			case '\f':
				b.WriteString(`\f`)
			case '\r':
				b.WriteString(`\r`)
			default:
				if r < 0x20 {
					fmt.Fprintf(&b, `\u%04x`, r)
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
		return b.Bytes()
	}
	write = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					out.WriteByte(',')
				}
				out.Write(stringJSON(k))
				out.WriteByte(':')
				if err := write(v[k]); err != nil {
					return err
				}
			}
			out.WriteByte('}')
		case []any:
			out.WriteByte('[')
			for i, x := range v {
				if i > 0 {
					out.WriteByte(',')
				}
				if err := write(x); err != nil {
					return err
				}
			}
			out.WriteByte(']')
		case string:
			out.Write(stringJSON(v))
		default:
			data, err := json.Marshal(v)
			if err != nil {
				return err
			}
			out.Write(data)
		}
		return nil
	}
	if err := write(decoded); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// VerifiedInventory can only be constructed by validating exact immutable bytes.
type VerifiedInventory struct {
	inventory *domain.ArchitectureGraphInventory
}

func VerifyInventory(pin domain.ArchitectureGraphPin, raw []byte, producer domain.ArchitectureGraphProducer) (VerifiedInventory, error) {
	if !ValidGraphPin(pin) || pin.SourceIdentity != "git:github.com/"+producer.RepositoryID || pin.CommitSHA != producer.CommitSHA {
		return VerifiedInventory{}, fmt.Errorf("inventory owner/producer pin mismatch: %w", domain.ErrConflict)
	}
	sum := sha256.Sum256(raw)
	if pin.ContentSHA256 != hex.EncodeToString(sum[:]) {
		return VerifiedInventory{}, fmt.Errorf("inventory content digest mismatch: %w", domain.ErrConflict)
	}
	object := append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...)
	oid := ""
	if len(pin.BlobOID) == 40 {
		sum := sha1.Sum(object)
		oid = hex.EncodeToString(sum[:])
	} else {
		sum := sha256.Sum256(object)
		oid = hex.EncodeToString(sum[:])
	}
	if oid != pin.BlobOID {
		return VerifiedInventory{}, fmt.Errorf("inventory blob digest mismatch: %w", domain.ErrConflict)
	}
	var document struct {
		SchemaVersion    string   `json:"schema_version"`
		SourceIdentities []string `json:"source_identities"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// Parse exact field names and reject repeated decoded keys, including escaped
	// spellings. Struct decoding silently accepts duplicates and case variants.
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return VerifiedInventory{}, fmt.Errorf("owner inventory object required: %w", domain.ErrValidation)
	}
	seen := make(map[string]bool, 2)
	for decoder.More() {
		token, err := decoder.Token()
		key, isString := token.(string)
		if err != nil || !isString {
			return VerifiedInventory{}, fmt.Errorf("invalid owner inventory key: %w", domain.ErrValidation)
		}
		if seen[key] {
			return VerifiedInventory{}, fmt.Errorf("duplicate owner inventory key: %w", domain.ErrValidation)
		}
		seen[key] = true
		switch key {
		case "schema_version":
			err = decoder.Decode(&document.SchemaVersion)
		case "source_identities":
			err = decoder.Decode(&document.SourceIdentities)
		default:
			return VerifiedInventory{}, fmt.Errorf("unknown owner inventory field: %w", domain.ErrValidation)
		}
		if err != nil {
			return VerifiedInventory{}, fmt.Errorf("invalid owner inventory value: %w", domain.ErrValidation)
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return VerifiedInventory{}, fmt.Errorf("invalid owner inventory object: %w", domain.ErrValidation)
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return VerifiedInventory{}, fmt.Errorf("trailing owner inventory data: %w", domain.ErrValidation)
	}
	if document.SchemaVersion != "architecture-graph-inventory.v1" || len(document.SourceIdentities) == 0 {
		return VerifiedInventory{}, fmt.Errorf("versioned nonempty owner inventory required: %w", domain.ErrValidation)
	}
	for i, identity := range document.SourceIdentities {
		if strings.TrimSpace(identity) != identity || identity == "" || strings.ContainsAny(identity, "\x00\r\n") || (i > 0 && document.SourceIdentities[i-1] >= identity) {
			return VerifiedInventory{}, fmt.Errorf("sorted unique owner identities required: %w", domain.ErrValidation)
		}
	}
	return VerifiedInventory{inventory: &domain.ArchitectureGraphInventory{Pin: pin, SourceIdentities: append([]string{}, document.SourceIdentities...)}}, nil
}
