package architecturecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"

	projection "github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	manifests "github.com/bemulima/agent-orchestrator/internal/architecturemanifest"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// FleetExport reads only the locked Git blobs. It has no persisted CURRENT or
// discovery dependency: the reviewed owner declarations select CURRENT.
type FleetExport struct {
	Resolver  immutableInventoryReader
	Inventory *InventoryRequest
	Producer  domain.ArchitectureGraphProducer
	Inputs    []byte
	Roots     map[string]string
}

func (uc FleetExport) Handle(ctx context.Context) (domain.ArchitectureGraph, error) {
	fleet, digest, err := projection.ParseFleetInputs(uc.Inputs)
	if err != nil {
		return domain.ArchitectureGraph{}, err
	}
	if uc.Resolver == nil || len(uc.Roots) != len(fleet.Repositories) {
		return domain.ArchitectureGraph{}, fmt.Errorf("exact fleet object stores required: %w", domain.ErrValidation)
	}
	sources := []domain.ArchitectureCatalogSource{}
	pins := []domain.ArchitectureGraphPin{}
	identities := []string{}
	for _, repo := range fleet.Repositories {
		root, ok := uc.Roots[repo.SourceIdentity]
		if !ok || root == "" {
			return domain.ArchitectureGraph{}, fmt.Errorf("fleet object store missing: %w", domain.ErrValidation)
		}
		source := domain.ArchitectureCatalogSource{PinnedDeclarations: true, Topology: domain.TopologySource{
			Project:  domain.Project{ID: repo.SourceIdentity, Name: repo.ServiceID, SourceIdentity: repo.SourceIdentity, RepositoryRole: repo.RepositoryRole, HeadCommit: repo.CommitSHA, Status: domain.ProjectStatusAnalyzed},
			Snapshot: domain.ServiceSnapshot{ID: repo.CommitSHA, ProjectID: repo.SourceIdentity, Status: "complete", CommitSHA: repo.CommitSHA, ContentChecksum: digest},
			Report:   domain.DiscoveryReport{SchemaVersion: 3, ProjectID: repo.SourceIdentity, CommitSHA: repo.CommitSHA, ContentChecksum: digest},
		}}
		paths := map[string]bool{}
		serviceCount := 0
		serviceDeclarationPath := ""
		for _, declaration := range repo.Declarations {
			pin, raw, e := uc.Resolver.Read(ctx, root, repo.SourceIdentity, repo.CommitSHA, declaration.Path)
			if e != nil {
				return domain.ArchitectureGraph{}, fmt.Errorf("locked declaration unavailable for %s: %w", repo.RepositoryID, domain.ErrNotFound)
			}
			sum := sha256.Sum256(raw)
			if pin.ContentSHA256 != hex.EncodeToString(sum[:]) {
				return domain.ArchitectureGraph{}, fmt.Errorf("locked content digest mismatch: %w", domain.ErrConflict)
			}
			if pin.SourceIdentity != repo.SourceIdentity || pin.CommitSHA != repo.CommitSHA || pin.Path != declaration.Path || pin.BlobOID != declaration.BlobOID || pin.ContentSHA256 != declaration.ContentSHA256 {
				return domain.ArchitectureGraph{}, fmt.Errorf("locked declaration mismatch: %w", domain.ErrConflict)
			}
			service, e := manifests.ParseService(raw)
			kind, id := "service", service.ID
			if e == nil {
				serviceCount++
				source.ServiceManifest = service
				serviceDeclarationPath = declaration.Path
			} else {
				operation, oe := manifests.ParseOperation(raw)
				if oe != nil {
					return domain.ArchitectureGraph{}, fmt.Errorf("invalid owner declaration: %w", domain.ErrValidation)
				}
				source.Operations = append(source.Operations, operation)
				kind, id = "operation", operation.ID
				paths[declaration.Path] = true
			}
			source.Topology.Report.ArchitectureManifests = append(source.Topology.Report.ArchitectureManifests, domain.ArchitectureManifestMetadata{Path: declaration.Path, Kind: kind, ID: id, Checksum: pin.ContentSHA256})
			pins = append(pins, pin)
		}
		if serviceCount != 1 || source.ServiceManifest.ID != repo.ServiceID || len(source.ServiceManifest.OperationManifests) != len(source.Operations) {
			return domain.ArchitectureGraph{}, fmt.Errorf("locked service declaration bundle mismatch: %w", domain.ErrConflict)
		}
		for _, file := range source.ServiceManifest.OperationManifests {
			// ParseService already enforces safe, relative operation paths. Resolve
			// those paths against the exact pinned service declaration directory.
			if !paths[path.Join(path.Dir(serviceDeclarationPath), file)] {
				return domain.ArchitectureGraph{}, fmt.Errorf("unlocked operation declaration: %w", domain.ErrConflict)
			}
		}
		sources = append(sources, source)
		identities = append(identities, repo.SourceIdentity)
	}
	catalog, err := (projection.Builder{}).Build(ctx, sources)
	if err != nil {
		return domain.ArchitectureGraph{}, err
	}
	inventories := []projection.VerifiedInventory{}
	if uc.Inventory != nil {
		request := uc.Inventory
		pin, raw, e := uc.Resolver.Read(ctx, request.Root, request.SourceIdentity, request.CommitSHA, request.Path)
		if e != nil {
			return domain.ArchitectureGraph{}, fmt.Errorf("fleet inventory unavailable: %w", domain.ErrNotFound)
		}
		inventory, e := projection.VerifyInventory(pin, raw, uc.Producer)
		if e != nil {
			return domain.ArchitectureGraph{}, e
		}
		inventories = append(inventories, inventory)
	}
	graph, err := projection.Export(catalog, sources, uc.Producer, pins, nil, inventories...)
	if err != nil {
		return graph, err
	}
	projection.ResolveFleetExternals(&graph, catalog)
	diagnostics := graph.Diagnostics[:0]
	for _, diagnostic := range graph.Diagnostics {
		if diagnostic.Code != "FLEET_SCOPE_UNPROVEN" || uc.Inventory != nil {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	graph.Diagnostics = diagnostics
	for _, source := range sources {
		for _, operation := range source.Operations {
			for _, interaction := range operation.ExternalInteractions {
				if projection.IsUnassertedInteraction(interaction) {
					graph.OperationSemanticDebt++
				}
			}
		}
	}
	graph.ExcludedInterfaceMetadata = projection.CountUnmatchedInterfaceMetadata(sources)
	graph.FleetInputs = &domain.ArchitectureGraphFleetInputs{SchemaVersion: fleet.SchemaVersion, ContentSHA256: digest, SourceIdentities: identities}
	graph.ContentSHA256 = ""
	canonical, err := projection.CanonicalGraphJSON(graph)
	if err != nil {
		return graph, err
	}
	sum := sha256.Sum256(canonical)
	graph.ContentSHA256 = hex.EncodeToString(sum[:])
	return graph, nil
}
