package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	architecturetargetuc "github.com/bemulima/agent-orchestrator/internal/usecase/architecturetarget"
)

const targetCurrentFingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type targetCatalogFake struct {
	value domain.ArchitectureCatalog
	err   error
	calls int
}

func (f *targetCatalogFake) Handle(context.Context) (domain.ArchitectureCatalog, error) {
	f.calls++
	return f.value, f.err
}

type targetCreateFake struct {
	input architecturetargetuc.CreateInput
	value domain.ArchitectureTargetProposal
	err   error
	calls int
}

func (f *targetCreateFake) Handle(_ context.Context, input architecturetargetuc.CreateInput) (domain.ArchitectureTargetProposal, error) {
	f.calls++
	f.input = input
	return f.value, f.err
}

type targetGetFake struct {
	id    string
	value domain.ArchitectureTargetProposal
	err   error
}

func (f *targetGetFake) Handle(_ context.Context, id string) (domain.ArchitectureTargetProposal, error) {
	f.id = id
	return f.value, f.err
}

type targetReviseFake struct {
	input architecturetargetuc.ReviseInput
	value domain.ArchitectureTargetProposal
	err   error
	calls int
}

func (f *targetReviseFake) Handle(_ context.Context, input architecturetargetuc.ReviseInput) (domain.ArchitectureTargetProposal, error) {
	f.calls++
	f.input = input
	return f.value, f.err
}

type targetSubmitFake struct {
	input architecturetargetuc.SubmitInput
	value domain.ArchitectureTargetProposal
	err   error
}

type targetApproveFake struct {
	input architecturetargetuc.ApproveInput
	value domain.ArchitectureTargetProposal
	err   error
}

func (f *targetApproveFake) Handle(_ context.Context, input architecturetargetuc.ApproveInput) (domain.ArchitectureTargetProposal, error) {
	f.input = input
	return f.value, f.err
}

type targetRejectFake struct {
	input architecturetargetuc.RejectInput
	value domain.ArchitectureTargetProposal
	err   error
}

func (f *targetRejectFake) Handle(_ context.Context, input architecturetargetuc.RejectInput) (domain.ArchitectureTargetProposal, error) {
	f.input = input
	return f.value, f.err
}

type targetRequestChangesFake struct {
	input architecturetargetuc.RequestChangesInput
	value domain.ArchitectureTargetProposal
	err   error
}

type targetIntegrationFake struct {
	input architecturetargetuc.IntegrationInput
	value architecturetargetuc.IntegrationResult
	err   error
	calls int
}

type targetVerificationFake struct {
	input architecturetargetuc.VerificationInput
	value architecturetargetuc.VerificationReport
	err   error
	calls int
}

func (f *targetVerificationFake) Handle(input architecturetargetuc.VerificationInput) (architecturetargetuc.VerificationReport, error) {
	f.calls++
	f.input = input
	return f.value, f.err
}

func (f *targetIntegrationFake) Handle(_ context.Context, input architecturetargetuc.IntegrationInput) (architecturetargetuc.IntegrationResult, error) {
	f.calls++
	f.input = input
	return f.value, f.err
}

func (f *targetRequestChangesFake) Handle(_ context.Context, input architecturetargetuc.RequestChangesInput) (domain.ArchitectureTargetProposal, error) {
	f.input = input
	return f.value, f.err
}

func (f *targetSubmitFake) Handle(_ context.Context, input architecturetargetuc.SubmitInput) (domain.ArchitectureTargetProposal, error) {
	f.input = input
	return f.value, f.err
}

type targetListFake struct {
	statuses []domain.ArchitectureTargetStatus
	value    []domain.ArchitectureTargetProposal
	err      error
}

func (f *targetListFake) Handle(_ context.Context, statuses []domain.ArchitectureTargetStatus) ([]domain.ArchitectureTargetProposal, error) {
	f.statuses = append([]domain.ArchitectureTargetStatus(nil), statuses...)
	return f.value, f.err
}

func TestArchitectureTargetHandlerCreatesAndRevisesOnlyFromInjectedCurrent(t *testing.T) {
	catalog := &targetCatalogFake{value: domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent, Fingerprint: targetCurrentFingerprint}}
	create := &targetCreateFake{value: domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusDraft, Revision: 1}}
	revise := &targetReviseFake{value: domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusDraft, Revision: 2}}
	handler := ArchitectureTargetHandler{Current: catalog, Create: create, Revise: revise}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets", strings.NewReader(`{"current_fingerprint":"`+targetCurrentFingerprint+`","changes":[{"id":"change-1"}]}`))
	createRequest.Header.Set("Idempotency-Key", "request-1")
	createResponse := httptest.NewRecorder()
	handler.CreateTarget(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", createResponse.Code, createResponse.Body.String())
	}
	if catalog.calls != 1 || create.calls != 1 || !reflect.DeepEqual(create.input.Current, catalog.value) || create.input.CurrentFingerprint != targetCurrentFingerprint || create.input.IdempotencyKey != "request-1" {
		t.Fatalf("create input = %#v, catalog calls = %d", create.input, catalog.calls)
	}

	reviseRequest := httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/changes", strings.NewReader(`{"expected_revision":1,"current_fingerprint":"`+targetCurrentFingerprint+`","changes":[{"id":"change-2"}]}`))
	reviseRequest = withTargetRouteParams(reviseRequest, map[string]string{"targetId": "target-1"})
	reviseResponse := httptest.NewRecorder()
	handler.ReviseTarget(reviseResponse, reviseRequest)
	if reviseResponse.Code != http.StatusOK {
		t.Fatalf("revise status = %d, body=%s", reviseResponse.Code, reviseResponse.Body.String())
	}
	if catalog.calls != 2 || revise.calls != 1 || revise.input.ProposalID != "target-1" || revise.input.ExpectedRevision != 1 || !reflect.DeepEqual(revise.input.Current, catalog.value) || revise.input.CurrentFingerprint != targetCurrentFingerprint {
		t.Fatalf("revise input = %#v, catalog calls = %d", revise.input, catalog.calls)
	}
}

func TestArchitectureTargetHandlerValidatesBoundedDTOsAndMapsDomainErrors(t *testing.T) {
	catalog := &targetCatalogFake{value: domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent, Fingerprint: targetCurrentFingerprint}}
	create := &targetCreateFake{}
	handler := ArchitectureTargetHandler{Current: catalog, Create: create}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets", strings.NewReader(`{"current_fingerprint":"`+targetCurrentFingerprint+`","idempotency_key":"key","changes":[{}],"manifest":"forbidden"}`))
	response := httptest.NewRecorder()
	handler.CreateTarget(response, request)
	if response.Code != http.StatusBadRequest || catalog.calls != 0 || create.calls != 0 || !strings.Contains(response.Body.String(), "invalid_json") {
		t.Fatalf("unknown field result = %d %s (catalog=%d create=%d)", response.Code, response.Body.String(), catalog.calls, create.calls)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets", strings.NewReader(`{"current_fingerprint":"wrong","idempotency_key":"key","changes":[{}]}`))
	response = httptest.NewRecorder()
	handler.CreateTarget(response, request)
	if response.Code != http.StatusBadRequest || catalog.calls != 0 || !strings.Contains(response.Body.String(), "validation_error") {
		t.Fatalf("bad fingerprint result = %d %s", response.Code, response.Body.String())
	}

	create.err = domain.ErrConflict
	request = httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets", strings.NewReader(`{"current_fingerprint":"`+targetCurrentFingerprint+`","idempotency_key":"key","changes":[{}]}`))
	response = httptest.NewRecorder()
	handler.CreateTarget(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "conflict") {
		t.Fatalf("domain error result = %d %s", response.Code, response.Body.String())
	}
}

func TestArchitectureTargetHandlerListsGetsAndSubmits(t *testing.T) {
	list := &targetListFake{value: []domain.ArchitectureTargetProposal{{ID: "target-1"}}}
	get := &targetGetFake{value: domain.ArchitectureTargetProposal{ID: "target-1"}}
	submit := &targetSubmitFake{value: domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusSubmitted, Revision: 2}}
	handler := ArchitectureTargetHandler{List: list, Get: get, Submit: submit}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/architecture/targets?status=draft&status=submitted", nil)
	listResponse := httptest.NewRecorder()
	handler.ListTargets(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || len(list.statuses) != 2 || list.statuses[0] != domain.ArchitectureTargetStatusDraft || list.statuses[1] != domain.ArchitectureTargetStatusSubmitted {
		t.Fatalf("list result = %d %s statuses=%v", listResponse.Code, listResponse.Body.String(), list.statuses)
	}

	getRequest := withTargetRouteParams(httptest.NewRequest(http.MethodGet, "/api/v1/architecture/targets/target-1", nil), map[string]string{"targetId": "target-1"})
	getResponse := httptest.NewRecorder()
	handler.GetTarget(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || get.id != "target-1" {
		t.Fatalf("get result = %d %s id=%q", getResponse.Code, getResponse.Body.String(), get.id)
	}

	submitRequest := withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/submit", strings.NewReader(`{"expected_revision":1,"actor":"owner","comment":"reviewed"}`)), map[string]string{"targetId": "target-1"})
	submitResponse := httptest.NewRecorder()
	handler.SubmitTarget(submitResponse, submitRequest)
	if submitResponse.Code != http.StatusOK || submit.input.ProposalID != "target-1" || submit.input.ExpectedRevision != 1 || submit.input.Actor != "owner" {
		t.Fatalf("submit result = %d %s input=%#v", submitResponse.Code, submitResponse.Body.String(), submit.input)
	}

	listRequest = httptest.NewRequest(http.MethodGet, "/api/v1/architecture/targets?status=draft&invalid=true", nil)
	listResponse = httptest.NewRecorder()
	handler.ListTargets(listResponse, listRequest)
	if listResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid list query status = %d", listResponse.Code)
	}
}

func TestArchitectureTargetHandlerS6DecisionsPinFingerprintAndRevision(t *testing.T) {
	approve := &targetApproveFake{value: domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusApproved, Revision: 3}}
	reject := &targetRejectFake{value: domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusRejected, Revision: 3}}
	changes := &targetRequestChangesFake{value: domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusChangesRequested, Revision: 3}}
	handler := ArchitectureTargetHandler{Approve: approve, Reject: reject, RequestChanges: changes}
	routeValues := map[string]string{"targetId": "target-1"}
	body := `{"expected_revision":2,"expected_fingerprint":"` + targetCurrentFingerprint + `","actor":"architecture-owner","comment":"reviewed"}`

	approveResponse := httptest.NewRecorder()
	handler.ApproveTarget(approveResponse, withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/approve", strings.NewReader(body)), routeValues))
	if approveResponse.Code != http.StatusOK || approve.input.ProposalID != "target-1" || approve.input.ExpectedRevision != 2 || approve.input.ExpectedFingerprint != targetCurrentFingerprint || approve.input.Actor != "architecture-owner" || approve.input.Comment != "reviewed" {
		t.Fatalf("approve result = %d %s input=%#v", approveResponse.Code, approveResponse.Body.String(), approve.input)
	}

	rejectResponse := httptest.NewRecorder()
	handler.RejectTarget(rejectResponse, withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/reject", strings.NewReader(body)), routeValues))
	if rejectResponse.Code != http.StatusOK || reject.input.ProposalID != "target-1" || reject.input.ExpectedRevision != 2 || reject.input.ExpectedFingerprint != targetCurrentFingerprint || reject.input.Actor != "architecture-owner" {
		t.Fatalf("reject result = %d %s input=%#v", rejectResponse.Code, rejectResponse.Body.String(), reject.input)
	}

	changesResponse := httptest.NewRecorder()
	handler.RequestTargetChanges(changesResponse, withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/request-changes", strings.NewReader(body)), routeValues))
	if changesResponse.Code != http.StatusOK || changes.input.ProposalID != "target-1" || changes.input.ExpectedRevision != 2 || changes.input.ExpectedFingerprint != targetCurrentFingerprint || changes.input.Actor != "architecture-owner" || changes.input.Comment != "reviewed" {
		t.Fatalf("request changes result = %d %s input=%#v", changesResponse.Code, changesResponse.Body.String(), changes.input)
	}
}

func TestArchitectureTargetHandlerS6DecisionDTOValidation(t *testing.T) {
	approve := &targetApproveFake{}
	changes := &targetRequestChangesFake{}
	handler := ArchitectureTargetHandler{Approve: approve, RequestChanges: changes}
	routeValues := map[string]string{"targetId": "target-1"}

	missingFingerprint := `{"expected_revision":1,"actor":"owner","comment":"approved"}`
	response := httptest.NewRecorder()
	handler.ApproveTarget(response, withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/approve", strings.NewReader(missingFingerprint)), routeValues))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "validation_error") || approve.input.ProposalID != "" {
		t.Fatalf("missing fingerprint result = %d %s input=%#v", response.Code, response.Body.String(), approve.input)
	}

	missingComment := `{"expected_revision":1,"expected_fingerprint":"` + targetCurrentFingerprint + `","actor":"owner"}`
	response = httptest.NewRecorder()
	handler.RequestTargetChanges(response, withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/request-changes", strings.NewReader(missingComment)), routeValues))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "comment is required") || changes.input.ProposalID != "" {
		t.Fatalf("missing comment result = %d %s input=%#v", response.Code, response.Body.String(), changes.input)
	}

	unknownField := `{"expected_revision":1,"expected_fingerprint":"` + targetCurrentFingerprint + `","actor":"owner","unexpected":true}`
	response = httptest.NewRecorder()
	handler.ApproveTarget(response, withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/approve", strings.NewReader(unknownField)), routeValues))
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_json") {
		t.Fatalf("unknown decision field result = %d %s", response.Code, response.Body.String())
	}
}

func TestArchitectureTargetHandlerPreparesImplementationPlanFromFreshTarget(t *testing.T) {
	get := &targetGetFake{value: domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusApproved, Fingerprint: targetCurrentFingerprint}}
	integrate := &targetIntegrationFake{value: architecturetargetuc.IntegrationResult{
		Classification:     architecturetargetuc.IntegrationClassificationProjectPlan,
		AffectedProjectIDs: []string{"service-a", "service-b"},
		Command:            domain.Command{ID: "command-1"},
		Plan:               domain.PlanBundle{Plan: domain.Plan{ID: "plan-1"}},
		IssueDrafts:        []domain.WorkItem{{ID: "draft-1"}},
	}}
	handler := ArchitectureTargetHandler{Get: get, Integrate: integrate}
	requestBody := `{"expected_fingerprint":"` + targetCurrentFingerprint + `","actor":"architecture-owner","comment":"prepare local drafts"}`
	request := withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/implementation-plan", strings.NewReader(requestBody)), map[string]string{"targetId": "target-1"})
	response := httptest.NewRecorder()
	handler.CreateTargetImplementationPlan(response, request)
	if response.Code != http.StatusCreated || get.id != "target-1" || integrate.calls != 1 {
		t.Fatalf("integration result = %d %s get=%q calls=%d", response.Code, response.Body.String(), get.id, integrate.calls)
	}
	if !reflect.DeepEqual(integrate.input.Proposal, get.value) || integrate.input.ExpectedFingerprint != targetCurrentFingerprint || integrate.input.Source != domain.CommandSourceAPI || integrate.input.SourceUserID == nil || *integrate.input.SourceUserID != "architecture-owner" {
		t.Fatalf("integration input = %#v", integrate.input)
	}
	for _, field := range []string{`"kind":"project_plan"`, `"command"`, `"plan"`, `"issue_drafts"`} {
		if !strings.Contains(response.Body.String(), field) {
			t.Fatalf("integration response misses %s: %s", field, response.Body.String())
		}
	}
}

func TestArchitectureTargetHandlerImplementationPlanValidationAndNoPublish(t *testing.T) {
	get := &targetGetFake{value: domain.ArchitectureTargetProposal{ID: "target-1"}}
	integrate := &targetIntegrationFake{}
	handler := ArchitectureTargetHandler{Get: get, Integrate: integrate}
	routeValues := map[string]string{"targetId": "target-1"}

	request := withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/implementation-plan", strings.NewReader(`{"expected_fingerprint":"`+targetCurrentFingerprint+`","actor":"","publish":true}`)), routeValues)
	response := httptest.NewRecorder()
	handler.CreateTargetImplementationPlan(response, request)
	if response.Code != http.StatusBadRequest || get.id != "" || integrate.calls != 0 || !strings.Contains(response.Body.String(), "invalid_json") {
		t.Fatalf("invalid integration request = %d %s get=%q calls=%d", response.Code, response.Body.String(), get.id, integrate.calls)
	}

	request = withTargetRouteParams(httptest.NewRequest(http.MethodPost, "/api/v1/architecture/targets/target-1/implementation-plan", strings.NewReader(`{"expected_fingerprint":"`+targetCurrentFingerprint+`","actor":"owner"}`)), routeValues)
	get.err = domain.ErrNotFound
	response = httptest.NewRecorder()
	handler.CreateTargetImplementationPlan(response, request)
	if response.Code != http.StatusNotFound || integrate.calls != 0 {
		t.Fatalf("missing target result = %d %s calls=%d", response.Code, response.Body.String(), integrate.calls)
	}
}

func TestArchitectureTargetHandlerVerifiesFreshTargetAgainstFreshCurrent(t *testing.T) {
	proposal := domain.ArchitectureTargetProposal{ID: "target-1", Status: domain.ArchitectureTargetStatusApproved, Fingerprint: targetCurrentFingerprint}
	get := &targetGetFake{value: proposal}
	current := &targetCatalogFake{value: domain.ArchitectureCatalog{Mode: domain.ArchitectureCatalogModeCurrent, Fingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	verify := &targetVerificationFake{value: architecturetargetuc.VerificationReport{Status: architecturetargetuc.VerificationStatusMatched, ProposalID: "target-1", ProposalFingerprint: targetCurrentFingerprint}}
	handler := ArchitectureTargetHandler{Get: get, Current: current, Verify: verify}
	request := withTargetRouteParams(httptest.NewRequest(http.MethodGet, "/api/v1/architecture/targets/target-1/verification?expected_fingerprint="+targetCurrentFingerprint, nil), map[string]string{"targetId": "target-1"})
	response := httptest.NewRecorder()
	handler.VerifyTarget(response, request)
	if response.Code != http.StatusOK || get.id != "target-1" || current.calls != 1 || verify.calls != 1 {
		t.Fatalf("verify result = %d %s get=%q current=%d verify=%d", response.Code, response.Body.String(), get.id, current.calls, verify.calls)
	}
	if !reflect.DeepEqual(verify.input.Proposal, proposal) || verify.input.ExpectedFingerprint != targetCurrentFingerprint || !reflect.DeepEqual(verify.input.Current, current.value) {
		t.Fatalf("verification input = %#v", verify.input)
	}
	if !strings.Contains(response.Body.String(), `"status":"MATCHED"`) {
		t.Fatalf("verification response = %s", response.Body.String())
	}
}

func TestArchitectureTargetHandlerVerificationRequiresExactBoundedFingerprintQuery(t *testing.T) {
	get := &targetGetFake{value: domain.ArchitectureTargetProposal{ID: "target-1"}}
	current := &targetCatalogFake{}
	verify := &targetVerificationFake{}
	handler := ArchitectureTargetHandler{Get: get, Current: current, Verify: verify}
	routeValues := map[string]string{"targetId": "target-1"}

	for _, path := range []string{
		"/api/v1/architecture/targets/target-1/verification",
		"/api/v1/architecture/targets/target-1/verification?expected_fingerprint=wrong",
		"/api/v1/architecture/targets/target-1/verification?expected_fingerprint=" + targetCurrentFingerprint + "&expected_fingerprint=" + targetCurrentFingerprint,
		"/api/v1/architecture/targets/target-1/verification?expected_fingerprint=" + targetCurrentFingerprint + "&scan=true",
	} {
		response := httptest.NewRecorder()
		handler.VerifyTarget(response, withTargetRouteParams(httptest.NewRequest(http.MethodGet, path, nil), routeValues))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, body=%s", path, response.Code, response.Body.String())
		}
	}
	if get.id != "" || current.calls != 0 || verify.calls != 0 {
		t.Fatalf("invalid queries must not load target/current or invoke verifier: get=%q current=%d verify=%d", get.id, current.calls, verify.calls)
	}

	get.err = domain.ErrNotFound
	request := withTargetRouteParams(httptest.NewRequest(http.MethodGet, "/api/v1/architecture/targets/target-1/verification?expected_fingerprint="+targetCurrentFingerprint, nil), routeValues)
	response := httptest.NewRecorder()
	handler.VerifyTarget(response, request)
	if response.Code != http.StatusNotFound || current.calls != 0 || verify.calls != 0 {
		t.Fatalf("missing proposal result = %d %s current=%d verify=%d", response.Code, response.Body.String(), current.calls, verify.calls)
	}
}

func TestArchitectureTargetHandlerDoesNotLeakUnexpectedError(t *testing.T) {
	handler := ArchitectureTargetHandler{Get: &targetGetFake{err: errors.New("database address")}}
	request := withTargetRouteParams(httptest.NewRequest(http.MethodGet, "/api/v1/architecture/targets/target-1", nil), map[string]string{"targetId": "target-1"})
	response := httptest.NewRecorder()
	handler.GetTarget(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "database address") {
		t.Fatalf("error result = %d %s", response.Code, response.Body.String())
	}
}

func withTargetRouteParams(request *http.Request, values map[string]string) *http.Request {
	routeContext := chi.NewRouteContext()
	for key, value := range values {
		routeContext.URLParams.Add(key, value)
	}
	return request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
}
