package shardexecution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func TestIntegrationSourceChecksRejectUnsafeConstructionAndPersistenceUsecaseDependency(t *testing.T) {
	for _, tc := range []struct {
		name, imp, route, path string
		pass                   bool
	}{
		{"http application", "canary/internal/usecase", "backend.transport.http", "internal/transport/http/a.go", true},
		{"usecase repository", "canary/internal/domain", "backend.usecase", "internal/usecase/a.go", true},
		{"persistence repository", "canary/internal/domain", "backend.infrastructure.persistence", "internal/infrastructure/persistence/a.go", true},
		{"approved PostgreSQL fixture", "canary/internal/testsupport/postgresfixture", "backend.infrastructure.persistence", "internal/infrastructure/persistence/a_test.go", true},
		{"fixture forbidden in production", "canary/internal/testsupport/postgresfixture", "backend.infrastructure.persistence", "internal/infrastructure/persistence/a.go", false},
		{"persistence usecase forbidden", "canary/internal/usecase", "backend.infrastructure.persistence", "internal/infrastructure/persistence/a.go", false},
		{"unsafe test forbidden", "unsafe", "backend.transport.http", "internal/transport/http/a_test.go", false},
		{"reflect test forbidden", "reflect", "backend.usecase", "internal/usecase/a_test.go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Dir(tc.path)
			if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module canary\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, tc.path), []byte("package sample\nimport _ \""+tc.imp+"\"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			wp := domain.WorkPackage{Route: tc.route}
			wp.WriteScope.Allow = []string{dir + "/**"}
			result := checkIntegrationSourceBoundaries(root, []PreparedShard{{WorkPackage: wp}})
			if (result.ExitCode == 0) != tc.pass {
				t.Fatalf("unexpected check: %+v", result)
			}
		})
	}
}

func TestIntegrationVerificationRetryRetainsFrozenHashAndPreReviewGate(t *testing.T) {
	valid := domain.ShardFanoutExecution{State: "INTEGRATION_VERIFICATION_FAILED", AssemblyCommit: "assembled", IntegrationVerification: []byte(`{"passed":false,"frozen_hashes_passed":true}`)}
	if !integrationVerificationCanResume(valid) {
		t.Fatal("unchanged mechanically failed tree must permit verification retry")
	}
	for _, change := range []func(*domain.ShardFanoutExecution){
		func(x *domain.ShardFanoutExecution) {
			x.IntegrationVerification = []byte(`{"passed":false,"frozen_hashes_passed":false}`)
		},
		func(x *domain.ShardFanoutExecution) { x.ReviewerThreadID = "prior-review" },
		func(x *domain.ShardFanoutExecution) { x.State = "INTEGRATION_REJECTED" },
		func(x *domain.ShardFanoutExecution) { x.AssemblyCommit = "" },
	} {
		invalid := valid
		change(&invalid)
		if integrationVerificationCanResume(invalid) {
			t.Fatalf("unsafe verification retry: %+v", invalid)
		}
	}
}

func TestIntegrationReflectAllowsOnlyDirectTestDeepEqualAssertions(t *testing.T) {
	for _, tc := range []struct {
		name, filename, source string
		pass                   bool
	}{
		{"ordinary assertion", "a_test.go", `package sample;import "reflect";func compare(){if !reflect.DeepEqual(1,2){panic("different")}}`, true},
		{"aliased assertion", "a_test.go", `package sample;import r "reflect";func compare(){if !r.DeepEqual(1,2){panic("different")}}`, true},
		{"constructor bypass", "a_test.go", `package sample;import "reflect";func compare(){reflect.DeepEqual(1,2);reflect.NewAt(nil,nil)}`, false},
		{"value access", "a_test.go", `package sample;import r "reflect";func compare(){r.ValueOf(nil)}`, false},
		{"production comparison", "a.go", `package sample;import "reflect";func compare(){reflect.DeepEqual(1,2)}`, false},
		{"function assignment", "a_test.go", `package sample;import "reflect";var equal=reflect.DeepEqual;func compare(){reflect.DeepEqual(1,2)}`, false},
		{"function argument", "a_test.go", `package sample;import "reflect";func compare(){accept(reflect.DeepEqual);reflect.DeepEqual(1,2)}`, false},
		{"parenthesized value", "a_test.go", `package sample;import "reflect";func compare(){(reflect.DeepEqual)(1,2)}`, false},
		{"dot import", "a_test.go", `package sample;import . "reflect";func compare(){DeepEqual(1,2)}`, false},
		{"blank import", "a_test.go", `package sample;import _ "reflect"`, false},
		{"no actual comparison", "a_test.go", `package sample;import "reflect"`, false},
		{"alias shadowing", "a_test.go", `package sample;import r "reflect";func compare(){r.DeepEqual(1,2);r:=fake();r.DeepEqual(1,2)}`, false},
		{"unsafe alongside assertion", "a_test.go", `package sample;import("reflect";"unsafe");func compare(){reflect.DeepEqual(1,2)}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := "internal/usecase"
			if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module canary\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, dir, tc.filename), []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			wp := domain.WorkPackage{Route: "backend.usecase"}
			wp.WriteScope.Allow = []string{dir + "/**"}
			result := checkIntegrationSourceBoundaries(root, []PreparedShard{{WorkPackage: wp}})
			if (result.ExitCode == 0) != tc.pass {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}
