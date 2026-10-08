package planning

import (
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"strings"
	"testing"
)

// These cases distinguish explicit task responsibilities from metadata frequency
// and from lexical references to an unchanged dependency.
func TestClosureRoutingIntent(t *testing.T) {
	cases := []struct {
		name, task string
		want       []string
	}{
		{"same clause dependency", "Change usecase using an existing HTTP handler internal/transport/http/metrics.go.", []string{"backend.usecase"}},
		{"deferred SQL", "Inspect domain metric contract; require owner meaning before changing SQL or DTO.", []string{"backend.domain"}},
		{"unchanged named dependency", "Change usecase. Existing HTTP handler internal/transport/http/metrics.go remains a dependency.", []string{"backend.usecase"}},
		{"unchanged qualified dependency", "Change usecase. Existing Sessions.ApplyMetrics remains a dependency.", []string{"backend.usecase"}},
		{"existing dependency", "Add HTTP endpoint using an existing identity dependency.", []string{"backend.transport.http"}},
		{"metadata cannot erase requested layers", "Change HTTP ingestion and outgoing publisher contract.", []string{"backend.transport.http", "backend.infrastructure.messaging"}},
		{"repository interface owner", "Add repository interface method and SQL persistence implementation.", []string{"backend.domain", "backend.infrastructure.persistence"}},
		{"message handler specificity", "Change NATS message handler decoding.", []string{"backend.transport.message"}},
		{"named boundary source", "Inspect statistics consumer boundary; Sessions.ApplyMetrics accepts trusted aggregates.", []string{"backend.transport.http", "backend.usecase"}},
		{"inspect schema not execution", "Inspect database schema migration and legacy zero UUID write invariant without executing migrations.", []string{"backend.migration", "backend.infrastructure.persistence"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := goRoutingFixture(t)
			writeFixtureFiles(t, root, map[string]string{
				"AGENTS.md":                                      "backend.transport.http owns internal/transport/http.\n",
				"internal/usecase/session.go":                    "package usecase\ntype Sessions struct{}\nfunc (s *Sessions) ApplyMetrics() {}\n",
				"internal/transport/http/metrics.go":             "package http\ntype Handler struct{ Sessions interface{ApplyMetrics()} }\nfunc (h *Handler) Apply() {h.Sessions.ApplyMetrics()}\n",
				"internal/transport/message/consumer.go":         "package message\nfunc Decode(){}\n",
				"internal/infrastructure/messaging/publisher.go": "package messaging\nfunc Publish(){}\n",
				"migrations/0001.sql":                            "CREATE TABLE events (id UUID);\n",
			})
			r, _, _, err := buildRoutingMetadata(c.task, domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}}, []domain.Project{{ID: "project", LocalPath: &root}}, plannerControlPlane(t))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, route := range r.Routes {
				got = append(got, route.RouteID)
			}
			if strings.Join(uniqueSorted(got), ",") != strings.Join(uniqueSorted(c.want), ",") {
				t.Fatalf("layers %v want %v, candidates=%+v", got, c.want, r.RouteCandidates)
			}
			if r.Status != domain.RoutingStatusResolved {
				t.Fatalf("unexpected unresolved intent: %+v", r)
			}
		})
	}
}

func TestClosureTypedApplicationBoundary(t *testing.T) {
	root := goRoutingFixture(t)
	writeFixtureFiles(t, root, map[string]string{
		"internal/usecase/summary.go":        "package usecase\ntype Input struct{ID string}\ntype View struct{ID string}\ntype Summarize struct{}\nfunc (s Summarize) Handle(in Input)(View,error){return View{},nil}\n",
		"internal/transport/http/summary.go": "package http\ntype Handler struct{Summarize interface{Handle(any)(any,error)}}\nfunc (h Handler) Read(){h.Summarize.Handle(nil)}\n",
	})
	_, plan, _, err := buildRoutingMetadata("Inspect HTTP provider boundary and Summarize.Handle.", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}}, []domain.Project{{ID: "project", LocalPath: &root}}, plannerControlPlane(t))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range plan.Boundaries {
		if b.Kind == "application-command-result" {
			found = true
			if !b.Existing || b.TargetPath != "internal/usecase/summary.go" {
				t.Fatalf("typed owner boundary replaced by invented target: %+v", b)
			}
		}
	}
	if !found {
		t.Fatal("required shared boundary lost")
	}
}

func TestClosureTypedBoundaryRejectsUnrelatedSymbolsAndImports(t *testing.T) {
	for _, probe := range []struct{ name, task, consumer string }{
		{"unrelated task", "Inspect HTTP provider and Other.Handle", "package http\ntype Handler struct{Summarize interface{Handle(any)(any,error)}}\nfunc (h Handler) Read(){h.Summarize.Handle(nil)}\n"},
		{"foreign same named type", "Inspect HTTP provider and Summarize.Handle", "package http\nimport foreign \"foreign.invalid/elsewhere/internal/usecase\"\ntype Handler struct{Alias foreign.Summarize}\nfunc (h Handler) Read(){h.Alias.Handle(nil)}\n"},
		{"non-call text", "Inspect HTTP provider and Summarize.Handle", "package http\n// Summarize.Handle is mentioned here.\n"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			root := goRoutingFixture(t)
			writeFixtureFiles(t, root, map[string]string{"internal/usecase/summary.go": "package usecase\ntype Input struct{}\ntype View struct{}\ntype Summarize struct{}\nfunc (s Summarize) Handle(in Input)(View,error){return View{},nil}\n", "internal/transport/http/summary.go": probe.consumer})
			inventory, err := indexRepository(root, "project")
			if err != nil {
				t.Fatal(err)
			}
			profile := plannerControlPlane(t).Profiles["go.canonical"]
			refs := []domain.RouteReference{{ProjectID: "project", RouteID: "backend.transport.http"}, {ProjectID: "project", RouteID: "backend.usecase"}}
			if item, found := taskTypedApplicationBoundary(probe.task, findProfileRoute(profile, "backend.usecase"), refs, inventory, profile); found {
				t.Fatalf("unrelated boundary certified: %s", item.value.Path)
			}
		})
	}
}
