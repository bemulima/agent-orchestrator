package architecturecatalog

import (
	"context"

	"github.com/bemulima/agent-orchestrator/internal/architecturemanifest"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ServiceMermaid is a deterministic presentation of one manifest-backed
// service. It is intentionally generated on demand and never persisted.
type ServiceMermaid struct{ Service Service }

func (uc ServiceMermaid) Handle(ctx context.Context, projectID string) (string, error) {
	service, err := uc.Service.Handle(ctx, projectID)
	if err != nil {
		return "", err
	}
	if !service.Covered || service.Manifest == nil {
		return "", domain.ErrNotFound
	}
	operations := make([]domain.ArchitectureOperationManifest, 0)
	for _, group := range service.Groups {
		for _, operation := range group.Operations {
			operations = append(operations, operation.Manifest)
		}
	}
	for _, operation := range service.Ungrouped {
		operations = append(operations, operation.Manifest)
	}
	return architecturemanifest.ServiceMermaid(*service.Manifest, operations), nil
}

// OperationMermaid is a deterministic presentation of one operation manifest.
// It shares the same lookup path as the JSON operation endpoint.
type OperationMermaid struct{ Operation Operation }

func (uc OperationMermaid) Handle(ctx context.Context, projectID, operationID string) (string, error) {
	operation, err := uc.Operation.Handle(ctx, projectID, operationID)
	if err != nil {
		return "", err
	}
	return architecturemanifest.OperationMermaid(operation.Manifest), nil
}
