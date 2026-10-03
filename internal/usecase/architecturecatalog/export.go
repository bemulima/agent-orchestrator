package architecturecatalog

import (
	"context"
	"fmt"

	projection "github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ImmutableDeclarationResolver reads metadata from the pinned owner commit.
type ImmutableDeclarationResolver interface {
	Resolve(context.Context, string, string, string, string) (domain.ArchitectureGraphPin, error)
}

// Export captures CURRENT without rebuilding topology or modifying sources.
type InventoryRequest struct{ Root, SourceIdentity, CommitSHA, Path string }
type immutableInventoryReader interface {
	Read(context.Context, string, string, string, string) (domain.ArchitectureGraphPin, []byte, error)
}
type Export struct {
	Current   Current
	Resolver  ImmutableDeclarationResolver
	Producer  domain.ArchitectureGraphProducer
	Inventory *InventoryRequest
}

func (uc Export) Handle(ctx context.Context) (domain.ArchitectureGraph, error) {
	if uc.Resolver == nil {
		return domain.ArchitectureGraph{}, fmt.Errorf("immutable declaration resolver missing: %w", domain.ErrValidation)
	}
	sources, err := uc.Current.Sources(ctx)
	if err != nil {
		return domain.ArchitectureGraph{}, err
	}
	catalog, err := uc.Current.Builder.Build(ctx, sources)
	if err != nil {
		return domain.ArchitectureGraph{}, err
	}
	pins := []domain.ArchitectureGraphPin{}
	diagnostics := []domain.ArchitectureGraphDiagnostic{}
	for _, source := range sources {
		project := source.Topology.Project
		reference := projection.StableReferenceID(project.SourceIdentity, source.ServiceManifest.ID)
		for _, metadata := range source.Topology.Report.ArchitectureManifests {
			if err := ctx.Err(); err != nil {
				return domain.ArchitectureGraph{}, err
			}
			if project.LocalPath == nil {
				diagnostics = append(diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: "DECLARATION_OBJECT_STORE_MISSING", ReferenceID: reference, Path: metadata.Path, Message: "Captured owner source has no configured immutable object store."})
				continue
			}
			pin, resolveErr := uc.Resolver.Resolve(ctx, *project.LocalPath, project.SourceIdentity, source.Topology.Snapshot.CommitSHA, metadata.Path)
			if resolveErr != nil {
				// Adapter errors can contain private paths; emit only a stable diagnosis.
				diagnostics = append(diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: "DECLARATION_OBJECT_UNAVAILABLE", ReferenceID: reference, Path: metadata.Path, Message: "Owner declaration could not be resolved from its exact immutable commit."})
				continue
			}
			pins = append(pins, pin)
		}
	}
	inventories := []projection.VerifiedInventory{}
	if uc.Inventory != nil {
		reader, ok := uc.Resolver.(immutableInventoryReader)
		inventoryErr := fmt.Errorf("immutable inventory reader missing")
		if ok {
			request := uc.Inventory
			pin, raw, readErr := reader.Read(ctx, request.Root, request.SourceIdentity, request.CommitSHA, request.Path)
			inventoryErr = readErr
			if readErr == nil {
				verified, verifyErr := projection.VerifyInventory(pin, raw, uc.Producer)
				inventoryErr = verifyErr
				if verifyErr == nil {
					inventories = append(inventories, verified)
				}
			}
		}
		if inventoryErr != nil {
			diagnostics = append(diagnostics, domain.ArchitectureGraphDiagnostic{Severity: "BLOCKED", Code: "FLEET_INVENTORY_UNVERIFIED", Message: "CDO owner inventory could not be independently resolved and verified at the producer commit."})
		}
	}
	return projection.Export(catalog, sources, uc.Producer, pins, diagnostics, inventories...)
}
