package architecture

import (
	"strings"
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestCurrentProjectsOnlyActualUniqueRelationsAndUnknowns(t *testing.T) {
	code := "GET /v1/students"
	catalog := domain.TopologyCatalog{Revision: domain.TopologyRevision{ID: "r1", Fingerprint: "f1"}, Services: []domain.TopologyService{{ProjectID: "a", Name: "alpha", Purpose: "", SnapshotID: "s1"}, {ProjectID: "b", Name: "beta", SnapshotID: "s2"}}, Contracts: []domain.Contract{{ID: "c1", ProjectID: "a", Code: code, Type: domain.ContractTypeHTTP, Direction: domain.ContractDirectionProvides, Definition: []byte(`{"method":"GET","path":"/v1/students"}`), SourcePath: "api.go"}, {ID: "c2", ProjectID: "b", Code: code, Type: domain.ContractTypeHTTP, Direction: domain.ContractDirectionConsumes, SourcePath: "client.go"}}, Relations: []domain.ServiceRelation{{ID: "r", SourceProjectID: "b", TargetProjectID: "a", RelationType: domain.RelationConsumes, ContractCode: &code, Source: "client.go", Confidence: .9}, {ID: "duplicate", SourceProjectID: "b", TargetProjectID: "a", RelationType: domain.RelationConsumes, ContractCode: &code, Source: "client.go", Confidence: .9}}, Drifts: []domain.ContractDrift{{ID: "d", ProducerProjectID: ptr("a"), ConsumerProjectID: ptr("b"), ContractCode: code, Severity: domain.DriftSeverityWarning}}}
	current := Projector{Now: func() time.Time { return time.Unix(1, 0) }}.Current(catalog, true)
	if current.Mode != domain.ArchitectureModeCurrent || !current.TopologyStale || len(current.Relations) != 1 {
		t.Fatalf("unexpected projection: %#v", current)
	}
	if current.Services[0].Purpose != "" {
		t.Fatal("unknown purpose must remain unknown")
	}
	if current.Contracts[0].SchemaDiscovered {
		t.Fatal("payload schema must not be invented")
	}
	if current.Contracts[0].Method != "GET" || current.Contracts[0].Path != "/v1/students" {
		t.Fatal("known HTTP operation facts were lost")
	}
	if current.Services[0].DriftStatus != "warning" {
		t.Fatal("existing drift was not projected")
	}
}

func TestCurrentEmptyAndMermaidAreDeterministic(t *testing.T) {
	current := Projector{Now: func() time.Time { return time.Unix(2, 0) }}.Current(domain.TopologyCatalog{Revision: domain.TopologyRevision{ID: "empty"}}, false)
	if len(current.Services) != 0 || len(current.Relations) != 0 {
		t.Fatal("empty topology must remain empty")
	}
	first, second := SystemMermaid(current), SystemMermaid(current)
	if first != second || !strings.HasPrefix(first, "%% GENERATED FILE — DO NOT EDIT MANUALLY") {
		t.Fatalf("unexpected Mermaid: %q", first)
	}
}
func ptr(value string) *string { return &value }
