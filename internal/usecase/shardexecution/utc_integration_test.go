package shardexecution

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestUTCIntegrationRegressionUsesRealConstructorsAndZeroLocationProbe(t *testing.T) {
	if _, err := parser.ParseFile(token.NewFileSet(), "verifier_test.go", utcIntegrationRegression, 0); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"httptransport.NewAvailabilityHandler(application)", "usecase.NewAvailabilityUsecase(repo)", "time.FixedZone(\"verifier-zero-local\",0)", "+00:00", "+03:00", "-05:00", "repo.calls", "TestVerifierHTTPDelegationUTC", "application.calls", "positive end rejected before delegation", "negative end rejected before delegation", "malformed start rejected before delegation", "malformed end rejected before delegation", "equal range rejected", "reversed range rejected"} {
		if !strings.Contains(utcIntegrationRegression, required) {
			t.Fatalf("missing integration regression %q", required)
		}
	}
	for _, forbidden := range []string{"\"reflect\"", "\"unsafe\"", "acceptingApplication"} {
		if strings.Contains(utcIntegrationRegression, forbidden) {
			t.Fatalf("fake/unsafe path: %s", forbidden)
		}
	}
	if requiresUTCIntegration(PreparedFanout{}) {
		t.Fatal("unrequested integration regression enabled")
	}
	if !requiresUTCIntegration(PreparedFanout{Remediation: &RemediationRequest{IntegrationChecks: []string{"UTC_HTTP_USECASE"}}}) {
		t.Fatal("approved integration regression disabled")
	}
}
