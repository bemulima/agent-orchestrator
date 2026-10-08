package shardexecution

import (
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"reflect"
	"testing"
)

func TestSandboxScopeSeparatesREDImplementationAndReadonlyAssessment(t *testing.T) {
	wp := domain.WorkPackage{}
	wp.WriteScope.Allow = []string{"internal/http.go", "internal/http_test.go"}
	wp.Verification.TestPaths = []string{"internal/http_test.go"}
	for _, tc := range []struct {
		phase    string
		expected []string
	}{{"boundary assessment", []string{}}, {"semantic RED test-only", []string{"internal/http_test.go"}}, {"implementation", []string{"internal/http.go"}}, {"RED_SETUP", wp.WriteScope.Allow}} {
		if got := sandboxLayerPaths(wp, tc.phase); !reflect.DeepEqual(got, tc.expected) {
			t.Fatalf("%s got %v want %v", tc.phase, got, tc.expected)
		}
	}
	composition := domain.CompositionWorkPackage{}
	composition.WriteScope.Allow = []string{"cmd/main.go", "cmd/main_test.go"}
	composition.Verification.TestPaths = []string{"cmd/main_test.go"}
	if got := sandboxCompositionPaths(composition, "wiring implementation after semantic RED"); !reflect.DeepEqual(got, []string{"cmd/main.go"}) {
		t.Fatal(got)
	}
}
