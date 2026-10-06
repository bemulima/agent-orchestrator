package contextretrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

var (
	ErrInvalidRequest = errors.New("invalid retrieval request")
	ErrStaleBase      = errors.New("stale context base")
	ErrScopeChange    = errors.New("context admission changed")
)

// BuildPlan projects caller-admitted sources and existing routing; it grants no
// execution permission. Admission is explicit even for read-only neighbors.
func BuildPlan(request RetrievalRequest) (RetrievalPlan, error) {
	if request.SchemaVersion != "" && request.SchemaVersion != SchemaVersion {
		return RetrievalPlan{}, fmt.Errorf("%w: schema version", ErrInvalidRequest)
	}
	if len(request.Sources) == 0 || len(request.Sources) > 64 || strings.TrimSpace(request.Route.Digest) == "" {
		return RetrievalPlan{}, fmt.Errorf("%w: admitted sources and route digest required", ErrInvalidRequest)
	}
	if len(request.Task) > 65536 || len(request.RequestID) > 256 || len(request.Purpose) > 256 {
		return RetrievalPlan{}, fmt.Errorf("%w: oversized request", ErrInvalidRequest)
	}
	for _, char := range request.RequestID {
		if char < 32 || char == 127 {
			return RetrievalPlan{}, fmt.Errorf("%w: request identity control characters", ErrInvalidRequest)
		}
	}
	limits, err := normalizeLimits(request.Limits)
	if err != nil {
		return RetrievalPlan{}, err
	}
	budget, err := normalizeBudget(request.Budget)
	if err != nil {
		return RetrievalPlan{}, err
	}
	plan := RetrievalPlan{SchemaVersion: SchemaVersion, RequestID: request.RequestID, Task: request.Task, Purpose: request.Purpose, Route: request.Route, Budget: budget, Limits: limits}
	routeBytes, err := json.Marshal(request.Route)
	if err != nil {
		return RetrievalPlan{}, ErrInvalidRequest
	}
	if err = json.Unmarshal(routeBytes, &plan.Route); err != nil {
		return RetrievalPlan{}, ErrInvalidRequest
	}
	admitted := map[string]SourceAdmission{}
	for _, source := range request.Sources {
		if source.Identity == "" || len(source.Identity) > 256 || strings.ContainsAny(source.Identity, "\x00\n\r\\") || path.IsAbs(source.Identity) || strings.HasPrefix(source.Identity, "local:/") || strings.HasPrefix(source.Identity, "file:/") || source.Root == "" || len(source.ReadPaths) == 0 || len(source.ReadPaths) > 256 {
			return RetrievalPlan{}, fmt.Errorf("%w: explicit source identity/root/read paths required", ErrInvalidRequest)
		}
		if _, exists := admitted[source.Identity]; exists {
			return RetrievalPlan{}, fmt.Errorf("%w: duplicate source", ErrInvalidRequest)
		}
		source.ReadPaths = sortedUnique(source.ReadPaths)
		source.ExcludePaths = sortedUnique(source.ExcludePaths)
		for _, p := range append(append([]string{}, source.ReadPaths...), source.ExcludePaths...) {
			if p != "." && !SafeRelativePath(p) {
				return RetrievalPlan{}, fmt.Errorf("%w: unsafe scope path", ErrInvalidRequest)
			}
		}
		admitted[source.Identity] = source
		plan.Sources = append(plan.Sources, source)
		plan.ForbiddenScope.Write = append(plan.ForbiddenScope.Write, source.Identity)
		if source.Neighbor {
			plan.ForbiddenScope.ReadOnlyEvidence = append(plan.ForbiddenScope.ReadOnlyEvidence, source.Identity)
		}
	}
	sort.Slice(plan.Sources, func(i, j int) bool { return plan.Sources[i].Identity < plan.Sources[j].Identity })
	plan.ForbiddenScope.Write = sortedUnique(plan.ForbiddenScope.Write)
	plan.ForbiddenScope.ReadOnlyEvidence = sortedUnique(plan.ForbiddenScope.ReadOnlyEvidence)
	plan.ForbiddenScope.Read = sortedUnique(request.Route.Avoid)
	plan.Unresolved = append(plan.Unresolved, request.Route.Diagnostics...)
	if request.Route.OwnerReviewRequired {
		plan.Unresolved = append(plan.Unresolved, RetrievalDiagnostic{Code: "OWNER_REVIEW_REQUIRED", Status: Partial, Message: "Existing routing requires owner review; retrieval grants no owner or execution permission."})
	}
	for _, owner := range request.Route.Owners {
		if _, ok := admitted[owner]; !ok {
			plan.Unresolved = append(plan.Unresolved, RetrievalDiagnostic{Code: "OWNER_SOURCE_NOT_ADMITTED", Status: Partial, SourceIdentity: owner, Message: "Routing owner has no caller-admitted read source."})
		}
	}
	facets := append([]Facet{}, request.RequiredFacets...)
	for i := range facets {
		facets[i].Required = true
	}
	optional := append([]Facet{}, request.OptionalFacets...)
	for i := range optional {
		optional[i].Required = false
	}
	facets = append(facets, optional...)
	facets = append(facets, request.Route.SeedFacets...)
	if len(facets) == 0 {
		for _, target := range request.Route.Targets {
			for _, p := range target.Paths {
				facets = append(facets, Facet{ID: "route:" + target.SourceIdentity + ":" + p, Kind: "source", QueryKind: QueryExact, SourceIdentity: target.SourceIdentity, Path: p, ClaimType: ImplementationBehavior, Required: true})
			}
			for _, symbol := range target.Symbols {
				facets = append(facets, Facet{ID: "symbol:" + target.SourceIdentity + ":" + symbol, Kind: "symbol", QueryKind: QueryDefinition, SourceIdentity: target.SourceIdentity, Symbol: symbol, ClaimType: ImplementationBehavior, Required: true})
			}
		}
	}
	if len(facets) > 512 {
		return RetrievalPlan{}, fmt.Errorf("%w: facet count", ErrInvalidRequest)
	}
	seen := map[string]Facet{}
	for _, facet := range facets {
		if facet.ID == "" || len(facet.ID) > 512 || len(facet.Text) > 4096 || len(facet.Symbol) > 512 || !knownQuery(facet.QueryKind) || !knownClaim(facet.ClaimType) {
			return RetrievalPlan{}, fmt.Errorf("%w: invalid facet", ErrInvalidRequest)
		}
		if facet.Path != "" && !SafeRelativePath(facet.Path) {
			return RetrievalPlan{}, fmt.Errorf("%w: unsafe facet path", ErrInvalidRequest)
		}
		if facet.Resolver == "" {
			facet.Resolver = resolverFor(facet.QueryKind)
		}
		if prior, exists := seen[facet.ID]; exists {
			if sameFacet(prior, facet) {
				continue
			}
			return RetrievalPlan{}, fmt.Errorf("%w: conflicting facet identity", ErrInvalidRequest)
		}
		seen[facet.ID] = facet
		source, ok := admitted[facet.SourceIdentity]
		if !ok {
			plan.Unresolved = append(plan.Unresolved, RetrievalDiagnostic{Code: "SOURCE_NOT_ADMITTED", Status: Blocked, SourceIdentity: facet.SourceIdentity, FacetID: facet.ID, Message: "Facet source is outside explicit read admission."})
		} else if facet.Path != "" && !PathAdmitted(source, facet.Path) {
			plan.Unresolved = append(plan.Unresolved, RetrievalDiagnostic{Code: "PATH_NOT_ADMITTED", Status: Blocked, SourceIdentity: facet.SourceIdentity, FacetID: facet.ID, RelativePath: facet.Path, Message: "Facet path is outside admitted scope."})
		}
		if facet.Required {
			plan.RequiredFacets = append(plan.RequiredFacets, facet)
		} else {
			plan.OptionalFacets = append(plan.OptionalFacets, facet)
		}
	}
	if len(plan.RequiredFacets)+len(plan.OptionalFacets) == 0 {
		plan.Unresolved = append(plan.Unresolved, RetrievalDiagnostic{Code: "NO_EVIDENCE_REQUIREMENTS", Status: Partial, Message: "Routing supplies no deterministic evidence selectors."})
	}
	sort.Slice(plan.RequiredFacets, func(i, j int) bool { return plan.RequiredFacets[i].ID < plan.RequiredFacets[j].ID })
	sort.Slice(plan.OptionalFacets, func(i, j int) bool { return plan.OptionalFacets[i].ID < plan.OptionalFacets[j].ID })
	for _, policy := range request.TrustedPolicies {
		source, ok := admitted[policy.SourceIdentity]
		if !ok || !SafeRelativePath(policy.RelativePath) || !PathAdmitted(source, policy.RelativePath) || !EqualContentHash(policy.ContentHash, policy.ContentHash) || (policy.Scope != "." && !SafeRelativePath(policy.Scope)) {
			return RetrievalPlan{}, fmt.Errorf("%w: invalid trusted policy registration", ErrInvalidRequest)
		}
		plan.TrustedPolicies = append(plan.TrustedPolicies, policy)
		if PolicyApplies(plan, policy) {
			policyID := "policy:" + policy.SourceIdentity + ":" + policy.RelativePath + ":" + policy.Scope
			if _, exists := seen[policyID]; exists {
				return RetrievalPlan{}, fmt.Errorf("%w: policy facet identity collision", ErrInvalidRequest)
			}
			policyFacet := Facet{ID: policyID, Kind: "policy", QueryKind: QueryExact, Resolver: "exact", SourceIdentity: policy.SourceIdentity, Path: policy.RelativePath, ExpectedHash: policy.ContentHash, ClaimType: ArchitectureRule, Required: true}
			seen[policyID] = policyFacet
			plan.RequiredFacets = append(plan.RequiredFacets, policyFacet)
		}
	}
	sort.Slice(plan.TrustedPolicies, func(i, j int) bool {
		a, b := plan.TrustedPolicies[i], plan.TrustedPolicies[j]
		return a.SourceIdentity+"/"+a.RelativePath+"/"+a.Scope < b.SourceIdentity+"/"+b.RelativePath+"/"+b.Scope
	})
	return plan, nil
}

func normalizeLimits(l Limits) (Limits, error) {
	vals := []*int{&l.MaxFiles, &l.MaxFileBytes, &l.MaxTotalBytes, &l.MaxDepth, &l.MaxResults, &l.MaxSymbolMatches, &l.MaxExpandDepth, &l.MaxDurationMillis}
	defs := []int{1000, 256 << 10, 8 << 20, 24, 200, 100, 8, 10000}
	caps := []int{10000, 1 << 20, 20 << 20, 64, 1000, 1000, 32, 60000}
	for i, p := range vals {
		if *p < 0 || *p > caps[i] {
			return Limits{}, fmt.Errorf("%w: retrieval limit out of bounds", ErrInvalidRequest)
		}
		if *p == 0 {
			*p = defs[i]
		}
	}
	return l, nil
}
func normalizeBudget(b Budget) (Budget, error) {
	if b.MaxSourceBytes < 0 || b.MaxSourceBytes > 20<<20 || b.MaxContextTokens < 0 || b.MaxContextTokens > 262144 || b.ReservedPromptTokens < 0 || b.PerFacetTokens < 0 || b.PerSourceBytes < 0 {
		return Budget{}, fmt.Errorf("%w: budget bounds", ErrInvalidRequest)
	}
	if b.MaxSourceBytes == 0 {
		b.MaxSourceBytes = 8 << 20
	}
	if b.MaxContextTokens == 0 {
		b.MaxContextTokens = 32768
	}
	if b.ReservedPromptTokens >= b.MaxContextTokens {
		return Budget{}, fmt.Errorf("%w: reserved prompt exhausts budget", ErrInvalidRequest)
	}
	return b, nil
}

// PathAdmitted uses component boundaries; exclusions always take precedence.
func PathAdmitted(source SourceAdmission, p string) bool {
	if !SafeRelativePath(p) {
		return false
	}
	p = path.Clean(p)
	match := func(scope string) bool { return scope == "." || p == scope || strings.HasPrefix(p, scope+"/") }
	for _, excluded := range source.ExcludePaths {
		if match(excluded) {
			return false
		}
	}
	for _, scope := range source.ReadPaths {
		if match(scope) {
			return true
		}
	}
	return false
}
func resolverFor(q QueryKind) string {
	switch q {
	case QueryMetadata:
		return "metadata"
	case QueryArchitecture:
		return "architecture"
	case QueryDefinition, QueryDeclarations, QueryImports, QueryReferences, QueryImplementation, QueryCallers:
		return "go"
	case QueryContract:
		return "contract"
	case QueryTests:
		return "tests"
	default:
		return "exact"
	}
}
func knownQuery(q QueryKind) bool {
	switch q {
	case QueryExact, QueryMetadata, QueryArchitecture, QueryDefinition, QueryDeclarations, QueryImports, QueryReferences, QueryImplementation, QueryCallers, QueryContract, QueryTests, QueryErrorMapping, QuerySchema, QueryHistory, QueryDocs:
		return true
	}
	return false
}
func knownClaim(c ClaimType) bool {
	switch c {
	case BusinessOwnership, PublicAPI, ImplementationBehavior, DatabaseSchema, DataSemantics, TestingPolicy, ArchitectureRule, HistoricalDecision:
		return true
	}
	return false
}
func sameFacet(a, b Facet) bool {
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	return string(rawA) == string(rawB)
}
func sortedUnique(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	if len(out) == 0 {
		return out
	}
	n := 1
	for _, v := range out[1:] {
		if v != out[n-1] {
			out[n] = v
			n++
		}
	}
	return out[:n]
}
func hashJSON(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
