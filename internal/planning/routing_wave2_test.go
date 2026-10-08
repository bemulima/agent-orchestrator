package planning

import (
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"testing"
)

func TestWave2ClassificationBoundaries(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"prerequisite UUID distinguish performance", "maintenance"},
		{"UI component", "frontend-ui"}, {"database query", "persistence"},
		{"сценарий обработки", "business-process"}, {"сообщения", "messaging"},
	} {
		if got := classifyRequest(c.text); got != c.want {
			t.Errorf("%q got %s want %s", c.text, got, c.want)
		}
	}
}

func TestWave2DeclaredApplicationShape(t *testing.T) {
	for _, c := range []struct {
		name, metadata, source string
		want                   bool
	}{
		{"declared source", "layers:\n  use_cases: internal/application/usecase\n", "package usecase\nfunc Run(){}\n", true},
		{"no declaration", "", "package usecase\nfunc Run(){}\n", false},
		{"comment declaration", "# layers.use_cases: internal/application/usecase\n", "package usecase\n", false},
		{"inbound declaration", "layers:\n  inbound: internal/application/usecase\n", "package usecase\n", false},
		{"package only", "layers:\n  usecase: internal/application/usecase\n", "package usecase\n", false},
		{"invalid Go", "layers:\n  usecase: internal/application/usecase\n", "not Go", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFiles(t, root, canonicalGoFixtureFiles(map[string]string{"go.mod": "module example.test/shape\n", ".ai/architecture.yaml": c.metadata, "internal/application/usecase/run.go": c.source}))
			inv, e := indexRepository(root, "project")
			if e != nil {
				t.Fatal(e)
			}
			r, _, e := resolveArchitectureProfile("Inspect usecase", "project", inv, plannerControlPlane(t))
			if e != nil {
				t.Fatal(e)
			}
			if (r.Status == domain.ProfileResolutionResolved) != c.want {
				t.Fatalf("resolution %+v want %v", r, c.want)
			}
		})
	}
}

func TestWave2DeclaredOutboundAdapters(t *testing.T) {
	for _, c := range []struct {
		name, metadata, source string
		want                   bool
	}{
		{"real outbound", "layers:\n  outbound: [internal/infrastructure/adapters]\n", "package adapters\nimport \"net/http\"\nfunc Call(){http.Get(\"https://example.test\")}\n", true},
		{"inbound", "layers:\n  inbound: internal/infrastructure/adapters\n", "package adapters\nimport \"net/http\"\nfunc Call(){http.Get(\"https://example.test\")}\n", false},
		{"only comment", "layers:\n  outbound: internal/infrastructure/adapters\n", "package adapters\n// http.Get(\"x\")\n", false},
		{"unrelated method", "layers:\n  outbound: internal/infrastructure/adapters\n", "package adapters\ntype Client struct{}\nfunc (Client) Do(){}\nfunc Call(){Client{}.Do()}\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := goRoutingFixture(t)
			writeFixtureFiles(t, root, map[string]string{".ai/architecture.yaml": c.metadata, "internal/infrastructure/adapters/client.go": c.source})
			r, _, _, e := buildRoutingMetadata("Trace external HTTP client request mapping", domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}}, []domain.Project{{ID: "project", LocalPath: &root}}, plannerControlPlane(t))
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, route := range r.Routes {
				for _, p := range route.Paths {
					found = found || p == "internal/infrastructure/adapters/client.go"
				}
			}
			if found != c.want {
				t.Fatalf("alias selected=%v want %v routes=%+v", found, c.want, r.Routes)
			}
		})
	}
}

func TestWave2AnalysisIntent(t *testing.T) {
	for _, c := range []struct {
		task string
		want []string
	}{
		{"Trace HTTP handler behavior, including transient storage retry.", []string{"backend.transport.http"}},
		{"Review domain invariant", []string{"backend.domain"}},
		{"Explain stable independent application before learner progress", nil},
		{"Explain that the current internal/adapters/postgres/repository.go already exists", nil},
		{"Trace current internal/adapters/postgres/repository.go as an existing dependency", nil},
		{"Explain that the current HTTP client already exists; implementation is out of scope.", nil},
		{"Explain route registration only if necessary.", nil},
		{"Do not inspect HTTP handlers; inspect PostgreSQL checkpoint adapter", []string{"backend.infrastructure.persistence"}},
		{"Trace student.diagnostic-result.committed.v1 consumer ACK, including transient storage retry.", []string{"backend.transport.message"}},
		{"Trace POST /api/items validation", []string{"backend.transport.http"}},
		{"Trace fictional.subject.v1 consumer", nil},
		{"Trace GET /api/items validation", nil},
		{"Trace internal/adapters/client/client.go only if necessary", nil},
	} {
		t.Run(c.task, func(t *testing.T) {
			root := goRoutingFixture(t)
			writeFixtureFiles(t, root, map[string]string{"internal/transport/message/consumer.go": "package message\nconst Subject=\"student.diagnostic-result.committed.v1\"\nfunc Consume(){ js.PullSubscribe(Subject) }\n", "internal/transport/http/route.go": "package http\nconst Route=\"/api/items\"\nfunc Register(){ router.POST(Route) }\n"})
			r, _, _, e := buildRoutingMetadata(c.task, domain.PlannerOutput{Tasks: []domain.PlannedTask{{ProjectID: "project"}}}, []domain.Project{{ID: "project", LocalPath: &root}}, plannerControlPlane(t))
			if e != nil {
				t.Fatal(e)
			}
			got := []string{}
			for _, x := range r.Routes {
				got = append(got, x.RouteID)
			}
			if !sameWave2Strings(got, c.want) {
				t.Fatalf("got %v want %v candidates %+v", got, c.want, r.RouteCandidates)
			}
		})
	}
}
func sameWave2Strings(a, b []string) bool {
	a = uniqueSorted(a)
	b = uniqueSorted(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
