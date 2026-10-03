package architecturecatalog

import (
	"context"
	"errors"
	projection "github.com/bemulima/agent-orchestrator/internal/architecturecatalog"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"strings"
	"testing"
)

type unavailableGraphResolver struct{}

func (unavailableGraphResolver) Resolve(context.Context, string, string, string, string) (domain.ArchitectureGraphPin, error) {
	return domain.ArchitectureGraphPin{}, errors.New("private error path must not leak")
}
func TestExportUnavailableOwnerObjectsRemainDiagnostic(t *testing.T) {
	project, snapshot, report := catalogFixture("teacher", "snapshot")
	root := "fixture-object-store"
	project.LocalPath = &root
	sha := strings.Repeat("a", 40)
	project.HeadCommit = sha
	snapshot.CommitSHA = sha
	report.CommitSHA = sha
	report.ArchitectureManifests = []domain.ArchitectureManifestMetadata{{Path: ".ai/architecture/service.yaml", ID: report.ArchitectureService.ID, Kind: "service", Checksum: strings.Repeat("b", 64)}}
	current := Current{Topology: topologyRepositoryFake{catalog: domain.TopologyCatalog{Services: []domain.TopologyService{{ProjectID: project.ID, SnapshotID: snapshot.ID}}}}, Projects: catalogProjectReaderFake{projects: map[string]domain.Project{project.ID: project}, snapshots: map[string]domain.ServiceSnapshot{project.ID: snapshot}, reports: map[string]domain.DiscoveryReport{project.ID: report}}, Builder: projection.Builder{}}
	graph, err := (Export{Current: current, Resolver: unavailableGraphResolver{}, Producer: domain.ArchitectureGraphProducer{RepositoryID: "bemulima/agent-orchestrator", CommitSHA: sha}}).Handle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diagnostic := range graph.Diagnostics {
		if strings.Contains(diagnostic.Message, "private") {
			t.Fatal("adapter error leaked")
		}
		if diagnostic.Code == "DECLARATION_OBJECT_UNAVAILABLE" {
			found = true
		}
	}
	if !found || len(graph.References[0].DeclarationPins) != 0 {
		t.Fatal("unavailable owner object became evidence")
	}
}
