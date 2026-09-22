package discovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestScannerResolvesMountedGoRoutersAcrossPackages(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.test/service\n",
		"router.go": `package service
import (
 "github.com/go-chi/chi/v5"
 api "example.test/service/api"
 admin "example.test/service/admin"
)
func NewRouter() chi.Router {
 root := chi.NewRouter()
 root.Get("/health", handler)
 authenticated := chi.NewRouter()
 authenticated.Mount("/", api.NewRouter())
 root.Mount("/api/v1", authenticated)
 register := func(r chi.Router) { admin.RegisterRoutes(r) }
 root.Group(register)
 root.Route("/admin/v1", register)
 root.Route("/internal", func(r chi.Router) {
   r.Group(func(inner chi.Router) {
     secured := inner.With(auth)
     secured.Post("/items", handler)
     inner.Route("/nested", func(n chi.Router) { n.Get("/items", handler) })
   })
 })
 return root
}`,
		"api/router.go": `package api
import "github.com/go-chi/chi/v5"
func NewRouter() chi.Router {
 r := chi.NewRouter()
 r.Get("/items", handler)
 return r
}`,
		"admin/router.go": `package admin
import "github.com/go-chi/chi/v5"
func RegisterRoutes(r chi.Router) {
 r.Get("/items", handler)
 r.Method("SET", "/items/{id}", handler)
}`,
	}
	report := scanGoRouteFixture(t, files)
	var got []string
	for _, op := range report.Operations {
		got = append(got, op.Method+" "+op.Path)
	}
	sort.Strings(got)
	want := []string{"GET /admin/v1/items", "GET /api/v1/items", "GET /health", "GET /internal/nested/items", "GET /items", "POST /internal/items", "SET /admin/v1/items/{id}", "SET /items/{id}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mounted inventory = %v, want %v", got, want)
	}
}

func TestScannerGoRoutesIgnoreCommentsAndKeepRootTrailingSlash(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{"router.go": `package fixture
func routes(r Router) {
 // r.Get("/not-a-route", nil)
 r.Route("/api", func(api Router) { api.Get("/", nil) })
 r.Get("/root", nil)
}`})
	var got []string
	for _, op := range report.Operations {
		got = append(got, op.Method+" "+op.Path)
	}
	sort.Strings(got)
	if want := []string{"GET /api/", "GET /root"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inventory = %v, want %v", got, want)
	}
}

func TestScannerGoServeMuxMethodPatterns(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{"handler.go": `package fixture
func (handler Handler) Router() {
 mux := http.NewServeMux()
 mux.HandleFunc("GET /internal/health", handler.health)
 mux.HandleFunc("POST /internal/jobs", handler.create)
 mux.HandleFunc("GET /internal/jobs/{id}", handler.get)
}`})
	var got []string
	for _, op := range report.Operations {
		got = append(got, op.Method+" "+op.Path)
	}
	sort.Strings(got)
	if want := []string{"GET /internal/health", "GET /internal/jobs/{id}", "POST /internal/jobs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inventory = %v, want %v", got, want)
	}
}

func scanGoRouteFixture(t *testing.T, files map[string]string) domain.DiscoveryReport {
	t.Helper()
	root := t.TempDir()
	for name, source := range files {
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := NewScanner(Config{}).Scan(context.Background(), domain.Project{ID: "fixture", Name: "fixture", RepositoryRole: domain.RepositoryRoleService}, domain.RepositorySource{LocalPath: root})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestScannerEchoGroupsResolveReceiverFieldsAndConfigDefaults(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{
		"go.mod":           "module example.test/service\n",
		"config/config.go": "package config\ntype Config struct { HTTPBasePath string `envDefault:\"/api/v1\"` }\n",
		"router.go": `package service
import (
 "example.test/service/config"
 api "example.test/service/api"
)
type Router struct { cfg *config.Config; api *api.Router }
func (r *Router) Setup(e Echo) {
 group:=e.Group(r.cfg.HTTPBasePath)
 r.api.Register(group)
 r.api.Register(e.Group("/legacy"))
}
`,
		"api/router.go": `package api
type Router struct{}
func(r *Router) Register(group Group) {
 auth:=group.Group("/auth")
 protected:=auth.Group("", middleware)
 protected.GET("/identities", handler)
 protected.POST("", handler)
}
`,
	})
	assertGoHTTPInventory(t, report, []string{"GET /api/v1/auth/identities", "GET /legacy/auth/identities", "POST /api/v1/auth", "POST /legacy/auth"})
}

func TestScannerServeMuxStripPrefixUsesHandlerMethodsWithoutRelativeDuplicates(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{
		"go.mod": "module example.test/service\n",
		"router.go": `package service
import (
 "net/http"
 api "example.test/service/api"
)
func Router() http.Handler {
 root:=http.NewServeMux()
 mux:=http.NewServeMux()
 api.Register(mux)
 root.Handle("/api/v1/",http.StripPrefix("/api/v1",mux))
 return root
}
`,
		"api/router.go": `package api
import "net/http"
type Handler struct{}
func(h *Handler) Create(w http.ResponseWriter,r *http.Request) { if r.Method!="SET" { return } }
func Register(mux *http.ServeMux) {
 h:= &Handler{}
 mux.HandleFunc("/items",h.Create)
 mux.HandleFunc("/items/",methodMux(map[string]http.HandlerFunc{http.MethodGet:h.Get,http.MethodPut:h.Update}))
 mux.HandleFunc("GET /health", health)
}
`,
	})
	assertGoHTTPInventory(t, report, []string{"GET /api/v1/health", "GET /api/v1/items/", "PUT /api/v1/items/", "SET /api/v1/items"})
}

func assertGoHTTPInventory(t *testing.T, report domain.DiscoveryReport, want []string) {
	t.Helper()
	var got []string
	for _, op := range report.Operations {
		if op.Type == domain.ArchitectureOperationHTTP {
			got = append(got, op.Method+" "+op.Path)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HTTP inventory=%v, want %v", got, want)
	}
}

func TestScannerUnknownEchoPrefixDoesNotInventRootEndpoints(t *testing.T) {
	report := scanGoRouteFixture(t, map[string]string{"router.go": `package fixture
func routes(e Echo, configuredPrefix string) {
 group:=e.Group(configuredPrefix)
 group.GET("/items", handler)
 e.GET("/health", handler)
}`})
	assertGoHTTPInventory(t, report, []string{"GET /health"})
	if len(report.Conflicts) == 0 {
		t.Fatal("unresolved mount must be explicit")
	}
}
