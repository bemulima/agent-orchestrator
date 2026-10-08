package planning

import (
	"testing"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestShardIDsAreStableAndRemainInternalToTopLevelTask(t *testing.T) {
	catalog := plannerControlPlane(t)
	profile := catalog.Profiles["go.canonical"]
	task := domain.Task{ID: "task-uuid", PlanID: "plan-uuid", ProjectID: "project-uuid", RiskLevel: domain.RiskLevelMedium}
	routing := domain.RoutingResult{
		Status: domain.RoutingStatusResolved, Confidence: "high",
		Profiles: []domain.ArchitectureProfileResolution{{ProjectID: task.ProjectID, Status: domain.ProfileResolutionResolved, ProfileID: profile.ID}},
		Routes: []domain.RoutedTarget{
			{RouteReference: domain.RouteReference{ProjectID: task.ProjectID, RouteID: "backend.usecase"}, Paths: []string{"internal/usecase/complete.go"}, EvidenceIDs: []string{"usecase-source"}},
			{RouteReference: domain.RouteReference{ProjectID: task.ProjectID, RouteID: "backend.domain"}, Paths: []string{"internal/domain/lesson.go"}, EvidenceIDs: []string{"domain-source"}},
		},
		EvidenceIndex: []domain.RoutingEvidence{
			{ID: "usecase-source", ProjectID: task.ProjectID, Kind: "source", Path: "internal/usecase/complete.go"},
			{ID: "domain-source", ProjectID: task.ProjectID, Kind: "source", Path: "internal/domain/lesson.go"},
		},
	}
	input := ShardPlanningInput{
		PlanID: task.PlanID, Task: task, Repository: "student-service", Profile: profile,
		ProfileFingerprint: "profile-fingerprint", Routing: routing, RouteIDs: []string{"backend.usecase"},
		Evidence: routing.EvidenceIndex, Now: time.Unix(100, 0).UTC(),
	}
	first, err := PlanArchitecturalShards(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PlanArchitecturalShards(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("shard selection or ID is unstable: first=%#v second=%#v", first, second)
	}
	shard := first[0]
	if shard.TaskID != task.ID || shard.ID == task.ID || shard.RouteID != "backend.usecase" || shard.RepositoryProjectID != task.ProjectID {
		t.Fatalf("shard did not preserve the repository-level Task identity: %#v", shard)
	}
	if len(shard.WriteScope.Allow) == 0 || containsString(shard.WriteScope.Allow, "internal/**") || shard.WriteScope.MaxFiles != 12 {
		t.Fatalf("shard write scope is not narrow: %#v", shard.WriteScope)
	}
	if !containsString(shard.WriteScope.Allow, "internal/usecase/complete_test.go") {
		t.Fatalf("shard did not name its exact sibling test path: %#v", shard.WriteScope)
	}
	for _, scopedPath := range append(append([]string(nil), shard.WriteScope.Allow...), shard.WriteScope.Deny...) {
		if err := validateShardScopePath(scopedPath); err != nil {
			t.Fatalf("shard scope contains a non-path pattern %q: %v", scopedPath, err)
		}
	}
	if err := validateShardScopePath("cmd/"); err != nil {
		t.Fatalf("canonical directory-prefix deny scope was rejected: %v", err)
	}
	if err := validateShardScopePath("../outside/"); err == nil {
		t.Fatal("parent traversal in a directory-prefix deny scope was accepted")
	}
	if shard.LocalIntent == "" || len(shard.Verification) == 0 || !shard.Parallel || shard.Phase != "workers" {
		t.Fatalf("shard is missing scoped intent or verification: %#v", shard)
	}
}

func TestCompositionRouteIsSerialAndNotFanoutWork(t *testing.T) {
	catalog := plannerControlPlane(t)
	profile := catalog.Profiles["go.canonical"]
	task := domain.Task{ID: "task", PlanID: "plan", ProjectID: "project", RiskLevel: domain.RiskLevelMedium}
	routing := domain.RoutingResult{
		Status:        domain.RoutingStatusResolved,
		Profiles:      []domain.ArchitectureProfileResolution{{ProjectID: "project", Status: domain.ProfileResolutionResolved, ProfileID: profile.ID}},
		Routes:        []domain.RoutedTarget{{RouteReference: domain.RouteReference{ProjectID: "project", RouteID: "backend.composition"}, Paths: []string{"cmd/service/main.go"}, EvidenceIDs: []string{"composition-source"}}},
		EvidenceIndex: []domain.RoutingEvidence{{ID: "composition-source", ProjectID: "project", Kind: "source", Path: "cmd/service/main.go"}},
	}
	shards, err := PlanArchitecturalShards(ShardPlanningInput{
		PlanID: "plan", Task: task, Repository: "repo", Profile: profile,
		ProfileFingerprint: "profile-fingerprint", Routing: routing, RouteIDs: []string{"backend.composition"},
		Evidence: routing.EvidenceIndex, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 1 || shards[0].Status != domain.ShardStatusComposition || shards[0].Parallel || shards[0].Phase != "after_workers" {
		t.Fatalf("composition was treated as an ordinary parallel shard: %#v", shards)
	}
}
