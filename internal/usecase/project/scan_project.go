package project

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	"github.com/bemulima/agent-orchestrator/internal/domain/repository"
)

type ScanResult struct {
	Project  domain.Project         `json:"project"`
	Snapshot domain.ServiceSnapshot `json:"snapshot"`
	Report   domain.DiscoveryReport `json:"report"`
}

// ScanProject refreshes Git metadata and persists one immutable read-only
// discovery snapshot.
type ScanProject struct {
	Projects repository.ProjectRepository
	Sources  repository.ProjectSource
	Scanner  repository.DiscoveryScanner
}

func (uc ScanProject) Handle(ctx context.Context, projectID string) (ScanResult, error) {
	project, err := uc.Projects.Get(ctx, projectID)
	if err != nil {
		return ScanResult{}, err
	}
	if project.LocalPath == nil || strings.TrimSpace(*project.LocalPath) == "" {
		return ScanResult{}, fmt.Errorf("project has no local checkout: %w", domain.ErrInvalidStatus)
	}
	source, err := uc.Sources.Inspect(ctx, *project.LocalPath)
	if err != nil {
		_ = uc.Projects.UpdateStatus(ctx, project.ID, domain.ProjectStatusFailed)
		return ScanResult{}, err
	}
	project, snapshot, report, err := uc.handleSource(ctx, project, source)
	if err != nil {
		return ScanResult{}, err
	}
	return ScanResult{Project: project, Snapshot: snapshot, Report: report}, nil
}

func (uc ScanProject) handleSource(
	ctx context.Context,
	project domain.Project,
	source domain.RepositorySource,
) (domain.Project, domain.ServiceSnapshot, domain.DiscoveryReport, error) {
	project, err := uc.Projects.UpdateSourceState(ctx, project.ID, domain.ProjectStatusScanning, source)
	if err != nil {
		return domain.Project{}, domain.ServiceSnapshot{}, domain.DiscoveryReport{}, err
	}
	report, err := uc.Scanner.Scan(ctx, project, source)
	if err != nil {
		statusErr := uc.Projects.UpdateStatus(ctx, project.ID, domain.ProjectStatusFailed)
		if statusErr != nil && !errors.Is(statusErr, domain.ErrNotFound) {
			return domain.Project{}, domain.ServiceSnapshot{}, domain.DiscoveryReport{},
				fmt.Errorf("scan project: %v; mark failed: %w", err, statusErr)
		}
		return domain.Project{}, domain.ServiceSnapshot{}, domain.DiscoveryReport{}, err
	}
	snapshot := snapshotFromReport(project.ID, report)
	snapshot, err = uc.Projects.SaveDiscovery(ctx, project, snapshot, report)
	if err != nil {
		_ = uc.Projects.UpdateStatus(ctx, project.ID, domain.ProjectStatusFailed)
		return domain.Project{}, domain.ServiceSnapshot{}, domain.DiscoveryReport{}, err
	}
	project, err = uc.Projects.Get(ctx, project.ID)
	if err != nil {
		return domain.Project{}, domain.ServiceSnapshot{}, domain.DiscoveryReport{}, err
	}
	return project, snapshot, report, nil
}

func snapshotFromReport(projectID string, report domain.DiscoveryReport) domain.ServiceSnapshot {
	serviceKind := domain.ServiceKindUnknown
	kindEvidence := bestEvidence(report.Facts, "classification", "service_kind")
	if kindEvidence.Value != "" {
		serviceKind = domain.ServiceKind(kindEvidence.Value)
	}
	language := bestEvidence(report.Facts, "stack", "language").Value
	if language == "" {
		if evidence := evidenceByValue(report.Facts, "stack", "framework", "typescript"); evidence.Value != "" {
			language = "typescript"
		} else if evidence := bestEvidence(report.Facts, "stack", "runtime"); evidence.Value == "node" {
			language = "javascript"
		}
	}
	framework := bestFramework(report.Facts)
	purpose := bestEvidence(report.Facts, "purpose", "summary").Value
	return domain.ServiceSnapshot{
		ProjectID:       projectID,
		CommitSHA:       report.CommitSHA,
		Branch:          report.Branch,
		IsDirty:         report.IsDirty,
		ContentChecksum: report.ContentChecksum,
		ServiceKind:     serviceKind,
		Language:        language,
		Framework:       framework,
		Purpose:         purpose,
		Confidence:      kindEvidence.Confidence,
		Status:          string(domain.ProjectStatusAnalyzed),
	}
}

func bestEvidence(facts []domain.Evidence, category, name string) domain.Evidence {
	var best domain.Evidence
	for _, fact := range facts {
		if fact.Category == category && fact.Name == name && fact.Confidence > best.Confidence {
			best = fact
		}
	}
	return best
}

func evidenceByValue(facts []domain.Evidence, category, name, value string) domain.Evidence {
	for _, fact := range facts {
		if fact.Category == category && fact.Name == name && fact.Value == value {
			return fact
		}
	}
	return domain.Evidence{}
}

func bestFramework(facts []domain.Evidence) string {
	priority := []string{"nextjs", "nestjs", "gin", "echo", "chi", "express", "fastify", "playwright", "nginx", "temporal"}
	for _, framework := range priority {
		if evidenceByValue(facts, "stack", "framework", framework).Value != "" {
			return framework
		}
	}
	return bestEvidence(facts, "stack", "framework").Value
}
