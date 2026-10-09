package planning

import (
	"errors"
	"fmt"
	"sort"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type readBoundaryOwnerConflict struct{ kind string }

func (e readBoundaryOwnerConflict) Error() string {
	return fmt.Sprintf("ARCHITECTURE_CONFLICT: boundary %s has no evidence-backed owner allowed by consumer dependencies: %s", e.kind, domain.ErrValidation)
}
func (e readBoundaryOwnerConflict) Unwrap() error { return domain.ErrValidation }
func isReadBoundaryOwnerConflict(err error) bool {
	var conflict readBoundaryOwnerConflict
	return errors.As(err, &conflict)
}

// BuildReadAnalysisRoutingMetadataWithCoverage preserves writer conflicts as
// unresolved facts. It adds source profiles for explicit read admissions only;
// neighboring profiles are never inferred business owners or writable routes.
func BuildReadAnalysisRoutingMetadataWithCoverage(task string, baseline domain.PlannerOutput, projects []domain.Project, catalog agentcontrol.Catalog) (domain.RoutingResult, domain.ContractPlan, RoutingCoverageReport, error) {
	route, contract, inventories, err := buildRoutingMetadataMode(task, baseline, projects, catalog, true)
	if err != nil {
		return route, contract, RoutingCoverageReport{SchemaVersion: "routing-coverage.v1", Status: "UNKNOWN"}, err
	}
	present := map[string]bool{}
	for _, p := range route.Profiles {
		present[p.ProjectID] = true
	}
	for _, p := range projects {
		if present[p.ID] || p.LocalPath == nil {
			continue
		}
		inventory, err := indexRepository(*p.LocalPath, p.ID)
		if err != nil {
			return domain.RoutingResult{}, domain.ContractPlan{}, RoutingCoverageReport{}, err
		}
		profile, _, err := resolveArchitectureProfile(task, p.ID, inventory, catalog)
		if err != nil {
			return domain.RoutingResult{}, domain.ContractPlan{}, RoutingCoverageReport{}, err
		}
		route.Profiles = append(route.Profiles, profile)
		inventories[p.ID] = inventory
	}
	sort.Slice(route.Profiles, func(i, j int) bool { return route.Profiles[i].ProjectID < route.Profiles[j].ProjectID })
	return route, contract, buildRoutingCoverageReport(route, contract, projects, inventories), nil
}
