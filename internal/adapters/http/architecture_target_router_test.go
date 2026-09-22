package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/adapters/http/handlers"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	architecturetargetuc "github.com/bemulima/agent-orchestrator/internal/usecase/architecturetarget"
	healthuc "github.com/bemulima/agent-orchestrator/internal/usecase/health"
)

const targetRouterFingerprint = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type targetRouterCurrentFake struct{}

func (targetRouterCurrentFake) Handle(context.Context) (domain.ArchitectureCatalog, error) {
	return domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent, Fingerprint: targetRouterFingerprint}, nil
}

type targetRouterListFake struct{}

func (targetRouterListFake) Handle(context.Context, []domain.ArchitectureTargetStatus) ([]domain.ArchitectureTargetProposal, error) {
	return []domain.ArchitectureTargetProposal{}, nil
}

type targetRouterCreateFake struct{}

func (targetRouterCreateFake) Handle(context.Context, architecturetargetuc.CreateInput) (domain.ArchitectureTargetProposal, error) {
	return domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusDraft, Revision: 1}, nil
}

type targetRouterGetFake struct{}

func (targetRouterGetFake) Handle(context.Context, string) (domain.ArchitectureTargetProposal, error) {
	return domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusDraft, Revision: 1}, nil
}

type targetRouterReviseFake struct{}

func (targetRouterReviseFake) Handle(context.Context, architecturetargetuc.ReviseInput) (domain.ArchitectureTargetProposal, error) {
	return domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusDraft, Revision: 2}, nil
}

type targetRouterSubmitFake struct{}

func (targetRouterSubmitFake) Handle(context.Context, architecturetargetuc.SubmitInput) (domain.ArchitectureTargetProposal, error) {
	return domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusSubmitted, Revision: 2}, nil
}

type targetRouterApproveFake struct{}

func (targetRouterApproveFake) Handle(context.Context, architecturetargetuc.ApproveInput) (domain.ArchitectureTargetProposal, error) {
	return domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusApproved, Revision: 3}, nil
}

type targetRouterRejectFake struct{}

func (targetRouterRejectFake) Handle(context.Context, architecturetargetuc.RejectInput) (domain.ArchitectureTargetProposal, error) {
	return domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusRejected, Revision: 3}, nil
}

type targetRouterRequestChangesFake struct{}

func (targetRouterRequestChangesFake) Handle(context.Context, architecturetargetuc.RequestChangesInput) (domain.ArchitectureTargetProposal, error) {
	return domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusChangesRequested, Revision: 3}, nil
}

type targetRouterIntegrationFake struct{}

func (targetRouterIntegrationFake) Handle(context.Context, architecturetargetuc.IntegrationInput) (architecturetargetuc.IntegrationResult, error) {
	return architecturetargetuc.IntegrationResult{Classification: architecturetargetuc.IntegrationClassificationIssue, Command: domain.Command{ID: "command-1"}, Plan: domain.PlanBundle{Plan: domain.Plan{ID: "plan-1"}}, IssueDrafts: []domain.WorkItem{}}, nil
}

type targetRouterVerificationFake struct{}

func (targetRouterVerificationFake) Handle(architecturetargetuc.VerificationInput) (architecturetargetuc.VerificationReport, error) {
	return architecturetargetuc.VerificationReport{Status: architecturetargetuc.VerificationStatusNotImplemented, ProposalID: "target-1"}, nil
}

func TestRouterArchitectureTargetAPI(t *testing.T) {
	router := NewRouter(RouterDependencies{
		HealthHandler: handlers.HealthHandler{Readiness: healthuc.CheckReadiness{}},
		ArchitectureTargetHandler: &handlers.ArchitectureTargetHandler{
			Current: targetRouterCurrentFake{}, List: targetRouterListFake{}, Create: targetRouterCreateFake{}, Get: targetRouterGetFake{}, Revise: targetRouterReviseFake{}, Submit: targetRouterSubmitFake{}, Approve: targetRouterApproveFake{}, Reject: targetRouterRejectFake{}, RequestChanges: targetRouterRequestChangesFake{}, Integrate: targetRouterIntegrationFake{}, Verify: targetRouterVerificationFake{},
		},
	})
	tests := []struct {
		method string
		path   string
		body   string
		status int
	}{
		{method: http.MethodGet, path: "/api/v1/architecture/targets", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/architecture/targets", body: `{"current_fingerprint":"` + targetRouterFingerprint + `","idempotency_key":"request-1","changes":[{}]}`, status: http.StatusCreated},
		{method: http.MethodGet, path: "/api/v1/architecture/targets/target-1", status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/architecture/targets/target-1/changes", body: `{"expected_revision":1,"current_fingerprint":"` + targetRouterFingerprint + `","changes":[{}]}`, status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/architecture/targets/target-1/submit", body: `{"expected_revision":1,"actor":"owner"}`, status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/architecture/targets/target-1/approve", body: `{"expected_revision":2,"expected_fingerprint":"` + targetRouterFingerprint + `","actor":"owner","comment":"approved"}`, status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/architecture/targets/target-1/reject", body: `{"expected_revision":2,"expected_fingerprint":"` + targetRouterFingerprint + `","actor":"owner","comment":"rejected"}`, status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/architecture/targets/target-1/request-changes", body: `{"expected_revision":2,"expected_fingerprint":"` + targetRouterFingerprint + `","actor":"owner","comment":"add evidence"}`, status: http.StatusOK},
		{method: http.MethodPost, path: "/api/v1/architecture/targets/target-1/implementation-plan", body: `{"expected_fingerprint":"` + targetRouterFingerprint + `","actor":"owner","comment":"prepare"}`, status: http.StatusCreated},
		{method: http.MethodGet, path: "/api/v1/architecture/targets/target-1/verification?expected_fingerprint=" + targetRouterFingerprint, status: http.StatusOK},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d, body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/request-changes", strings.NewReader(`{"expected_revision":2,"expected_fingerprint":"`+targetRouterFingerprint+`","actor":"owner"}`)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("request changes without comment must fail, status=%d", response.Code)
	}
}
