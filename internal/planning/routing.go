package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"gopkg.in/yaml.v3"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const (
	maxRoutingEvidence       = 500
	maxRoutingFilesVisited   = 4000
	maxRoutingFileBytes      = 256 << 10
	maxRoutingInventoryBytes = 8 << 20
	maxRouteTargets          = 12
	maxRoutingSymbolMatches  = 20
	maxRepositoryFactsBytes  = 96 << 10
)

var (
	goSymbolPattern = regexp.MustCompile("(?m)^\\s*(?:func|type|var|const)\\s+(?:\\([^)]*\\)\\s*)?([A-Za-z_][A-Za-z0-9_]*)")
	goImportPattern = regexp.MustCompile("(?m)^\\s*\"([A-Za-z0-9_./-]+)\"")
	tsSymbolPattern = regexp.MustCompile("(?m)^\\s*(?:export\\s+)?(?:async\\s+)?(?:function|class|interface|type|const)\\s+([A-Za-z_$][A-Za-z0-9_$]*)")
)

type indexedEvidence struct {
	value   domain.RoutingEvidence
	content []byte
}

type repositoryInventory struct {
	root     string
	evidence []indexedEvidence
	byPath   map[string]indexedEvidence
	coverage *RoutingRepositoryCoverage
}

type canonicalPromptAsset struct {
	ID       string `json:"id"`
	Checksum string `json:"checksum"`
	Content  string `json:"content"`
}

type plannerRepositoryFact struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

func buildRoutingMetadata(requestText string, baseline domain.PlannerOutput, projects []domain.Project, catalog agentcontrol.Catalog) (domain.RoutingResult, domain.ContractPlan, map[string]repositoryInventory, error) {
	if err := agentcontrol.ValidateCatalog(catalog); err != nil {
		return domain.RoutingResult{}, domain.ContractPlan{}, nil, fmt.Errorf("validate canonical routing catalog: %w", err)
	}
	projectByID := make(map[string]domain.Project, len(projects))
	for _, project := range projects {
		projectByID[project.ID] = project
	}
	result := domain.RoutingResult{
		Status: domain.RoutingStatusResolved, Classification: classifyRequest(strings.ToLower(requestText)),
		CatalogDigest: catalog.Digest, Confidence: "high", ScopeMode: "analysis_only",
		EvidenceIDs: []string{}, EvidenceIndex: []domain.RoutingEvidence{},
		Profiles: []domain.ArchitectureProfileResolution{}, Routes: []domain.RoutedTarget{},
		Verification: []domain.RouteVerification{}, Avoid: []string{"unrelated cross-layer changes"},
	}
	inventories := make(map[string]repositoryInventory, len(baseline.Tasks))
	resolvedProfiles := make(map[string]agentcontrol.Profile, len(baseline.Tasks))
	routeOwnerReview := false
	var allEvidence []indexedEvidence
	for _, task := range baseline.Tasks {
		project, ok := projectByID[task.ProjectID]
		if !ok || project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
			return domain.RoutingResult{}, domain.ContractPlan{}, nil,
				fmt.Errorf("planner routing requires checkout for project %q: %w", task.ProjectID, domain.ErrInvalidStatus)
		}
		inventory, err := indexRepository(*project.LocalPath, task.ProjectID)
		if err != nil {
			return domain.RoutingResult{}, domain.ContractPlan{}, nil, err
		}
		inventories[task.ProjectID] = inventory
		allEvidence = append(allEvidence, inventory.evidence...)
		resolution, profile, err := resolveArchitectureProfile(requestText, task.ProjectID, inventory, catalog)
		if err != nil {
			return domain.RoutingResult{}, domain.ContractPlan{}, nil, err
		}
		result.Profiles = append(result.Profiles, resolution)
		if resolution.Status != domain.ProfileResolutionResolved {
			result.Status = domain.RoutingStatusPartial
			result.Confidence = "low"
			result.UnresolvedReason = joinReason(result.UnresolvedReason, resolution.Reason)
			continue
		}
		resolvedProfiles[task.ProjectID] = profile
		result.EvidenceIDs = append(result.EvidenceIDs, resolution.EvidenceIDs...)
		routes, classification, confidence, candidates, needsOwnerReview := selectEvidenceBackedRoutes(
			requestText, task.ProjectID, inventory, profile, resolution.EvidenceIDs,
		)
		result.RouteCandidates = append(result.RouteCandidates, candidates...)
		if needsOwnerReview {
			routeOwnerReview = true
			result.Status = domain.RoutingStatusPartial
			result.Confidence = "low"
			result.UnresolvedReason = joinReason(result.UnresolvedReason,
				"one or more route instructions are conditional, conflicting, or lack required source evidence")
		}
		if len(routes) == 0 {
			if !needsOwnerReview {
				result.Status = domain.RoutingStatusPartial
				result.Confidence = "low"
				reason := fmt.Sprintf("profile %s resolved for project %s, but no requested route has compatible repository evidence", profile.ID, task.ProjectID)
				result.UnresolvedReason = joinReason(result.UnresolvedReason, reason)
			}
			continue
		}
		if classification != "maintenance" {
			result.Classification = classification
		}
		if confidence == "low" {
			result.Confidence = "low"
		} else if confidence == "medium" && result.Confidence == "high" {
			result.Confidence = "medium"
		}
		result.Routes = append(result.Routes, routes...)
		for _, route := range routes {
			result.EvidenceIDs = append(result.EvidenceIDs, route.EvidenceIDs...)
			profileRoute := findProfileRoute(profile, route.RouteID)
			if profileRoute != nil && len(profileRoute.Contracts) > 0 {
				result.ContractsToInspect = append(result.ContractsToInspect, domain.ContractInspection{
					Kind: strings.Join(profileRoute.Contracts, ", "), ProjectID: task.ProjectID,
					EvidenceIDs: append([]string(nil), route.EvidenceIDs...),
					Routes:      []domain.RouteReference{{ProjectID: task.ProjectID, RouteID: route.RouteID}},
				})
			}
			verification, ids := routeVerification(task.ProjectID, profile, route, inventory)
			result.Verification = append(result.Verification, verification)
			result.EvidenceIDs = append(result.EvidenceIDs, ids...)
		}
	}
	if len(result.Routes) == 0 {
		if routeOwnerReview {
			result.Status = domain.RoutingStatusPartial
		} else {
			result.Status = domain.RoutingStatusUnresolved
		}
	} else if anyProfileUnresolved(result.Profiles) {
		result.Status = domain.RoutingStatusPartial
	}
	result.Profiles = sortProfileResolutions(result.Profiles)
	sort.Slice(result.RouteCandidates, func(i, j int) bool {
		if result.RouteCandidates[i].ProjectID != result.RouteCandidates[j].ProjectID {
			return result.RouteCandidates[i].ProjectID < result.RouteCandidates[j].ProjectID
		}
		return result.RouteCandidates[i].RouteID < result.RouteCandidates[j].RouteID
	})
	result.Routes = sortRoutedTargets(result.Routes)
	result.Verification = sortVerifications(result.Verification)
	result.ContractsToInspect = sortContractInspections(result.ContractsToInspect)
	result.SharedBoundaryCandidates = sharedBoundaryCandidates(result.Routes, resolvedProfiles)
	result.OwnerReviewRequired = result.Status != domain.RoutingStatusResolved || result.Confidence == "low"
	if result.OwnerReviewRequired {
		result.Avoid = uniqueSorted(append(result.Avoid, "owner review is required before treating route analysis as implementation scope"))
	}
	if len(result.Routes) > 0 {
		primary := primaryRoute(result.Routes, result.Classification)
		result.PrimaryRoute = &primary
	}
	contractPlan, err := buildContractPlan(result.Status, result.Routes, result.SharedBoundaryCandidates, inventories, resolvedProfiles, requestText)
	if err != nil {
		return domain.RoutingResult{}, domain.ContractPlan{}, nil, err
	}
	recordRoutingRequirements(result, contractPlan, inventories)
	result.EvidenceIndex = buildBoundedEvidenceIndex(allEvidence, result, contractPlan)
	available := make(map[string]struct{}, len(result.EvidenceIndex))
	for _, evidence := range result.EvidenceIndex {
		available[evidence.ID] = struct{}{}
	}
	result.EvidenceIDs = retainEvidenceIDs(result.EvidenceIDs, available)
	for index := range result.Profiles {
		result.Profiles[index].EvidenceIDs = retainEvidenceIDs(result.Profiles[index].EvidenceIDs, available)
	}
	for index := range result.Routes {
		result.Routes[index].EvidenceIDs = retainEvidenceIDs(result.Routes[index].EvidenceIDs, available)
	}
	for index := range result.RouteCandidates {
		result.RouteCandidates[index].ArchitectureEvidence = retainEvidenceIDs(result.RouteCandidates[index].ArchitectureEvidence, available)
		result.RouteCandidates[index].SourceEvidence = retainEvidenceIDs(result.RouteCandidates[index].SourceEvidence, available)
	}
	for index := range result.Verification {
		result.Verification[index].EvidenceIDs = retainEvidenceIDs(result.Verification[index].EvidenceIDs, available)
	}
	result.EvidenceIDs = uniqueSorted(result.EvidenceIDs)
	return result, contractPlan, inventories, nil
}

func resolveArchitectureProfile(requestText, projectID string, inventory repositoryInventory, catalog agentcontrol.Catalog) (domain.ArchitectureProfileResolution, agentcontrol.Profile, error) {
	var goMod, packageJSON, appSource *indexedEvidence
	goSources := 0
	for index := range inventory.evidence {
		evidence := &inventory.evidence[index]
		switch evidence.value.Path {
		case "go.mod":
			goMod = evidence
		case "package.json":
			packageJSON = evidence
		}
		if strings.HasPrefix(evidence.value.Path, "src/app/") && sourcePath(evidence.value.Path) {
			appSource = evidence
		}
		if strings.HasSuffix(evidence.value.Path, ".go") && evidence.value.Kind == "source" {
			goSources++
		}
	}
	profileID := ""
	profileReason := "repository evidence does not verify a supported go.canonical or nextjs.common profile"
	var profileEvidence []string
	if goMod != nil && goSources > 0 {
		goProfile := catalog.Profiles["go.canonical"]
		shapeMatches, alternateEvidence := matchesGoRepositoryShape(inventory, goProfile)
		if !shapeMatches {
			profileReason = "Go source was found, but required canonical layered source was not found"
			if inventory.coverage.UnscannedRemainder || inventory.coverage.ReadErrors > 0 {
				profileReason = "Required canonical layered source NOT_VERIFIED_BY_ACQUISITION_LIMIT_OR_READ_ERROR"
			}
		}
		if shapeMatches {
			profileEvidence = append(profileEvidence, alternateEvidence...)
			profileID = "go.canonical"
			profileEvidence = append(profileEvidence, goMod.value.ID)
			for _, evidence := range inventory.evidence {
				if evidence.value.Kind == "source" && strings.HasSuffix(evidence.value.Path, ".go") &&
					isRequiredShapePath(evidence.value.Path, goProfile.RepoShape) {
					profileEvidence = append(profileEvidence, evidence.value.ID)
				}
			}
		}
	} else if packageJSON != nil && appSource != nil && bytesContainNextDependency(packageJSON.content) &&
		matchesRepositoryShape(inventory, catalog.Profiles["nextjs.common"].RepoShape) {
		profileID = "nextjs.common"
		profileEvidence = append(profileEvidence, packageJSON.value.ID, appSource.value.ID)
	}
	if profileID == "" {
		return domain.ArchitectureProfileResolution{
			ProjectID: projectID, Status: domain.ProfileResolutionUnresolved,
			Reason:      profileReason,
			EvidenceIDs: []string{},
		}, agentcontrol.Profile{}, nil
	}
	profile, ok := catalog.Profiles[profileID]
	if !ok {
		return domain.ArchitectureProfileResolution{}, agentcontrol.Profile{}, fmt.Errorf("canonical profile %q is missing from Agent Control Plane catalog", profileID)
	}
	profileFingerprint, err := agentcontrol.ProfileFingerprint(catalog, profileID)
	if err != nil {
		return domain.ArchitectureProfileResolution{}, agentcontrol.Profile{}, err
	}
	resolution := domain.ArchitectureProfileResolution{
		ProjectID: projectID, Status: domain.ProfileResolutionResolved, ProfileID: profileID,
		ProfileFingerprint: profileFingerprint,
		EvidenceIDs:        uniqueSorted(profileEvidence), Reason: "verified by repository stack, required structural evidence, and source files",
	}
	if profileID == "nextjs.common" {
		student := hasPathPrefix(inventory, "src/app/student/", "src/app/(student)/")
		admin := hasPathPrefix(inventory, "src/app/admin/", "src/app/(admin)/")
		switch {
		case student && !admin:
			resolution.Variant = "student"
		case admin && !student:
			resolution.Variant = "admin"
		case student && admin:
			text := strings.ToLower(requestText)
			if strings.Contains(text, "student") || strings.Contains(text, "учен") {
				resolution.Variant = "student"
			} else if strings.Contains(text, "admin") || strings.Contains(text, "админ") {
				resolution.Variant = "admin"
			} else {
				resolution.Reason = "verified Next.js application; both student and admin variants exist"
			}
		default:
			resolution.Reason = "verified Next.js application; route variant is not established by repository paths"
		}
	}
	return resolution, profile, nil
}

func matchesRepositoryShape(inventory repositoryInventory, shape agentcontrol.RepositoryShape) bool {
	if len(shape.RequiredPaths) == 0 {
		return false
	}
	for _, required := range shape.RequiredPaths {
		required = strings.TrimSuffix(required, "/")
		found := false
		for _, evidence := range inventory.evidence {
			if evidence.value.Path == required || strings.HasPrefix(evidence.value.Path, required+"/") {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// An already declared profile target may satisfy its canonical usecase root only
// when structured repository metadata and parsed implementation corroborate it.
func matchesGoRepositoryShape(inventory repositoryInventory, profile agentcontrol.Profile) (bool, []string) {
	if matchesRepositoryShape(inventory, profile.RepoShape) {
		return true, nil
	}
	alternate := "internal/application/usecase"
	targetDeclared := false
	for _, route := range profile.Routes {
		if route.ID == "backend.usecase" {
			targetDeclared = evidenceMatchesRoute(alternate+"/source.go", route.Targets)
		}
	}
	if !targetDeclared {
		return false, nil
	}
	var metadata, source []string
	for _, item := range inventory.evidence {
		if item.value.Kind == "architecture_metadata" || item.value.Kind == "service_metadata" {
			var node yaml.Node
			if yaml.Unmarshal(item.content, &node) == nil && declaresUsecaseDirectory(&node, alternate) {
				metadata = append(metadata, item.value.ID)
			}
		}
		if item.value.Kind == "source" && strings.HasPrefix(item.value.Path, alternate+"/") && strings.HasSuffix(item.value.Path, ".go") && !strings.HasSuffix(item.value.Path, "_test.go") {
			if file, err := parser.ParseFile(token.NewFileSet(), "usecase.go", item.content, 0); err == nil && hasGoImplementationDeclaration(file) {
				source = append(source, item.value.ID)
			}
		}
	}
	if len(metadata) == 0 || len(source) == 0 {
		return false, nil
	}
	shape := profile.RepoShape
	shape.RequiredPaths = append([]string(nil), shape.RequiredPaths...)
	replaced := false
	for i, required := range shape.RequiredPaths {
		if strings.TrimSuffix(required, "/") == "internal/usecase" {
			shape.RequiredPaths[i] = alternate + "/"
			replaced = true
		}
	}
	return replaced && matchesRepositoryShape(inventory, shape), uniqueSorted(append(metadata, source...))
}

func hasGoImplementationDeclaration(file *ast.File) bool {
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Body != nil {
			return true
		}
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.TYPE {
			return true
		}
	}
	return false
}

func declaresUsecaseDirectory(node *yaml.Node, directory string) bool {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return declaresUsecaseDirectory(node.Content[0], directory)
	}
	if node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != "layers" || node.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		layers := node.Content[i+1]
		for j := 0; j+1 < len(layers.Content); j += 2 {
			key, value := layers.Content[j], layers.Content[j+1]
			if (key.Value == "usecase" || key.Value == "use_cases") && value.Kind == yaml.ScalarNode && strings.TrimSuffix(value.Value, "/") == directory {
				return true
			}
		}
	}
	return false
}

func isRequiredShapePath(path string, shape agentcontrol.RepositoryShape) bool {
	for _, required := range shape.RequiredPaths {
		required = strings.TrimSuffix(required, "/")
		if path == required || strings.HasPrefix(path, required+"/") {
			return true
		}
	}
	return false
}

func selectEvidenceBackedRoutes(
	requestText, projectID string, inventory repositoryInventory, profile agentcontrol.Profile, architectureEvidence []string,
) ([]domain.RoutedTarget, string, string, []domain.RoutingCandidateEvidence, bool) {
	classification := "maintenance"
	var candidates []evidenceBackedRouteCandidate
	for _, route := range profile.Routes {
		candidate := evidenceBackedRouteCandidate{route: route}
		for _, evidence := range inventory.evidence {
			if evidenceMatchesRoute(evidence.value.Path, route.Targets) && verifiedOutboundRoot(route.ID, evidence, inventory) {
				candidate.files = append(candidate.files, evidence)
			}
		}
		if len(candidate.files) > 0 {
			candidate.localScore = localRouteScore(route, inventory)
		}
		candidate.diagnostic = analyzeRouteCandidateWithCoverage(route.ID, requestText, projectID, architectureEvidence, candidate.files, inventory.coverage)
		if candidate.diagnostic.Polarity == domain.RoutePolarityNeutral && (routeActionPattern.MatchString(requestText) || requestedRoutingActionPattern.MatchString(requestText)) && namedSourceSyntax(requestText, candidate.files) {
			candidate.diagnostic.Polarity = domain.RoutePolarityPositive
			candidate.diagnostic.CandidateSignal = "named source syntax anchors requested inspection or work; no type-aware relationship claimed"
		}
		protocolSyntax := verifiedProtocolTask(requestText, route.ID, candidate.files, inventory)
		if candidate.diagnostic.Polarity == domain.RoutePolarityNeutral && protocolSyntax {
			candidate.diagnostic.Polarity = domain.RoutePolarityPositive
			candidate.diagnostic.CandidateSignal = "concrete protocol literal and admitted implementation call syntax anchor analysis; no execution or type-aware proof"
		}
		if route.ID == "backend.transport.message" && strings.EqualFold(candidate.diagnostic.MatchedPhrase, "consumer") && !protocolSyntax && !regexp.MustCompile(`(?i)\b(?:nats|queue|message|messaging|jetstream|broker)\b`).MatchString(requestText) {
			candidate.diagnostic.Polarity = domain.RoutePolarityNeutral
			candidate.diagnostic.CandidateSignal = "consumer without an incoming message protocol does not establish queue transport ownership"
		}
		candidates = append(candidates, candidate)
	}
	var selected []evidenceBackedRouteCandidate
	ownerReview := false
	confidence := "high"
	maxLocal := 0
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.diagnostic.Polarity == domain.RoutePolarityConditional ||
			candidate.diagnostic.Polarity == domain.RoutePolarityConflicting {
			candidate.diagnostic.Decision = domain.RouteDecisionOwnerReview
			ownerReview = true
		}
		implicitFragment := strings.Contains(candidate.diagnostic.CandidateSignal, "bounded task-fragment form")
		if candidate.diagnostic.Polarity == domain.RoutePolarityPositive && len(candidate.files) == 0 && !implicitFragment {
			candidate.diagnostic.Decision = domain.RouteDecisionOwnerReview
			candidate.diagnostic.CandidateSignal = joinReason(candidate.diagnostic.CandidateSignal,
				"positive task action has no compatible repository source evidence")
			ownerReview = true
		}
		if candidate.diagnostic.Polarity == domain.RoutePolarityPositive && len(candidate.files) > 0 && candidate.localScore > maxLocal {
			maxLocal = candidate.localScore
		}
	}
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.diagnostic.Polarity == domain.RoutePolarityNegative {
			candidate.diagnostic.Decision = domain.RouteDecisionExcluded
			continue
		}
		if candidate.diagnostic.Polarity != domain.RoutePolarityPositive || len(candidate.files) == 0 {
			if candidate.diagnostic.Decision == "" {
				candidate.diagnostic.Decision = domain.RouteDecisionNotSelected
			}
			continue
		}
		if maxLocal > 0 && candidate.localScore != maxLocal && !requestedRoutingActionPattern.MatchString(requestText) && !strings.Contains(candidate.diagnostic.CandidateSignal, "named source syntax") {
			candidate.diagnostic.Decision = domain.RouteDecisionNotSelected
			candidate.diagnostic.CandidateSignal = joinReason(candidate.diagnostic.CandidateSignal,
				"local ownership evidence selected a more specific positive route")
			continue
		}
		candidate.diagnostic.Decision = domain.RouteDecisionSelected
		selected = append(selected, *candidate)
	}
	if len(selected) == 0 && !ownerReview {
		confidence = "low"
	}
	if maxLocal > 0 {
		confidence = "medium"
	}
	if len(selected) > 0 {
		classification = classifySelectedRoutes(selected, requestText)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].route.ID < selected[j].route.ID })
	result := make([]domain.RoutedTarget, 0, len(selected))
	for _, candidate := range selected {
		files := append([]indexedEvidence(nil), candidate.files...)
		sort.Slice(files, func(i, j int) bool {
			leftTest := files[i].value.Kind == "test"
			rightTest := files[j].value.Kind == "test"
			if leftTest != rightTest {
				return !leftTest
			}
			return files[i].value.Path < files[j].value.Path
		})
		recordRoutingTargetCoverage(inventory.coverage, candidate.route.ID, files)
		if len(files) > maxRouteTargets {
			files = files[:maxRouteTargets]
		}
		target := domain.RoutedTarget{RouteReference: domain.RouteReference{ProjectID: projectID, RouteID: candidate.route.ID}}
		for _, evidence := range files {
			target.Paths = append(target.Paths, evidence.value.Path)
			target.EvidenceIDs = append(target.EvidenceIDs, evidence.value.ID)
			if evidence.value.Symbol != "" {
				target.Symbols = append(target.Symbols, strings.Split(evidence.value.Symbol, ", ")...)
			}
		}
		target.Paths, target.Symbols, target.EvidenceIDs = uniqueSorted(target.Paths), uniqueSorted(target.Symbols), uniqueSorted(target.EvidenceIDs)
		result = append(result, target)
	}
	diagnostics := make([]domain.RoutingCandidateEvidence, 0, len(candidates))
	for _, candidate := range candidates {
		diagnostics = append(diagnostics, candidate.diagnostic)
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].RouteID < diagnostics[j].RouteID })
	return result, classification, confidence, diagnostics, ownerReview
}

func indexRepository(root, projectID string) (repositoryInventory, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return repositoryInventory{}, fmt.Errorf("resolve planner evidence root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return repositoryInventory{}, fmt.Errorf("resolve planner evidence checkout %q: %w", projectID, err)
	}
	rootInfo, err := os.Stat(canonical)
	if err != nil {
		return repositoryInventory{}, fmt.Errorf("inspect planner evidence root for project %q: %w", projectID, err)
	}
	if !rootInfo.IsDir() {
		return repositoryInventory{}, fmt.Errorf("planner evidence root for project %q is not a directory: %w", projectID, domain.ErrInvalidStatus)
	}
	inventory := repositoryInventory{root: canonical, byPath: map[string]indexedEvidence{}, coverage: newRoutingRepositoryCoverage(projectID)}
	visited, totalBytes := 0, 0
	err = walkRoutingInventory(canonical, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			inventory.coverage.ReadErrors++
			inventory.coverage.UnscannedRemainder = true
			inventory.coverage.RemainderCountKnown = false
			relative, _ := filepath.Rel(canonical, current)
			inventory.coverage.omit("inventory", filepath.ToSlash(relative), "walk_error", 1)
			return nil
		}
		relative, relErr := filepath.Rel(canonical, current)
		if relErr != nil {
			inventory.coverage.ReadErrors++
			inventory.coverage.omit("inventory", "", "relative_path_error", 1)
			return nil
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative != "." && excludedEvidenceDirectory(entry.Name()) {
				inventory.coverage.ExcludedDirectories++
				inventory.coverage.omit("inventory", relative, "excluded_by_policy_directory", 1)
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			inventory.coverage.ExcludedSymlinks++
			inventory.coverage.omit("inventory", relative, "excluded_symlink", 1)
			return nil
		}
		if !entry.Type().IsRegular() {
			inventory.coverage.ExcludedNonRegular++
			inventory.coverage.omit("inventory", relative, "excluded_nonregular", 1)
			return nil
		}
		if excludedEvidenceFile(relative) {
			inventory.coverage.ExcludedFiles++
			inventory.coverage.omit("inventory", relative, "excluded_by_policy_file", 1)
			return nil
		}
		visited++
		inventory.coverage.VisitedFiles = visited
		if visited > maxRoutingFilesVisited || len(inventory.evidence) >= maxRoutingEvidence || totalBytes >= maxRoutingInventoryBytes {
			reason := "inventory_bytes_limit"
			if visited > maxRoutingFilesVisited {
				reason = "visited_files_limit"
			} else if len(inventory.evidence) >= maxRoutingEvidence {
				reason = "inventory_evidence_limit"
			}
			inventory.coverage.TerminationReason = reason
			inventory.coverage.UnscannedRemainder = true
			inventory.coverage.RemainderCountKnown = false
			inventory.coverage.omit("inventory", relative, reason, 1)
			return filepath.SkipAll
		}
		kind := evidenceKind(relative)
		if kind == "" {
			inventory.coverage.UnsupportedFiles++
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			inventory.coverage.ReadErrors++
			inventory.coverage.omit("inventory", relative, "stat_error", 1)
			return nil
		}
		if info.Size() < 0 || info.Size() > maxRoutingFileBytes || totalBytes+int(info.Size()) > maxRoutingInventoryBytes {
			reason := "inventory_bytes_omission"
			if info.Size() < 0 {
				reason = "invalid_file_size"
			} else if info.Size() > maxRoutingFileBytes {
				reason = "file_bytes_limit"
				inventory.coverage.SkippedLargeFiles++
			}
			inventory.coverage.omit("inventory", relative, reason, 1)
			return nil
		}
		content, readErr := os.ReadFile(current)
		if readErr != nil {
			inventory.coverage.ReadErrors++
			inventory.coverage.omit("inventory", relative, "read_error", 1)
			return nil
		}
		totalBytes += len(content)
		inventory.coverage.IndexedFiles++
		inventory.coverage.IndexedBytes = totalBytes
		recordRoutingExtractionCoverage(inventory.coverage, relative, content)
		evidence := makeEvidence(projectID, relative, kind, content)
		indexed := indexedEvidence{value: evidence, content: content}
		inventory.evidence = append(inventory.evidence, indexed)
		inventory.byPath[relative] = indexed
		return nil
	})
	if err != nil {
		return repositoryInventory{}, fmt.Errorf("index planner evidence for project %q: %w", projectID, err)
	}
	sort.Slice(inventory.evidence, func(i, j int) bool { return inventory.evidence[i].value.Path < inventory.evidence[j].value.Path })
	return inventory, nil
}

func makeEvidence(projectID, relative, kind string, content []byte) domain.RoutingEvidence {
	checksum := sha256.Sum256(content)
	identity := sha256.Sum256([]byte(projectID + "\x00" + relative + "\x00" + kind))
	summary, symbols := kind, extractSymbols(relative, content)
	if kind == "source" || kind == "test" {
		imports := extractImports(relative, content)
		if len(symbols) > 0 {
			summary += "; symbols: " + strings.Join(symbols, ", ")
		}
		if len(imports) > 0 {
			summary += "; imports: " + strings.Join(imports, ", ")
		}
		lower := strings.ToLower(string(content))
		if strings.Contains(lower, "interface {") || strings.Contains(lower, "interface{") ||
			strings.Contains(lower, "interface ") {
			summary += "; interface declaration"
		}
	}
	return domain.RoutingEvidence{
		ID: "ev-" + hex.EncodeToString(identity[:8]), ProjectID: projectID, Kind: kind, Path: relative,
		Summary: summary, Checksum: "sha256:" + hex.EncodeToString(checksum[:]), Symbol: strings.Join(symbols, ", "),
		BoundaryKinds: detectedBoundaryKinds(relative, kind, content, symbols),
	}
}

func evidenceKind(relative string) string {
	base := path.Base(relative)
	switch {
	case base == "AGENTS.md":
		return "repository_instructions"
	case relative == ".ai/service.yaml":
		return "service_metadata"
	case relative == ".ai/architecture.yaml" || strings.HasPrefix(relative, ".ai/architecture/") && strings.HasSuffix(relative, ".mmd"):
		return "architecture_metadata"
	case strings.HasPrefix(relative, ".ai/contracts/"):
		return "contract"
	case relative == ".ai/commands.yaml":
		return "commands"
	case relative == ".ai/testing/test-manifest.yaml":
		return "test_manifest"
	case relative == "go.mod" || relative == "package.json":
		return "stack_manifest"
	case strings.HasSuffix(relative, "_test.go") || strings.HasSuffix(relative, ".test.ts") || strings.HasSuffix(relative, ".test.tsx") ||
		strings.HasSuffix(relative, ".spec.ts") || strings.HasSuffix(relative, ".spec.tsx") || strings.HasSuffix(relative, ".test.js") || strings.HasSuffix(relative, ".spec.js"):
		return "test"
	case sourcePath(relative):
		return "source"
	default:
		return ""
	}
}

func sourcePath(relative string) bool {
	for _, extension := range []string{".go", ".ts", ".tsx", ".js", ".jsx", ".sql", ".proto"} {
		if strings.HasSuffix(relative, extension) {
			return true
		}
	}
	return false
}

func excludedEvidenceDirectory(name string) bool {
	switch name {
	case ".git", ".agents", ".cache", ".next", ".turbo", ".vercel", "node_modules", "vendor", "dist", "build", "coverage", "out":
		return true
	default:
		return false
	}
}

func excludedEvidenceFile(relative string) bool {
	base := strings.ToLower(path.Base(relative))
	if strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") ||
		strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".jks") {
		return true
	}
	return strings.HasPrefix(relative, ".ai/agents/") || relative == ".ai/rules/common.md" || strings.HasPrefix(relative, ".ai/workflows/")
}

func evidenceMatchesRoute(relative string, patterns []string) bool {
	for _, pattern := range patterns {
		if globPathMatch(pattern, relative) {
			return true
		}
	}
	return false
}

func globPathMatch(pattern, value string) bool {
	patternParts, valueParts := strings.Split(pattern, "/"), strings.Split(value, "/")
	var match func(int, int) bool
	match = func(pi, vi int) bool {
		if pi == len(patternParts) {
			return vi == len(valueParts)
		}
		if patternParts[pi] == "**" {
			if pi == len(patternParts)-1 {
				return true
			}
			for index := vi; index <= len(valueParts); index++ {
				if match(pi+1, index) {
					return true
				}
			}
			return false
		}
		if vi >= len(valueParts) {
			return false
		}
		ok, err := path.Match(patternParts[pi], valueParts[vi])
		return err == nil && ok && match(pi+1, vi+1)
	}
	return match(0, 0)
}

type routePhraseMatch struct {
	phrase string
	start  int
	end    int
}

type routingClause struct {
	text  string
	start int
}

type evidenceBackedRouteCandidate struct {
	route      agentcontrol.ProfileRoute
	files      []indexedEvidence
	localScore int
	diagnostic domain.RoutingCandidateEvidence
}

var routingClauseSeparator = regexp.MustCompile(`(?i)\b(?:but|however|while)\b|[,!?;\n]+|\.\s+|\.$`)

var routeActionPattern = regexp.MustCompile(`(?i)\b(?:inspect(?:s|ed|ing)?|implement(?:s|ed|ing)?|add(?:s|ed|ing)?|chang(?:e|es|ed|ing)|modif(?:y|ies|ied|ying)|updat(?:e|es|ed|ing)|fix(?:es|ed|ing)?|creat(?:e|es|ed|ing)|build(?:s|ing)?|writ(?:e|es|ten|ing)|introduc(?:e|es|ed|ing)|extend(?:s|ed|ing)?|refactor(?:s|ed|ing)?|replac(?:e|es|ed|ing)|remov(?:e|es|ed|ing)|support(?:s|ed|ing)?|реализ\p{L}*|добав\p{L}*|измен\p{L}*|обнов\p{L}*|исправ\p{L}*|созда\p{L}*|поддерж\p{L}*)\b`)

var requestedRoutingActionPattern = regexp.MustCompile(`(?i)(?:^|[.!?;,]\s*)(?:only\s+)?(?:inspect|trace|explain|investigate|audit|review|select|locate|verify|implement|add|change|modify|update|fix|create|build|write|introduce|extend|refactor|replace|remove|support)\b`)

var routingAnalysisPattern = regexp.MustCompile(`(?i)^\s*(?:trace|explain|investigate|audit|review|select|locate|verify)\b`)
var routingDependentFragmentPattern = regexp.MustCompile(`(?i)^\s*(?:including|such as|for example|with|without)\b`)

var compositionActionPattern = regexp.MustCompile(`(?i)\b(?:register(?:s|ed|ing)?|wire(?:s|d|ing)?|connect(?:s|ed|ing)?|bootstrap(?:s|ped|ping)?|bind(?:s|bound|ing)?|configur(?:e|es|ed|ing)|add(?:s|ed|ing)?|change(?:s|d|ing)?|modify(?:s|ied|ying)?|update(?:s|d|ing)?)\b`)

var compositionOwnershipActionPattern = regexp.MustCompile(`(?i)\b(?:register(?:s|ed|ing)?|wire(?:s|d|ing)?|connect(?:s|ed|ing)?|bootstrap(?:s|ped|ping)?|bind(?:s|bound|ing)?|configur(?:e|es|ed|ing))\b`)

var routingConditionalPattern = regexp.MustCompile(`(?i)\b(?:only\s+if\s+necessary|unless\s+required|only\s+when\s+(?:necessary|required)|if\s+necessary)\b`)

var routingExplanatoryPattern = regexp.MustCompile(`(?i)\b(?:currently|already|existing|the\s+existing|current)\b`)

var routeTaskFragmentVerbPattern = regexp.MustCompile(`(?i)\b(?:is|are|was|were|has|have|had|currently|already|wires|register|registers|wire|wired|wiring|connect|connects|bootstrap|bind|configure|implements|changes)\b`)

var routeIssuePattern = regexp.MustCompile(`(?i)\b(?:is|are|was|were)\s+(?:slow|wrong|broken|failing|missing|incorrect)\b|\b(?:fails?|failure|error|timeout|times\s+out|is\s+missing)\b`)

var routeNegativePrefixPattern = regexp.MustCompile(`(?i)\b(?:do\s+not|don't|does\s+not|doesn't|must\s+not|should\s+not|without|exclude|excluded|leave|no\s+changes\s+to)\b`)

var routeNegativeSuffixPattern = regexp.MustCompile(`(?i)\b(?:out\s+of\s+scope|out-of-scope|unchanged|excluded|not\s+in\s+scope)\b`)

var routeSignalKeywords = map[string][]string{
	"backend.domain":                     {"invariant", "domain invariant", "domain", "business rule", "repository interface", "repository port", "правил", "инвариант", "модель предмет"},
	"backend.usecase":                    {"business process", "application workflow", "usecase", "use case", "application", "workflow", "процесс", "сценар", "бизнес-логик", "бизнес логик"},
	"backend.transport.http":             {"http", "endpoint", "handler", "rest api", "http boundary", "эндпоинт", "обработчик"},
	"backend.transport.message":          {"consumer", "message handler", "queue consumer", "потребител сообщ", "обработчик сообщ"},
	"backend.infrastructure.persistence": {"sql", "postgres", "postgresql", "database", "query", "persistence", "repository adapter", "storage adapter", "repository", "write invariant", "migration", "storage", "баз данных", "запрос", "миграц", "хранилищ"},
	"backend.infrastructure.client":      {"external client", "http client", "rpc client", "external api", "внешн клиент", "внешн api"},
	"backend.infrastructure.messaging":   {"publisher", "message publish", "event publish", "messaging", "broker", "публикатор", "публикац событ", "брокер"},
	"backend.migration":                  {"migration", "schema change", "database schema", "миграц", "схем базы"},
	"backend.composition":                {"composition", "dependency injection", "di", "wiring", "registration", "route registration", "constructor wiring", "consumer registration", "bootstrap", "сборк зависим"},
	"frontend.route":                     {"route", "page", "layout", "routing", "страниц", "маршрут"},
	"frontend.module.ui":                 {"ui", "component", "button", "form", "visual", "interface", "компонент", "интерфейс", "экран"},
	"frontend.module.usecase":            {"usecase", "use case", "frontend logic", "user flow", "сценар", "логик интерфейс"},
	"frontend.module.api":                {"api adapter", "api client", "fetch", "request mapping", "адаптер api", "api-запрос"},
	"frontend.module.model":              {"frontend model", "schema", "zod", "validation model", "схем данных", "модель frontend"},
	"frontend.shared.bff":                {"bff", "backend for frontend", "proxy route", "server action", "прокси"},
	"frontend.shared.ui":                 {"shared ui", "shared component", "design system", "общий компонент", "общий ui"},
	"frontend.i18n":                      {"translation", "localization", "i18n", "translation key", "перевод", "локализац"},
	"frontend.app-runtime":               {"provider", "app runtime", "next config", "runtime config", "провайдер", "конфигурац приложения"},
}

func analyzeRouteCandidate(routeID, requestText, projectID string, architectureEvidence []string, files []indexedEvidence) domain.RoutingCandidateEvidence {
	return analyzeRouteCandidateWithCoverage(routeID, requestText, projectID, architectureEvidence, files, nil)
}

func analyzeRouteCandidateWithCoverage(routeID, requestText, projectID string, architectureEvidence []string, files []indexedEvidence, coverage *RoutingRepositoryCoverage) domain.RoutingCandidateEvidence {
	result := domain.RoutingCandidateEvidence{
		RouteReference: domain.RouteReference{ProjectID: projectID, RouteID: routeID},
		Polarity:       domain.RoutePolarityNeutral, Decision: domain.RouteDecisionNotSelected,
		ArchitectureEvidence: append([]string(nil), architectureEvidence...),
	}
	for _, file := range files {
		result.SourceEvidence = append(result.SourceEvidence, file.value.ID)
	}
	result.SourceEvidence = uniqueSorted(result.SourceEvidence)
	if len(result.SourceEvidence) > maxRouteTargets {
		if coverage != nil {
			coverage.omit("candidate_evidence", "", "candidate_evidence_limit", len(result.SourceEvidence)-maxRouteTargets)
			coverage.Omissions[len(coverage.Omissions)-1].RouteID = routeID
		}
		result.SourceEvidence = result.SourceEvidence[:maxRouteTargets]
	}

	type signal struct {
		polarity domain.RoutePolarity
		phrase   string
		span     string
		detail   string
	}
	var signals []signal
	for _, clause := range splitRoutingClauses(requestText) {
		matches := routePhraseMatches(routeID, clause.text)
		if len(matches) == 0 {
			continue
		}
		if coverage != nil && len([]rune(strings.Join(strings.Fields(clause.text), " "))) > 180 {
			coverage.omit("task_span", "", "task_clause_display_limit", 1)
			coverage.Omissions[len(coverage.Omissions)-1].RouteID = routeID
		}
		lower := strings.ToLower(clause.text)
		conditional := routingConditionalPattern.MatchString(lower)
		explanatory := routingExplanatoryPattern.MatchString(lower)
		action, context := routeActionPattern.FindStringIndex(lower), routingExplanatoryPattern.FindStringIndex(lower)
		if action == nil {
			action = routingAnalysisPattern.FindStringIndex(lower)
		}
		if action != nil && (context == nil || action[0] < context[0]) {
			explanatory = false
		}
		if (regexp.MustCompile(`(?i)^\s*explain\s+that\b`).MatchString(lower) || regexp.MustCompile(`(?i)^\s*(?:trace|explain|investigate|audit|review|select|locate|verify)\s+(?:the\s+)?(?:current|existing|already)\b`).MatchString(lower)) && routingExplanatoryPattern.MatchString(lower) {
			explanatory = true
		}
		positiveAction := routeActionPattern.MatchString(lower) || routingAnalysisPattern.MatchString(lower) || routeIssuePattern.MatchString(lower)
		strongCompositionAction := routeID == "backend.composition" && compositionOwnershipActionPattern.MatchString(lower)
		analysisAction := routingAnalysisPattern.MatchString(lower)
		dependent := routingDependentFragmentPattern.MatchString(lower)
		fragmentAction := routeID != "backend.composition" && !analysisAction && !dependent && isNominalTaskFragment(lower)
		for _, match := range matches {
			actionForMatch := positiveAction && !dependent
			if analysisAction && strings.EqualFold(match.phrase, "application") && !regexp.MustCompile(`(?i)\bapplication\s+(?:layer|workflow|usecase)\b`).MatchString(lower) {
				actionForMatch = false
			}
			if context := routingExplanatoryPattern.FindStringIndex(lower); context != nil && context[0] < match.start && regexp.MustCompile(`(?i)\b(?:using|with|depends on)\s+(?:(?:a|an|the)\s+)?$`).MatchString(lower[:context[0]]) {
				signals = append(signals, signal{domain.RoutePolarityNeutral, match.phrase, clippedTaskSpan(clause.text), "existing dependency is contextual evidence, not this action's target"})
				continue
			}
			if future := regexp.MustCompile(`(?i)\bbefore\s+(?:changing|modifying|implementing|adding)\b`).FindStringIndex(lower); future != nil && match.start >= future[1] {
				signals = append(signals, signal{domain.RoutePolarityNeutral, match.phrase, clippedTaskSpan(clause.text), "future implementation depends on a prior owner decision; not current requested work"})
				continue
			}
			if routeMentionIsNegated(lower, match) {
				signals = append(signals, signal{domain.RoutePolarityNegative, match.phrase, clippedTaskSpan(clause.text), "explicit exclusion phrase applies to this route responsibility"})
				continue
			}
			if conditional {
				signals = append(signals, signal{domain.RoutePolarityConditional, match.phrase, clippedTaskSpan(clause.text), "route action is qualified by conditional language"})
				continue
			}
			if explanatory {
				signals = append(signals, signal{domain.RoutePolarityNeutral, match.phrase, clippedTaskSpan(clause.text), "route appears in explanatory or current-state text"})
				continue
			}
			if routeID == "backend.composition" {
				if strongCompositionAction ||
					compositionActionPattern.MatchString(lower) && routeMatchIsStrongCompositionObject(match.phrase) {
					signals = append(signals, signal{domain.RoutePolarityPositive, match.phrase, clippedTaskSpan(clause.text), "explicit composition ownership action matched"})
				} else if routeActionPattern.MatchString(lower) && routeMatchIsCompositionSurface(match.phrase) {
					signals = append(signals, signal{domain.RoutePolarityPositive, match.phrase, clippedTaskSpan(clause.text), "explicit action targets composition wiring"})
				} else {
					signals = append(signals, signal{domain.RoutePolarityNeutral, match.phrase, clippedTaskSpan(clause.text), "composition terminology without a composition ownership action"})
				}
				continue
			}
			if actionForMatch || fragmentAction {
				detail := "positive task action matched this route responsibility"
				if fragmentAction && !actionForMatch {
					detail = "bounded task-fragment form implies requested work for this route"
				}
				signals = append(signals, signal{domain.RoutePolarityPositive, match.phrase, clippedTaskSpan(clause.text), detail})
			} else {
				signals = append(signals, signal{domain.RoutePolarityNeutral, match.phrase, clippedTaskSpan(clause.text), "route terminology has no positive task-action signal"})
			}
		}
	}
	if len(signals) == 0 {
		return result
	}
	hasPositive, hasNegative, hasConditional := false, false, false
	for _, item := range signals {
		switch item.polarity {
		case domain.RoutePolarityPositive:
			hasPositive = true
		case domain.RoutePolarityNegative:
			hasNegative = true
		case domain.RoutePolarityConditional:
			hasConditional = true
		}
	}
	switch {
	case hasPositive && hasNegative:
		result.Polarity = domain.RoutePolarityConflicting
		result.CandidateSignal = "positive and explicit negative instructions target the same route responsibility"
	case hasNegative:
		result.Polarity = domain.RoutePolarityNegative
	case hasConditional:
		result.Polarity = domain.RoutePolarityConditional
	case hasPositive:
		result.Polarity = domain.RoutePolarityPositive
	default:
		result.Polarity = domain.RoutePolarityNeutral
	}
	var phrases, spans, details []string
	for _, item := range signals {
		phrases = append(phrases, item.phrase)
		spans = append(spans, item.span)
		details = append(details, item.detail)
	}
	result.MatchedPhrase = strings.Join(uniqueInOrder(phrases), "; ")
	result.TaskSpan = strings.Join(uniqueInOrder(spans), " | ")
	if len(result.TaskSpan) > 360 {
		if coverage != nil {
			coverage.omit("task_span", "", "task_span_display_limit", len(result.TaskSpan)-360)
			coverage.Omissions[len(coverage.Omissions)-1].RouteID = routeID
		}
		result.TaskSpan = result.TaskSpan[:360]
	}
	if result.CandidateSignal == "" {
		result.CandidateSignal = strings.Join(uniqueInOrder(details), "; ")
	}
	return result
}

func splitRoutingClauses(text string) []routingClause {
	var clauses []routingClause
	last := 0
	for _, boundary := range routingClauseSeparator.FindAllStringIndex(text, -1) {
		if strings.TrimSpace(text[last:boundary[0]]) != "" {
			start := last
			for start < boundary[0] && (text[start] == ' ' || text[start] == '\t' || text[start] == '\r') {
				start++
			}
			clauses = append(clauses, routingClause{text: strings.TrimSpace(text[last:boundary[0]]), start: start})
		}
		last = boundary[1]
	}
	if strings.TrimSpace(text[last:]) != "" {
		start := last
		for start < len(text) && (text[start] == ' ' || text[start] == '\t' || text[start] == '\r') {
			start++
		}
		clauses = append(clauses, routingClause{text: strings.TrimSpace(text[last:]), start: start})
	}
	return clauses
}

func routePhraseMatches(routeID, clause string) []routePhraseMatch {
	keywords := append([]string(nil), routeSignalKeywords[routeID]...)
	lower := strings.ToLower(clause)
	if routeID == "backend.composition" && compositionOwnershipActionPattern.MatchString(lower) {
		keywords = append(keywords, "route", "router", "endpoint", "handler", "consumer", "component", "http router", "nats consumer")
	}
	var matches []routePhraseMatch
	for _, keyword := range uniqueSorted(keywords) {
		patternText := regexp.QuoteMeta(keyword)
		patternText = strings.ReplaceAll(patternText, `\ `, `\s+`)
		patternText = strings.ReplaceAll(patternText, ` `, `\s+`)
		pattern := regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(` + patternText + `)(?:$|[^\p{L}\p{N}_])`)
		for _, indexes := range pattern.FindAllStringSubmatchIndex(clause, -1) {
			shadowed := false
			for other, phrases := range routeSignalKeywords {
				if other == routeID {
					continue
				}
				for _, phrase := range phrases {
					if len(phrase) <= len(keyword) {
						continue
					}
					re := regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])(` + strings.ReplaceAll(regexp.QuoteMeta(phrase), " ", `\s+`) + `)(?:$|[^\p{L}\p{N}_])`)
					for _, span := range re.FindAllStringSubmatchIndex(clause, -1) {
						if span[2] <= indexes[2] && span[3] >= indexes[3] {
							shadowed = true
						}
					}
				}
			}
			if !shadowed {
				matches = append(matches, routePhraseMatch{phrase: clause[indexes[2]:indexes[3]], start: indexes[2], end: indexes[3]})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		return matches[i].end-matches[i].start > matches[j].end-matches[j].start
	})
	if len(matches) < 2 {
		return matches
	}
	result := matches[:0]
	for _, match := range matches {
		if len(result) > 0 && match.start < result[len(result)-1].end {
			continue
		}
		result = append(result, match)
	}
	return result
}

func routeMentionIsNegated(text string, match routePhraseMatch) bool {
	prefix := text[:match.start]
	for _, marker := range routeNegativePrefixPattern.FindAllStringIndex(prefix, -1) {
		if regexp.MustCompile(`(?i)^\s*(?:executing|running|applying)\b`).MatchString(prefix[marker[1]:]) {
			continue
		}
		if wordCount(prefix[marker[1]:]) <= 10 {
			return true
		}
	}
	if noIndex := regexp.MustCompile(`(?i)\bno\b`).FindStringIndex(prefix); noIndex != nil && wordCount(prefix[noIndex[1]:]) <= 4 {
		suffix := text[match.end:]
		if len(suffix) <= 36 || regexp.MustCompile(`(?i)\bchanges?\b`).MatchString(suffix[:min(len(suffix), 36)]) {
			return true
		}
	}
	suffix := text[match.end:]
	if indexes := routeNegativeSuffixPattern.FindStringIndex(suffix); indexes != nil && wordCount(suffix[:indexes[0]]) <= 8 {
		return true
	}
	return false
}

func isNominalTaskFragment(text string) bool {
	if routingExplanatoryPattern.MatchString(text) || routeTaskFragmentVerbPattern.MatchString(text) {
		return false
	}
	return len(strings.Fields(text)) <= 18
}

func routeMatchIsStrongCompositionObject(phrase string) bool {
	return strings.Contains(strings.ToLower(phrase), "wiring") || strings.Contains(strings.ToLower(phrase), "registration") ||
		strings.Contains(strings.ToLower(phrase), "bootstrap")
}

func routeMatchIsCompositionSurface(phrase string) bool {
	phrase = strings.ToLower(phrase)
	for _, term := range []string{"composition", "dependency injection", "di", "wiring", "registration", "bootstrap"} {
		if strings.Contains(phrase, term) {
			return true
		}
	}
	return false
}

func classifySelectedRoutes(selected []evidenceBackedRouteCandidate, requestText string) string {
	classification := classifyRequest(strings.ToLower(requestText))
	for _, candidate := range selected {
		if classificationForRoute(candidate.route.ID) == classification {
			return classification
		}
	}
	for _, candidate := range selected {
		if value := classificationForRoute(candidate.route.ID); value != "maintenance" {
			return value
		}
	}
	return "maintenance"
}

func wordCount(text string) int {
	return len(strings.Fields(text))
}

func clippedTaskSpan(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 180 {
		text = string(runes[:180])
	}
	return text
}

func uniqueInOrder(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func classifyRequest(text string) string {
	categories := []struct {
		name  string
		words []string
	}{
		{"persistence", []string{"sql", "postgres", "query", "database", "баз данных", "запрос"}},
		{"business-process", []string{"business process", "usecase", "use case", "процесс", "сценар", "бизнес-логик"}},
		{"domain-invariant", []string{"invariant", "domain rule", "инвариант", "предметн правил"}},
		{"http-transport", []string{"http", "endpoint", "handler", "rest api", "эндпоинт", "обработчик"}},
		{"external-client", []string{"external client", "rpc client", "внешн клиент"}},
		{"messaging", []string{"message", "event", "queue", "broker", "сообщен", "событ", "очеред"}},
		{"frontend-ui", []string{"component", "button", "form", "ui", "компонент", "интерфейс", "экран"}},
		{"frontend-api", []string{"api adapter", "api client", "fetch", "адаптер api"}},
		{"frontend-usecase", []string{"user flow", "frontend logic", "сценарий интерфейса"}},
	}
	for _, category := range categories {
		for _, word := range category.words {
			if classificationWordMatches(text, word) {
				return category.name
			}
		}
	}
	return "maintenance"
}

func classificationWordMatches(text, word string) bool {
	// Retain the historical Russian stem behavior; ASCII terms require boundaries.
	for _, r := range word {
		if r > 127 {
			return strings.Contains(text, word)
		}
	}
	return regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_])` + strings.ReplaceAll(regexp.QuoteMeta(word), " ", `\s+`) + `(?:$|[^\p{L}\p{N}_])`).MatchString(text)
}

func classificationForRoute(routeID string) string {
	switch routeID {
	case "backend.domain":
		return "domain-invariant"
	case "backend.usecase", "frontend.module.usecase":
		return "business-process"
	case "backend.infrastructure.persistence", "backend.migration":
		return "persistence"
	case "backend.transport.http", "frontend.route":
		return "http-transport"
	case "backend.infrastructure.client":
		return "external-client"
	case "backend.transport.message", "backend.infrastructure.messaging":
		return "messaging"
	case "frontend.module.ui", "frontend.shared.ui":
		return "frontend-ui"
	case "frontend.module.api", "frontend.shared.bff":
		return "frontend-api"
	default:
		return "maintenance"
	}
}

func localRouteScore(route agentcontrol.ProfileRoute, inventory repositoryInventory) int {
	score := 0
	for _, evidence := range inventory.evidence {
		if evidence.value.Kind != "repository_instructions" && evidence.value.Kind != "service_metadata" &&
			evidence.value.Kind != "architecture_metadata" && evidence.value.Kind != "contract" {
			continue
		}
		text := strings.ToLower(string(evidence.content))
		weight := 0
		switch evidence.value.Kind {
		case "repository_instructions":
			weight = 8
		case "service_metadata":
			weight = 6
		case "architecture_metadata":
			weight = 4
		case "contract":
			weight = 2
		}
		if strings.Contains(text, strings.ToLower(route.ID)) {
			score += weight
		}
		for _, target := range route.Targets {
			root := strings.TrimSuffix(strings.TrimSuffix(target, "/**"), "/*")
			if len(root) >= 5 && strings.Contains(text, strings.ToLower(root)) {
				score += weight / 2
			}
		}
	}
	return score
}

func routeVerification(projectID string, profile agentcontrol.Profile, target domain.RoutedTarget, inventory repositoryInventory) (domain.RouteVerification, []string) {
	boundary := verificationBoundaryForRoute(target.RouteID)
	route := findProfileRoute(profile, target.RouteID)
	var ids []string
	if route != nil {
		for _, evidence := range inventory.evidence {
			if evidence.value.Kind == "test" && evidenceMatchesRoute(evidence.value.Path, route.Targets) {
				ids = append(ids, evidence.value.ID)
			}
		}
	}
	state, rationale := domain.VerificationEvidenceToAdd, "no route-specific test file was found; add focused verification at this boundary"
	if len(ids) > 0 {
		state, rationale = domain.VerificationEvidenceExisting, "route-specific test files exist; use declared repository commands to run this boundary"
	}
	return domain.RouteVerification{
		RouteReference: domain.RouteReference{ProjectID: projectID, RouteID: target.RouteID},
		Boundary:       boundary, EvidenceState: state, EvidenceIDs: uniqueSorted(ids), Rationale: rationale,
	}, ids
}

func verificationBoundaryForRoute(routeID string) string {
	switch routeID {
	case "backend.domain":
		return "domain-unit"
	case "backend.usecase", "frontend.module.usecase":
		return "usecase-unit-with-fakes"
	case "backend.transport.http", "frontend.route":
		return "http-handler-or-route-contract"
	case "backend.infrastructure.persistence":
		return "repository-integration-when-supported"
	case "backend.transport.message", "backend.infrastructure.messaging":
		return "message-schema-or-broker-integration"
	case "backend.infrastructure.client", "frontend.module.api":
		return "adapter-or-schema-test"
	case "frontend.module.ui", "frontend.shared.ui":
		return "component-behavior"
	case "frontend.shared.bff":
		return "bff-request-response-contract"
	case "frontend.i18n":
		return "translation-resource-validation"
	case "backend.migration":
		return "schema-compatibility"
	case "backend.composition", "frontend.app-runtime":
		return "composition-wiring-smoke"
	case "frontend.module.model":
		return "model-schema-test"
	default:
		return ""
	}
}

func sharedBoundaryCandidates(routes []domain.RoutedTarget, profiles map[string]agentcontrol.Profile) []domain.SharedBoundaryCandidate {
	byKind := map[string][]domain.RouteReference{}
	for _, route := range routes {
		profile, ok := profiles[route.ProjectID]
		if !ok {
			continue
		}
		profileRoute := findProfileRoute(profile, route.RouteID)
		if profileRoute == nil {
			continue
		}
		for _, boundary := range profileRoute.SharedBoundaries {
			// Registration is executable composition wiring, not a source
			// interface shared by transport and composition. Keep its routing
			// evidence, but do not infer a reverse transport dependency or
			// materialize a synthetic registration contract for freeze.
			// Database schema is likewise an inspected DDL artifact, not a Go
			// interface or a reverse import from persistence to migration.
			if boundary == "http-route-registration" || boundary == "message-consumer-registration" || boundary == "database-schema" {
				continue
			}
			byKind[boundary] = append(byKind[boundary], routeReference(route))
		}
	}
	var result []domain.SharedBoundaryCandidate
	for kind, refs := range byKind {
		refs = uniqueRouteReferences(refs)
		if len(refs) >= 2 {
			result = append(result, domain.SharedBoundaryCandidate{Kind: kind, Routes: refs})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	return result
}

func buildContractPlan(status domain.RoutingStatus, routes []domain.RoutedTarget, candidates []domain.SharedBoundaryCandidate, inventories map[string]repositoryInventory, profiles map[string]agentcontrol.Profile, requestText string) (domain.ContractPlan, error) {
	var refs []domain.RouteReference
	for _, route := range routes {
		refs = append(refs, routeReference(route))
	}
	refs = uniqueRouteReferences(refs)
	if len(candidates) > 0 {
		var independent []domain.RouteReference
		for _, ref := range refs {
			if !isCompositionRoute(ref.RouteID) {
				independent = append(independent, ref)
			}
		}
		plan := domain.ContractPlan{
			State: domain.ContractPlanFreezeNeeded, Required: true,
			Reason:         "multiple routed areas share canonical boundary candidates; an owner-reviewed contract plan must precede future route-level parallel work",
			AffectedRoutes: refs, FreezeRequired: true, IndependentAfterFreeze: [][]domain.RouteReference{independent},
		}
		for _, candidate := range candidates {
			owner, ownerErr := resolveContractOwner(candidate, profiles, inventories)
			if ownerErr != nil {
				return domain.ContractPlan{}, ownerErr
			}
			consumers := make([]domain.RouteReference, 0, len(candidate.Routes)-1)
			for _, ref := range candidate.Routes {
				if ref != owner {
					consumers = append(consumers, ref)
				}
			}
			boundary := domain.PlannedContractBoundary{
				Kind: candidate.Kind, Owner: owner, Consumers: consumers,
				Implementers: contractImplementers(profiles[owner.ProjectID], candidate.Kind, consumers),
				Rationale:    "canonical profile identifies a shared boundary candidate; source contract is not frozen by this plan",
			}
			ownerRoute := findProfileRoute(profiles[owner.ProjectID], owner.RouteID)
			for _, item := range inventories[owner.ProjectID].evidence {
				if !evidenceSupportsBoundary(candidate.Kind, ownerRoute, item) ||
					item.value.Kind == "source" && !agentcontrol.ContractPathOwnedByRoute(profiles[owner.ProjectID], owner.RouteID, item.value.Path) {
					continue
				}
				boundary.Existing, boundary.SourceEvidenceID = true, item.value.ID
				break
			}
			if !boundary.Existing && candidate.Kind == "application-command-result" {
				if item, ok := taskTypedApplicationBoundary(requestText, ownerRoute, candidate.Routes, inventories[owner.ProjectID], profiles[owner.ProjectID]); ok {
					boundary.Existing, boundary.SourceEvidenceID = true, item.value.ID
					boundary.Rationale = "task-named typed owner signature and consumer selector syntax; freeze and type compatibility remain unverified"
				}
			}
			references, err := contractbaseline.PlannedContractReferences(owner.ProjectID,
				profiles[owner.ProjectID], []domain.PlannedContractBoundary{boundary}, evidenceValues(inventories[owner.ProjectID].evidence))
			if err != nil {
				return domain.ContractPlan{}, fmt.Errorf("select approved contract target path for %q owned by %s: %w", candidate.Kind, owner.RouteID, err)
			}
			boundary.TargetPath = references[0].Path
			plan.Boundaries = append(plan.Boundaries, boundary)
			plan.ContractOwnerRoutes = uniqueRouteReferences(append(plan.ContractOwnerRoutes, owner))
		}
		return plan, nil
	}
	if status != domain.RoutingStatusResolved {
		return domain.ContractPlan{
			State: domain.ContractPlanPlanned, Required: true, AffectedRoutes: refs,
			Reason: "routing is incomplete; contract-plan necessity must be decided after owner-reviewed route resolution",
		}, nil
	}
	reason := "one isolated route; contract planning is not required"
	if len(refs) == 0 {
		reason = "no evidence-backed route was resolved; profile review is required before contract-plan necessity can be assessed"
	} else if len(refs) > 1 {
		reason = "multiple evidence-backed routes have no shared boundary candidate"
	}
	return domain.ContractPlan{State: domain.ContractPlanNotRequired, Reason: reason}, nil
}

func evidenceValues(items []indexedEvidence) []domain.RoutingEvidence {
	result := make([]domain.RoutingEvidence, 0, len(items))
	for _, item := range items {
		result = append(result, item.value)
	}
	return result
}

func selectBoundaryOwner(kind string, refs []domain.RouteReference) domain.RouteReference {
	preferred := map[string][]string{
		"domain-model":                  {"backend.domain", "frontend.module.model"},
		"application-command-result":    {"backend.usecase", "frontend.module.usecase"},
		"repository-port":               {"backend.domain", "backend.usecase"},
		"external-client-port":          {"backend.domain", "backend.usecase", "frontend.module.usecase"},
		"publisher-port":                {"backend.domain", "backend.usecase"},
		"http-route-registration":       {"backend.composition", "backend.transport.http", "frontend.route"},
		"message-envelope":              {"backend.transport.message", "backend.infrastructure.messaging"},
		"message-consumer-registration": {"backend.composition", "backend.transport.message"},
		"database-schema":               {"backend.migration", "backend.infrastructure.persistence"},
		"route-to-module":               {"frontend.route", "frontend.module.ui"},
		"bff-request-response":          {"frontend.shared.bff", "frontend.route"},
		"usecase-input-output":          {"frontend.module.usecase"},
		"shared-ui-props":               {"frontend.shared.ui", "frontend.module.ui"},
		"i18n-keys":                     {"frontend.i18n", "frontend.module.ui"},
		"api-adapter-boundary":          {"frontend.module.api", "frontend.module.usecase", "frontend.shared.bff"},
		"model-schema":                  {"frontend.module.model", "frontend.module.api"},
	}
	for _, routeID := range preferred[kind] {
		for _, ref := range refs {
			if ref.RouteID == routeID {
				return ref
			}
		}
	}
	return refs[0]
}

func evidenceSupportsBoundary(kind string, route *agentcontrol.ProfileRoute, evidence indexedEvidence) bool {
	if evidence.value.Kind == "contract" {
		return containsString(evidence.value.BoundaryKinds, kind)
	}
	return evidence.value.Kind == "source" && route != nil &&
		evidenceMatchesRoute(evidence.value.Path, route.InterfaceCandidates) &&
		containsString(evidence.value.BoundaryKinds, kind)
}

func detectedBoundaryKinds(relative, kind string, content []byte, symbols []string) []string {
	text := strings.ToLower(strings.ReplaceAll(string(content), "-", " "))
	text += " " + strings.ToLower(strings.ReplaceAll(relative, "-", " "))
	lowerSymbols := strings.ToLower(strings.Join(symbols, " "))
	interfaceDeclared := strings.Contains(strings.ToLower(string(content)), "interface ") ||
		strings.Contains(strings.ToLower(string(content)), "interface{")
	var result []string
	if kind == "contract" {
		for _, boundary := range []string{
			"domain-model", "application-command-result", "repository-port", "external-client-port", "publisher-port",
			"http-route-registration", "message-envelope", "message-consumer-registration", "database-schema",
			"route-to-module", "bff-request-response", "usecase-input-output", "shared-ui-props", "i18n-keys",
			"api-adapter-boundary", "model-schema",
		} {
			phrase := strings.ReplaceAll(boundary, "-", " ")
			if strings.Contains(text, phrase) || strings.Contains(strings.ToLower(string(content)), boundary) {
				result = append(result, boundary)
			}
		}
	}
	if kind == "source" || kind == "test" {
		if interfaceDeclared && (strings.Contains(lowerSymbols, "repo") || strings.Contains(lowerSymbols, "repository") || strings.Contains(lowerSymbols, "port")) {
			result = append(result, "repository-port")
		}
		if interfaceDeclared && (strings.Contains(lowerSymbols, "client") || strings.Contains(lowerSymbols, "external")) {
			result = append(result, "external-client-port")
		}
		if interfaceDeclared && strings.Contains(lowerSymbols, "publisher") {
			result = append(result, "publisher-port")
		}
		if strings.Contains(lowerSymbols, "command") && strings.Contains(lowerSymbols, "result") {
			result = append(result, "application-command-result", "usecase-input-output")
		}
		if (strings.Contains(relative, "/domain/") || strings.HasPrefix(relative, "domain/")) && strings.Contains(text, "type ") {
			result = append(result, "domain-model")
		}
		if strings.HasSuffix(relative, ".sql") || strings.Contains(text, "create table") || strings.Contains(text, "alter table") {
			result = append(result, "database-schema")
		}
	}
	return uniqueSorted(result)
}

func buildBoundedEvidenceIndex(all []indexedEvidence, routing domain.RoutingResult, plan domain.ContractPlan) []domain.RoutingEvidence {
	byID := make(map[string]domain.RoutingEvidence, len(all))
	for _, item := range all {
		byID[item.value.ID] = item.value
	}
	var ordered []domain.RoutingEvidence
	seen := map[string]struct{}{}
	add := func(id string) {
		if evidence, ok := byID[id]; ok {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				ordered = append(ordered, evidence)
			}
		}
	}
	for _, id := range routing.EvidenceIDs {
		add(id)
	}
	for _, resolution := range routing.Profiles {
		for _, id := range resolution.EvidenceIDs {
			add(id)
		}
	}
	for _, route := range routing.Routes {
		for _, id := range route.EvidenceIDs {
			add(id)
		}
	}
	for _, verification := range routing.Verification {
		for _, id := range verification.EvidenceIDs {
			add(id)
		}
	}
	for _, boundary := range plan.Boundaries {
		add(boundary.SourceEvidenceID)
	}
	for _, item := range all {
		add(item.value.ID)
		if len(ordered) >= maxRoutingEvidence {
			break
		}
	}
	if len(ordered) > maxRoutingEvidence {
		ordered = ordered[:maxRoutingEvidence]
	}
	return ordered
}

func canonicalPromptAssets(catalog agentcontrol.Catalog, profileIDs []string) ([]canonicalPromptAsset, error) {
	wanted := map[string]struct{}{"global-policy": {}, "skill/task-route": {}, "skill/contract-plan": {}}
	for _, profileID := range uniqueSorted(profileIDs) {
		if profileID != "" {
			wanted["profile/"+profileID] = struct{}{}
		}
	}
	assets := make([]canonicalPromptAsset, 0, len(wanted))
	seen := map[string]bool{}
	for _, asset := range catalog.Assets {
		if _, ok := wanted[asset.Asset.ID]; !ok {
			continue
		}
		content := ""
		if strings.HasPrefix(asset.Asset.ID, "skill/") {
			bytes, ok := asset.Files["SKILL.md"]
			if !ok {
				return nil, fmt.Errorf("canonical skill %q has no SKILL.md", asset.Asset.ID)
			}
			content = string(bytes)
		} else {
			for _, bytes := range asset.Files {
				content = string(bytes)
				break
			}
		}
		seen[asset.Asset.ID] = true
		assets = append(assets, canonicalPromptAsset{ID: asset.Asset.ID, Checksum: asset.Checksum, Content: content})
	}
	if !seen["global-policy"] || !seen["skill/task-route"] || !seen["skill/contract-plan"] {
		return nil, fmt.Errorf("canonical shared policy, task-route, and contract-plan assets are required")
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].ID < assets[j].ID })
	return assets, nil
}

func bytesContainNextDependency(content []byte) bool {
	var manifest struct {
		Dependencies     map[string]json.RawMessage `json:"dependencies"`
		DevDependencies  map[string]json.RawMessage `json:"devDependencies"`
		PeerDependencies map[string]json.RawMessage `json:"peerDependencies"`
	}
	if json.Unmarshal(content, &manifest) != nil {
		return false
	}
	for _, dependencies := range []map[string]json.RawMessage{manifest.Dependencies, manifest.DevDependencies, manifest.PeerDependencies} {
		if _, ok := dependencies["next"]; ok {
			return true
		}
	}
	return false
}

func repositoryFacts(inventory repositoryInventory) []plannerRepositoryFact {
	var facts []plannerRepositoryFact
	total := 0
	for _, evidence := range inventory.evidence {
		switch evidence.value.Kind {
		case "repository_instructions", "service_metadata", "architecture_metadata", "contract", "commands", "test_manifest", "stack_manifest":
		default:
			continue
		}
		if total+len(evidence.content) > maxRepositoryFactsBytes {
			break
		}
		facts = append(facts, plannerRepositoryFact{
			Path: evidence.value.Path, Kind: evidence.value.Kind, Content: string(evidence.content),
		})
		total += len(evidence.content)
	}
	recordRepositoryFactsCoverage(inventory.coverage, inventory.evidence, facts, total)
	return facts
}

func hasPathPrefix(inventory repositoryInventory, prefixes ...string) bool {
	for _, evidence := range inventory.evidence {
		for _, prefix := range prefixes {
			if strings.HasPrefix(evidence.value.Path, prefix) {
				return true
			}
		}
	}
	return false
}

func extractSymbols(relative string, content []byte) []string {
	pattern := tsSymbolPattern
	if strings.HasSuffix(relative, ".go") {
		pattern = goSymbolPattern
	}
	matches := pattern.FindAllSubmatch(content, maxRoutingSymbolMatches)
	var values []string
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, string(match[1]))
		}
	}
	return uniqueSorted(values)
}

func extractImports(relative string, content []byte) []string {
	if !strings.HasSuffix(relative, ".go") {
		return nil
	}
	matches := goImportPattern.FindAllSubmatch(content, maxRoutingSymbolMatches)
	var values []string
	for _, match := range matches {
		if len(match) > 1 {
			values = append(values, string(match[1]))
		}
	}
	return uniqueSorted(values)
}

func routeReference(route domain.RoutedTarget) domain.RouteReference {
	return domain.RouteReference{ProjectID: route.ProjectID, RouteID: route.RouteID}
}

func primaryRoute(routes []domain.RoutedTarget, classification string) domain.RouteReference {
	preferred := map[string][]string{
		"persistence":      {"backend.infrastructure.persistence", "backend.migration"},
		"business-process": {"backend.usecase", "frontend.module.usecase"},
		"domain-invariant": {"backend.domain", "frontend.module.model"},
		"http-transport":   {"backend.transport.http", "frontend.route"},
		"external-client":  {"backend.infrastructure.client", "frontend.module.api"},
		"messaging":        {"backend.infrastructure.messaging", "backend.transport.message"},
		"frontend-ui":      {"frontend.module.ui", "frontend.shared.ui"},
		"frontend-api":     {"frontend.module.api", "frontend.shared.bff"},
		"frontend-usecase": {"frontend.module.usecase"},
	}
	for _, routeID := range preferred[classification] {
		for _, route := range routes {
			if route.RouteID == routeID {
				return routeReference(route)
			}
		}
	}
	return routeReference(routes[0])
}

func findProfileRoute(profile agentcontrol.Profile, routeID string) *agentcontrol.ProfileRoute {
	for index := range profile.Routes {
		if profile.Routes[index].ID == routeID {
			return &profile.Routes[index]
		}
	}
	return nil
}

func retainEvidenceIDs(ids []string, available map[string]struct{}) []string {
	var result []string
	for _, id := range ids {
		if _, ok := available[id]; ok {
			result = append(result, id)
		}
	}
	return uniqueSorted(result)
}

func uniqueRouteReferences(refs []domain.RouteReference) []domain.RouteReference {
	seen := map[string]struct{}{}
	var result []domain.RouteReference
	for _, ref := range refs {
		if ref.ProjectID == "" || ref.RouteID == "" {
			continue
		}
		key := ref.ProjectID + "\x00" + ref.RouteID
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			result = append(result, ref)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ProjectID != result[j].ProjectID {
			return result[i].ProjectID < result[j].ProjectID
		}
		return result[i].RouteID < result[j].RouteID
	})
	return result
}

func sortProfileResolutions(values []domain.ArchitectureProfileResolution) []domain.ArchitectureProfileResolution {
	sort.Slice(values, func(i, j int) bool { return values[i].ProjectID < values[j].ProjectID })
	return values
}

func sortRoutedTargets(values []domain.RoutedTarget) []domain.RoutedTarget {
	sort.Slice(values, func(i, j int) bool {
		if values[i].ProjectID != values[j].ProjectID {
			return values[i].ProjectID < values[j].ProjectID
		}
		return values[i].RouteID < values[j].RouteID
	})
	return values
}

func sortVerifications(values []domain.RouteVerification) []domain.RouteVerification {
	sort.Slice(values, func(i, j int) bool {
		if values[i].ProjectID != values[j].ProjectID {
			return values[i].ProjectID < values[j].ProjectID
		}
		return values[i].RouteID < values[j].RouteID
	})
	return values
}

func sortContractInspections(values []domain.ContractInspection) []domain.ContractInspection {
	sort.Slice(values, func(i, j int) bool {
		if values[i].ProjectID != values[j].ProjectID {
			return values[i].ProjectID < values[j].ProjectID
		}
		return values[i].Kind < values[j].Kind
	})
	return values
}

func joinReason(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}

func anyProfileUnresolved(profiles []domain.ArchitectureProfileResolution) bool {
	for _, profile := range profiles {
		if profile.Status != domain.ProfileResolutionResolved {
			return true
		}
	}
	return false
}

func resolveContractOwner(candidate domain.SharedBoundaryCandidate, profiles map[string]agentcontrol.Profile, inventories map[string]repositoryInventory) (domain.RouteReference, error) {
	projectID := candidate.Routes[0].ProjectID
	profile := profiles[projectID]
	rule, explicit := agentcontrol.BoundaryOwnershipFor(profile, candidate.Kind)
	refs := candidate.Routes
	if explicit {
		refs = nil
		// Compatible existing owner evidence wins over the profile's ordered default.
		for _, id := range rule.OwnerRoutes {
			route := findProfileRoute(profile, id)
			for _, item := range inventories[projectID].evidence {
				if evidenceSupportsBoundary(candidate.Kind, route, item) && agentcontrol.ContractPathOwnedByRoute(profile, id, item.value.Path) {
					refs = append(refs, domain.RouteReference{ProjectID: projectID, RouteID: id})
					break
				}
			}
		}
		for _, id := range rule.OwnerRoutes {
			refs = append(refs, domain.RouteReference{ProjectID: projectID, RouteID: id})
		}
	} else {
		refs = []domain.RouteReference{selectBoundaryOwner(candidate.Kind, refs)}
	}
	for _, owner := range refs {
		route := findProfileRoute(profile, owner.RouteID)
		if route == nil {
			continue
		}
		proven := false
		for _, item := range inventories[projectID].evidence {
			if item.value.Kind == "source" && evidenceMatchesRoute(item.value.Path, route.Targets) {
				proven = true
				break
			}
		}
		if explicit && !proven {
			continue
		}
		valid := true
		for _, ref := range candidate.Routes {
			if ref.ProjectID != owner.ProjectID || !agentcontrol.ContractDependencyAllowed(profile, ref.RouteID, owner.RouteID) {
				valid = false
				break
			}
		}
		if valid {
			return owner, nil
		}
	}
	return domain.RouteReference{}, fmt.Errorf("ARCHITECTURE_CONFLICT: boundary %s has no evidence-backed owner allowed by consumer dependencies: %w", candidate.Kind, domain.ErrValidation)
}
func contractImplementers(profile agentcontrol.Profile, kind string, consumers []domain.RouteReference) []domain.RouteReference {
	rule, _ := agentcontrol.BoundaryOwnershipFor(profile, kind)
	var result []domain.RouteReference
	for _, ref := range consumers {
		if containsString(rule.ImplementerRoutes, ref.RouteID) {
			result = append(result, ref)
		}
	}
	return result
}

func isCompositionRoute(routeID string) bool {
	return routeID == "backend.composition" || routeID == "frontend.app-runtime" || routeID == "frontend.shared.bff"
}

// Ambiguous infrastructure/http roots need both an owner declaration and
// outbound source syntax. Legacy unambiguous client roots retain their contract.
func verifiedOutboundRoot(route string, evidence indexedEvidence, inventory repositoryInventory) bool {
	if route != "backend.infrastructure.client" || (!strings.HasPrefix(evidence.value.Path, "internal/infrastructure/http/") && !strings.HasPrefix(evidence.value.Path, "internal/infrastructure/adapters/")) {
		return true
	}
	directory := path.Dir(evidence.value.Path)
	declared := false
	for _, item := range inventory.evidence {
		if item.value.Kind != "architecture_metadata" && item.value.Kind != "service_metadata" {
			continue
		}
		var declaration yaml.Node
		if yaml.Unmarshal(item.content, &declaration) == nil {
			declared = declared || declaresOutboundDirectory(&declaration, directory, false)
		}
	}
	if !declared {
		return false
	}
	for _, item := range inventory.evidence {
		if path.Dir(item.value.Path) == directory && item.value.Kind == "source" && outboundHTTPSyntax(item.content) {
			return true
		}
	}
	return false
}

func declaresOutboundDirectory(node *yaml.Node, directory string, outbound bool) bool {
	if node.Kind == yaml.AliasNode {
		return false
	}
	if node.Kind == yaml.ScalarNode {
		return outbound && (node.Value == directory || strings.HasPrefix(directory, strings.TrimSuffix(node.Value, "/")+"/"))
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := strings.ToLower(node.Content[i].Value)
			direction := outbound || strings.Contains(key, "outbound") || strings.Contains(key, "outgoing") || strings.Contains(key, "client")
			if strings.Contains(key, "inbound") {
				direction = false
			}
			if declaresOutboundDirectory(node.Content[i+1], directory, direction) {
				return true
			}
		}
		return false
	}
	for _, child := range node.Content {
		if declaresOutboundDirectory(child, directory, outbound) {
			return true
		}
	}
	return false
}

// Bounded AST syntax establishes a concrete HTTP call, never execution or
// type-aware ownership. Comments, strings and unrelated Do methods are ignored.
func outboundHTTPSyntax(content []byte) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "client.go", content, 0)
	if err != nil {
		return false
	}
	alias := ""
	for _, imp := range file.Imports {
		value, _ := strconv.Unquote(imp.Path.Value)
		if value == "net/http" {
			alias = "http"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
		}
	}
	if alias == "" || alias == "_" || alias == "." {
		return false
	}
	clients := map[string]bool{}
	isClient := func(expr ast.Expr) bool {
		if star, ok := expr.(*ast.StarExpr); ok {
			expr = star.X
		}
		selector, ok := expr.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Client" {
			return false
		}
		ident, ok := selector.X.(*ast.Ident)
		return ok && ident.Name == alias
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Field:
			if isClient(v.Type) {
				for _, name := range v.Names {
					clients[name.Name] = true
				}
			}
		case *ast.ValueSpec:
			if isClient(v.Type) {
				for _, name := range v.Names {
					clients[name.Name] = true
				}
			}
		case *ast.AssignStmt:
			for i, rhs := range v.Rhs {
				if i >= len(v.Lhs) {
					break
				}
				if unary, ok := rhs.(*ast.UnaryExpr); ok {
					rhs = unary.X
				}
				if literal, ok := rhs.(*ast.CompositeLit); ok && isClient(literal.Type) {
					if name, ok := v.Lhs[i].(*ast.Ident); ok {
						clients[name.Name] = true
					}
				}
			}
		}
		return true
	})
	// Copies of an already established HTTP client retain the same syntax identity.
	ast.Inspect(file, func(n ast.Node) bool {
		if assignment, ok := n.(*ast.AssignStmt); ok {
			for i, rhs := range assignment.Rhs {
				if star, ok := rhs.(*ast.StarExpr); ok {
					rhs = star.X
				}
				if i >= len(assignment.Lhs) {
					break
				}
				if name, ok := rhs.(*ast.Ident); ok && clients[name.Name] {
					if target, ok := assignment.Lhs[i].(*ast.Ident); ok {
						clients[target.Name] = true
					}
				}
			}
		}
		return true
	})
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		method := selector.Sel.Name
		if receiver, ok := selector.X.(*ast.Ident); ok {
			if receiver.Name == alias && (method == "Get" || method == "Post" || method == "PostForm") {
				found = true
			}
			if clients[receiver.Name] && (method == "Do" || method == "Get" || method == "Post" || method == "PostForm") {
				found = true
			}
		}
		if receiver, ok := selector.X.(*ast.SelectorExpr); ok && clients[receiver.Sel.Name] && method == "Do" {
			found = true
		}
		return true
	})
	return found
}

// Fair acquisition visits root files first, then yields one file per subtree
// in deterministic rounds. The canonical internal layers get separate lanes,
// so early metadata or persistence cannot starve a later usecase directory.
// The visitor still owns all policy exclusions and global acquisition limits.
func walkRoutingInventory(root string, visit fs.WalkDirFunc) error {
	type node struct {
		name  string
		entry fs.DirEntry
		err   error
	}
	type lane struct{ pending []node }
	children := func(name string) []node {
		entries, err := os.ReadDir(name)
		if err != nil {
			return []node{{name: name, err: err}}
		}
		nodes := make([]node, 0, len(entries))
		for _, entry := range entries {
			nodes = append(nodes, node{filepath.Join(name, entry.Name()), entry, nil})
		}
		return nodes
	}
	var lanes []*lane
	var files []node
	for _, n := range children(root) {
		if n.err != nil {
			if err := visit(n.name, nil, n.err); err != nil {
				return err
			}
			continue
		}
		if !n.entry.IsDir() {
			files = append(files, n)
			continue
		}
		err := visit(n.name, n.entry, nil)
		if err == filepath.SkipAll {
			return nil
		}
		if err == filepath.SkipDir {
			continue
		}
		if err != nil {
			return err
		}
		if n.entry.Name() == "internal" {
			var internalFiles []node
			for _, child := range children(n.name) {
				if child.entry != nil && child.entry.IsDir() {
					lanes = append(lanes, &lane{pending: []node{child}})
				} else {
					internalFiles = append(internalFiles, child)
				}
			}
			if len(internalFiles) > 0 {
				lanes = append(lanes, &lane{pending: internalFiles})
			}
		} else {
			lanes = append(lanes, &lane{pending: children(n.name)})
		}
	}
	// Keep exact-cap behavior for flat inventories and stack/owner root files.
	for _, n := range files {
		err := visit(n.name, n.entry, n.err)
		if err == filepath.SkipAll {
			return nil
		}
		if err != nil {
			return err
		}
	}
	for len(lanes) > 0 {
		active := lanes[:0]
		for _, l := range lanes {
			for len(l.pending) > 0 {
				n := l.pending[0]
				l.pending = l.pending[1:]
				err := visit(n.name, n.entry, n.err)
				if err == filepath.SkipAll {
					return nil
				}
				if err == filepath.SkipDir {
					continue
				}
				if err != nil {
					return err
				}
				if n.err == nil && n.entry.IsDir() {
					l.pending = append(children(n.name), l.pending...)
					continue
				}
				break
			}
			if len(l.pending) > 0 {
				active = append(active, l)
			}
		}
		lanes = active
	}
	return nil
}

// namedSourceSyntax associates explicit task identifiers with bounded admitted
// declarations or literal qualified selector syntax. This is file/layer evidence,
// not a caller graph, receiver type proof, or permission to edit dependencies.
func namedSourceSyntax(task string, files []indexedEvidence) bool {
	inspection := regexp.MustCompile(`(?i)^\s*inspect\b`).MatchString(task)
	separator := regexp.MustCompile(`(?i)\b(?:but|however|while)\b|[,!?;\n]+|\.\s+|\.$`)
	for _, clause := range separator.Split(task, -1) {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if routingConditionalPattern.MatchString(clause) || routingDependentFragmentPattern.MatchString(clause) || routingAnalysisPattern.MatchString(clause) && routingExplanatoryPattern.MatchString(clause) {
			continue
		}
		action := requestedRoutingActionPattern.MatchString(clause)
		if !action && (!inspection || routingExplanatoryPattern.MatchString(clause)) {
			continue
		}
		if regexp.MustCompile(`(?i)\b(?:dependency|unchanged|out.of.scope|do not|must not|before changing)\b`).MatchString(clause) {
			continue
		}
		if regexp.MustCompile(`(?i)\b(?:using|with|depends on)\s+(?:(?:a|an|the)\s+)?(?:existing|current)\b`).MatchString(clause) {
			continue
		}
		if namedSourceClauseSyntax(clause, files) {
			return true
		}
	}
	return false
}

func namedSourceClauseSyntax(task string, files []indexedEvidence) bool {
	mentioned := func(name string) bool {
		return len(name) >= 4 && regexp.MustCompile(`(?:^|[^\p{L}\p{N}_])`+regexp.QuoteMeta(name)+`(?:$|[^\p{L}\p{N}_])`).MatchString(task)
	}
	qualified := regexp.MustCompile(`\b[A-Z][A-Za-z0-9_]*\.[A-Z][A-Za-z0-9_]*\b`).FindAllString(task, -1)
	for _, evidence := range files {
		if mentioned(evidence.value.Path) {
			return true
		}
		if !strings.HasSuffix(evidence.value.Path, ".go") || strings.HasSuffix(evidence.value.Path, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), "source.go", evidence.content, 0)
		if err != nil {
			continue
		}
		for _, decl := range tree.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil && len(d.Recv.List) > 0 {
					recv := d.Recv.List[0].Type
					if star, ok := recv.(*ast.StarExpr); ok {
						recv = star.X
					}
					if ident, ok := recv.(*ast.Ident); ok && mentioned(ident.Name+"."+d.Name.Name) {
						return true
					}
				}
			}
		}
		for _, name := range qualified {
			found := false
			ast.Inspect(tree, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					if recv, ok := sel.X.(*ast.SelectorExpr); ok && recv.Sel.Name+"."+sel.Sel.Name == name {
						found = true
					}
				}
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
}

// taskTypedApplicationBoundary recognizes actual task-named owner signatures
// without requiring conventional Command/Result names or creating a new file.
// Consumer selector syntax is corroboration only, not receiver-type resolution.
func taskTypedApplicationBoundary(task string, owner *agentcontrol.ProfileRoute, refs []domain.RouteReference, inventory repositoryInventory, profile agentcontrol.Profile) (indexedEvidence, bool) {
	if owner == nil {
		return indexedEvidence{}, false
	}
	mentions := func(name string) bool {
		return len(name) >= 4 && regexp.MustCompile(`(?:^|[^\p{L}\p{N}_])`+regexp.QuoteMeta(name)+`(?:$|[^\p{L}\p{N}_])`).MatchString(task)
	}
	custom := func(expr ast.Expr) bool {
		if star, ok := expr.(*ast.StarExpr); ok {
			expr = star.X
		}
		switch e := expr.(type) {
		case *ast.Ident:
			return e.Name != "error" && ast.IsExported(e.Name)
		case *ast.SelectorExpr:
			if x, ok := e.X.(*ast.Ident); ok {
				return x.Name != "context" && ast.IsExported(e.Sel.Name)
			}
		}
		return false
	}
	for _, item := range inventory.evidence {
		if item.value.Kind != "source" || !strings.HasSuffix(item.value.Path, ".go") || !agentcontrol.ContractPathOwnedByRoute(profile, owner.ID, item.value.Path) {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), "owner.go", item.content, 0)
		if err != nil {
			continue
		}
		for _, decl := range tree.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if !ok || f.Recv == nil || len(f.Recv.List) == 0 || f.Type.Params == nil || f.Type.Results == nil {
				continue
			}
			recv := f.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			typ, ok := recv.(*ast.Ident)
			if !ok || !mentions(typ.Name) && !mentions(typ.Name+"."+f.Name.Name) {
				continue
			}
			input, output := false, false
			for _, p := range f.Type.Params.List {
				isContext := false
				if sel, ok := p.Type.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok {
						isContext = pkg.Name == "context" && sel.Sel.Name == "Context"
					}
				}
				input = input || !isContext
			}
			for _, p := range f.Type.Results.List {
				output = output || custom(p.Type)
			}
			if !input || !output {
				continue
			}
			signature := typ.Name + "." + f.Name.Name
			for _, consumer := range refs {
				if consumer.RouteID == owner.ID {
					continue
				}
				route := findProfileRoute(profile, consumer.RouteID)
				if route == nil {
					continue
				}
				for _, e := range inventory.evidence {
					if evidenceMatchesRoute(e.value.Path, route.Targets) && (namedSourceClauseSyntax(signature, []indexedEvidence{e}) || declaredOwnerFieldCall(item, typ.Name, f.Name.Name, e, inventory)) {
						return item, true
					}
				}
			}
		}
	}
	return indexedEvidence{}, false
}

// declaredOwnerFieldCall follows an explicit imported struct-field type alias
// and selector expression inside one admitted consumer file. It records syntax
// corroboration, not a type-checked provider/consumer compatibility certificate.
func declaredOwnerFieldCall(owner indexedEvidence, typ, method string, consumer indexedEvidence, inventory repositoryInventory) bool {
	if !strings.HasSuffix(consumer.value.Path, ".go") {
		return false
	}
	mod, ok := inventory.byPath["go.mod"]
	if !ok {
		return false
	}
	module := ""
	for _, line := range strings.Split(string(mod.content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			module = strings.Trim(fields[1], `"`)
			break
		}
	}
	if module == "" {
		return false
	}
	ownerImport := module + "/" + path.Dir(owner.value.Path)
	tree, err := parser.ParseFile(token.NewFileSet(), "consumer.go", consumer.content, 0)
	if err != nil {
		return false
	}
	imports := map[string]bool{}
	for _, imp := range tree.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != ownerImport {
			continue
		}
		alias := path.Base(p)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		imports[alias] = true
	}
	aliases := map[string]bool{}
	ast.Inspect(tree, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok {
			return true
		}
		expr := field.Type
		if star, ok := expr.(*ast.StarExpr); ok {
			expr = star.X
		}
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != typ {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || !imports[pkg.Name] {
			return true
		}
		for _, name := range field.Names {
			aliases[name.Name] = true
		}
		return true
	})
	found := false
	ast.Inspect(tree, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != method {
			return true
		}
		recv, ok := sel.X.(*ast.SelectorExpr)
		if ok && aliases[recv.Sel.Name] {
			found = true
		}
		return !found
	})
	return found
}

// Literal protocol discovery is bounded local syntax evidence. Imported constant
// references must bind to the admitted module, and only implementation calls
// count. It establishes a layer to inspect, never runtime registration/execution.
func verifiedProtocolTask(task, route string, files []indexedEvidence, inventory repositoryInventory) bool {
	if route != "backend.transport.http" && route != "backend.transport.message" {
		return false
	}
	module := ""
	for _, e := range inventory.evidence {
		if e.value.Path == "go.mod" {
			fields := strings.Fields(string(e.content))
			if len(fields) > 1 && fields[0] == "module" {
				module = strings.Trim(fields[1], `"`)
			}
		}
	}
	constants := map[string]string{}
	for _, e := range inventory.evidence {
		if e.value.Kind != "source" || !strings.HasSuffix(e.value.Path, ".go") || strings.HasSuffix(e.value.Path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), "protocol.go", e.content, 0)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				v, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, n := range v.Names {
					if i >= len(v.Values) {
						continue
					}
					lit, ok := v.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(lit.Value)
					if err == nil {
						constants[module+"/"+path.Dir(e.value.Path)+"."+n.Name] = value
					}
				}
			}
		}
	}
	for _, clause := range splitRoutingClauses(task) {
		if !routingAnalysisPattern.MatchString(clause.text) || routeNegativePrefixPattern.MatchString(clause.text) || routeNegativeSuffixPattern.MatchString(clause.text) || routingConditionalPattern.MatchString(clause.text) || routingExplanatoryPattern.MatchString(clause.text) {
			continue
		}
		values := []string{}
		httpMethods := map[string]string{}
		if route == "backend.transport.http" {
			for _, m := range regexp.MustCompile(`(?i)\b(GET|POST|PUT|PATCH|DELETE)\s+(/[^\s,;]+)`).FindAllStringSubmatch(clause.text, -1) {
				values = append(values, m[2])
				httpMethods[m[2]] = strings.ToUpper(m[1])
			}
		} else {
			values = regexp.MustCompile(`\b[a-z][a-z0-9_-]*(?:\.[a-z0-9_-]+){2,}\b`).FindAllString(clause.text, -1)
		}
		for _, e := range files {
			if e.value.Kind != "source" || !strings.HasSuffix(e.value.Path, ".go") || strings.HasSuffix(e.value.Path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), "protocol.go", e.content, 0)
			if err != nil {
				continue
			}
			imports := map[string]string{}
			for _, imp := range f.Imports {
				p, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					continue
				}
				name := path.Base(p)
				if imp.Name != nil {
					name = imp.Name.Name
				}
				imports[name] = p
			}
			found := false
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				method := sel.Sel.Name
				allowed := route == "backend.transport.http" && (method == "GET" || method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE" || method == "Handle" || method == "HandleFunc") || route == "backend.transport.message" && (method == "Subscribe" || method == "QueueSubscribe" || method == "PullSubscribe")
				if !allowed || len(call.Args) == 0 {
					return true
				}
				value := ""
				switch a := call.Args[0].(type) {
				case *ast.BasicLit:
					if a.Kind == token.STRING {
						value, _ = strconv.Unquote(a.Value)
					}
				case *ast.Ident:
					value = constants[module+"/"+path.Dir(e.value.Path)+"."+a.Name]
				case *ast.SelectorExpr:
					if recv, ok := a.X.(*ast.Ident); ok {
						if p := imports[recv.Name]; module != "" && strings.HasPrefix(p, module+"/") {
							value = constants[p+"."+a.Sel.Name]
						}
					}
				}
				for _, expected := range values {
					if value != "" && value == expected && (route != "backend.transport.http" || method == httpMethods[expected]) {
						found = true
					}
				}
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
}
