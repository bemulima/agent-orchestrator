package planning

import (
	"context"
	"encoding/json"
	"github.com/bemulima/agent-orchestrator/internal/contractbaseline"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"testing"
	"time"
)

func ownershipOutput(t *testing.T) (domain.PlannerOutput, Validator) {
	t.Helper()
	c := plannerControlPlane(t)
	o := validRoutedOutput(t, c)
	root := goRoutingFixture(t)
	r, p, _, err := buildRoutingMetadata("Implement HTTP boundary, usecase and PostgreSQL persistence. Keep route registration and composition out of scope.", o, []domain.Project{{ID: "project", LocalPath: &root}}, c)
	if err != nil {
		t.Fatal(err)
	}
	o.Routing = &r
	o.ContractPlan = &p
	return o, Validator{MaxParallelTasks: 3, MaxRequiredTaskDepth: 3, ControlPlane: c}
}
func TestContractOwnerOnlyDomainAndDependencySplit(t *testing.T) {
	o, v := ownershipOutput(t)
	if err := v.Validate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var repository domain.PlannedContractBoundary
	for _, b := range o.ContractPlan.Boundaries {
		if b.Kind == "repository-port" {
			repository = b
		}
	}
	if repository.Owner.RouteID != "backend.domain" {
		t.Fatalf("repository owner=%s, want domain without domain implementation", repository.Owner.RouteID)
	}
	for _, r := range o.Routing.Routes {
		if r.RouteID == "backend.domain" {
			t.Fatal("contract ownership selected domain implementation")
		}
	}
	ids := []string{}
	for _, r := range o.Routing.Routes {
		ids = append(ids, r.RouteID)
	}
	shards, err := PlanArchitecturalShards(ShardPlanningInput{PlanID: "plan", Task: domain.Task{ID: "task", ProjectID: "project"}, Repository: "fixture", Profile: v.ControlPlane.Profiles["go.canonical"], ProfileFingerprint: o.Routing.Profiles[0].ProfileFingerprint, Routing: *o.Routing, RouteIDs: ids, Evidence: o.Routing.EvidenceIndex, Verification: o.Routing.Verification, Now: time.Now()})
	if err != nil || len(shards) != 3 {
		t.Fatalf("shards=%d err=%v", len(shards), err)
	}
	for _, s := range shards {
		if s.RouteID == "backend.domain" {
			t.Fatal("domain shard created")
		}
	}

	ownerOnly := clonePlannerOutput(t, o)
	for i := range ownerOnly.ContractPlan.Boundaries {
		if ownerOnly.ContractPlan.Boundaries[i].Kind == "repository-port" {
			ownerOnly.ContractPlan.Boundaries[i].Owner.RouteID = "backend.usecase"
		}
	}
	originalContractSHA, _ := contractbaseline.PlanFingerprint(*o.ContractPlan)
	ownerOnlySHA, _ := contractbaseline.PlanFingerprint(*ownerOnly.ContractPlan)
	if originalContractSHA == ownerOnlySHA {
		t.Fatal("changing only ownership did not change ContractPlan fingerprint")
	}
	originalJSON, _ := json.Marshal(o)
	ownerOnlyJSON, _ := json.Marshal(ownerOnly)
	if domain.PlannerFingerprint([]byte("same-input"), originalJSON) == domain.PlannerFingerprint([]byte("same-input"), ownerOnlyJSON) {
		t.Fatal("changing only ownership did not change Plan fingerprint")
	}
	old := clonePlannerOutput(t, o)
	for i := range old.ContractPlan.Boundaries {
		b := &old.ContractPlan.Boundaries[i]
		if b.Kind == "repository-port" {
			b.Owner.RouteID = "backend.usecase"
			b.TargetPath = "internal/usecase/repository_port.go"
			b.Consumers = []domain.RouteReference{{ProjectID: "project", RouteID: "backend.infrastructure.persistence"}}
		}
	}
	if err := v.Validate(context.Background(), old); err == nil {
		t.Fatal("persistence to usecase accepted")
	}
	a, _ := contractbaseline.PlanFingerprint(*o.ContractPlan)
	b, _ := contractbaseline.PlanFingerprint(*old.ContractPlan)
	if a == b {
		t.Fatal("owner change did not change contract fingerprint")
	}
	raw, _ := json.Marshal(o)
	other, _ := json.Marshal(old)
	if domain.PlannerFingerprint(nil, raw) == domain.PlannerFingerprint(nil, other) {
		t.Fatal("owner change did not change Plan fingerprint")
	}
	bad := clonePlannerOutput(t, o)
	bad.ContractPlan.Boundaries[0].Owner.RouteID = "backend.infrastructure.client"
	if err := v.Validate(context.Background(), bad); err == nil {
		t.Fatal("arbitrary owner accepted")
	}
}

func TestContractOwnerOnlyRequiresExplicitProfileAndEvidence(t *testing.T) {
	base, v := ownershipOutput(t)
	for _, kind := range []string{"missing owner declaration", "missing domain evidence", "wrong implementer", "arbitrary owner route"} {
		t.Run(kind, func(t *testing.T) {
			o := clonePlannerOutput(t, base)
			switch kind {
			case "missing owner declaration":
				o.ContractPlan.ContractOwnerRoutes = nil
			case "missing domain evidence":
				var evidence []domain.RoutingEvidence
				for _, ev := range o.Routing.EvidenceIndex {
					if len(ev.Path) < 16 || ev.Path[:16] != "internal/domain/" {
						evidence = append(evidence, ev)
					}
				}
				o.Routing.EvidenceIndex = evidence
			case "wrong implementer":
				for i := range o.ContractPlan.Boundaries {
					if o.ContractPlan.Boundaries[i].Kind == "repository-port" {
						o.ContractPlan.Boundaries[i].Implementers = []domain.RouteReference{{ProjectID: "project", RouteID: "backend.transport.http"}}
					}
				}
			case "arbitrary owner route":
				o.ContractPlan.ContractOwnerRoutes = append(o.ContractPlan.ContractOwnerRoutes, domain.RouteReference{ProjectID: "project", RouteID: "backend.infrastructure.client"})
			}
			if err := v.Validate(context.Background(), o); err == nil {
				t.Fatal("invalid contract-owner-only plan accepted")
			}
		})
	}
}
