package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bemulima/agent-orchestrator/internal/domain"
	architecturetargetuc "github.com/bemulima/agent-orchestrator/internal/usecase/architecturetarget"
)

// Target proposal bodies intentionally have their own bound. Unlike ordinary
// command requests, a proposal contains evidence and a structured diff input;
// it is still bounded so this API cannot be used as a document upload route.
const maxArchitectureTargetRequestBytes = 128 << 10

type architectureTargetCreateUseCase interface {
	Handle(context.Context, architecturetargetuc.CreateInput) (domain.ArchitectureTargetProposal, error)
}

type architectureTargetGetUseCase interface {
	Handle(context.Context, string) (domain.ArchitectureTargetProposal, error)
}

type architectureTargetReviseUseCase interface {
	Handle(context.Context, architecturetargetuc.ReviseInput) (domain.ArchitectureTargetProposal, error)
}

type architectureTargetSubmitUseCase interface {
	Handle(context.Context, architecturetargetuc.SubmitInput) (domain.ArchitectureTargetProposal, error)
}

type architectureTargetApproveUseCase interface {
	Handle(context.Context, architecturetargetuc.ApproveInput) (domain.ArchitectureTargetProposal, error)
}

type architectureTargetRejectUseCase interface {
	Handle(context.Context, architecturetargetuc.RejectInput) (domain.ArchitectureTargetProposal, error)
}

type architectureTargetRequestChangesUseCase interface {
	Handle(context.Context, architecturetargetuc.RequestChangesInput) (domain.ArchitectureTargetProposal, error)
}

type architectureTargetIntegrationUseCase interface {
	Handle(context.Context, architecturetargetuc.IntegrationInput) (architecturetargetuc.IntegrationResult, error)
}

type architectureTargetVerificationUseCase interface {
	Handle(architecturetargetuc.VerificationInput) (architecturetargetuc.VerificationReport, error)
}

// architectureTargetListUseCase is read-only. It is deliberately an injected
// boundary rather than a repository import, so the HTTP adapter never reaches
// around application policy or persistence to read TARGET records.
type architectureTargetListUseCase interface {
	Handle(context.Context, []domain.ArchitectureTargetStatus) ([]domain.ArchitectureTargetProposal, error)
}

// ArchitectureTargetHandler exposes the editable TARGET side of Architecture
// Control Center. CURRENT is fetched only through Current and is never
// accepted as client-supplied catalog data; target requests can therefore not
// mutate a manifest or manufacture a CURRENT projection.
type ArchitectureTargetHandler struct {
	Current        architectureCatalogCurrentUseCase
	List           architectureTargetListUseCase
	Create         architectureTargetCreateUseCase
	Get            architectureTargetGetUseCase
	Revise         architectureTargetReviseUseCase
	Submit         architectureTargetSubmitUseCase
	Approve        architectureTargetApproveUseCase
	Reject         architectureTargetRejectUseCase
	RequestChanges architectureTargetRequestChangesUseCase
	Integrate      architectureTargetIntegrationUseCase
	Verify         architectureTargetVerificationUseCase
}

type createArchitectureTargetRequest struct {
	CurrentFingerprint string                            `json:"current_fingerprint"`
	IdempotencyKey     string                            `json:"idempotency_key,omitempty"`
	Changes            []domain.ArchitectureTargetChange `json:"changes"`
}

type reviseArchitectureTargetRequest struct {
	ExpectedRevision   int                               `json:"expected_revision"`
	CurrentFingerprint string                            `json:"current_fingerprint"`
	Changes            []domain.ArchitectureTargetChange `json:"changes"`
}

type submitArchitectureTargetRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	Actor            string `json:"actor"`
	Comment          string `json:"comment,omitempty"`
}

// architectureTargetDecisionRequest pins an S6 decision to exactly the
// reviewed TARGET contents. expected_fingerprint is the proposal fingerprint,
// not the CURRENT fingerprint used when a draft is created or revised.
type architectureTargetDecisionRequest struct {
	ExpectedRevision    int    `json:"expected_revision"`
	ExpectedFingerprint string `json:"expected_fingerprint"`
	Actor               string `json:"actor"`
	Comment             string `json:"comment,omitempty"`
}

// architectureTargetIntegrationRequest is intentionally narrow: S7 only
// hands an already approved immutable TARGET into the existing planning
// pipeline. It accepts no plan body, issue content, publication flag, or
// source-manifest material.
type architectureTargetIntegrationRequest struct {
	ExpectedFingerprint string `json:"expected_fingerprint"`
	Actor               string `json:"actor"`
	Comment             string `json:"comment,omitempty"`
}

// ListTargets returns TARGET proposals only. It supports a bounded repeated
// status query to avoid accepting arbitrary filtering syntax.
func (h ArchitectureTargetHandler) ListTargets(w http.ResponseWriter, r *http.Request) {
	statuses, err := architectureTargetStatuses(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	proposals, err := h.List.Handle(r.Context(), statuses)
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": proposals})
}

// CreateTarget binds a new draft to the exact caller-supplied CURRENT
// fingerprint. The catalog itself is always loaded from the injected CURRENT
// use case and passed unchanged into application policy.
func (h ArchitectureTargetHandler) CreateTarget(w http.ResponseWriter, r *http.Request) {
	var request createArchitectureTargetRequest
	if err := decodeArchitectureTargetJSON(w, r, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if err := validateCreateArchitectureTargetRequest(request); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	catalog, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	proposal, err := h.Create.Handle(r.Context(), architecturetargetuc.CreateInput{
		Current:            catalog,
		CurrentFingerprint: strings.TrimSpace(request.CurrentFingerprint),
		IdempotencyKey:     strings.TrimSpace(request.IdempotencyKey),
		Changes:            request.Changes,
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, proposal)
}

func (h ArchitectureTargetHandler) GetTarget(w http.ResponseWriter, r *http.Request) {
	proposal, err := h.Get.Handle(r.Context(), chi.URLParam(r, "targetId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

// ReviseTarget uses an explicit expected revision and reloads the CURRENT
// catalog via the same injected boundary as create. It never trusts catalog
// data embedded in a browser request.
func (h ArchitectureTargetHandler) ReviseTarget(w http.ResponseWriter, r *http.Request) {
	var request reviseArchitectureTargetRequest
	if err := decodeArchitectureTargetJSON(w, r, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateReviseArchitectureTargetRequest(request); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	catalog, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	proposal, err := h.Revise.Handle(r.Context(), architecturetargetuc.ReviseInput{
		ProposalID:         chi.URLParam(r, "targetId"),
		ExpectedRevision:   request.ExpectedRevision,
		Current:            catalog,
		CurrentFingerprint: strings.TrimSpace(request.CurrentFingerprint),
		Changes:            request.Changes,
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (h ArchitectureTargetHandler) SubmitTarget(w http.ResponseWriter, r *http.Request) {
	var request submitArchitectureTargetRequest
	if err := decodeArchitectureTargetJSON(w, r, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateSubmitArchitectureTargetRequest(request); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	proposal, err := h.Submit.Handle(r.Context(), architecturetargetuc.SubmitInput{
		ProposalID:       chi.URLParam(r, "targetId"),
		ExpectedRevision: request.ExpectedRevision,
		Actor:            strings.TrimSpace(request.Actor),
		Comment:          strings.TrimSpace(request.Comment),
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

// ApproveTarget is an S6 lifecycle decision. It is intentionally separate
// from submit so a target's author cannot accidentally treat submission as an
// approval; the use case verifies the stored submitted revision and checksum.
func (h ArchitectureTargetHandler) ApproveTarget(w http.ResponseWriter, r *http.Request) {
	request, ok := h.decodeArchitectureTargetDecision(w, r, false)
	if !ok {
		return
	}
	proposal, err := h.Approve.Handle(r.Context(), architecturetargetuc.ApproveInput{
		ProposalID:          chi.URLParam(r, "targetId"),
		ExpectedRevision:    request.ExpectedRevision,
		ExpectedFingerprint: strings.TrimSpace(request.ExpectedFingerprint),
		Actor:               strings.TrimSpace(request.Actor),
		Comment:             strings.TrimSpace(request.Comment),
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (h ArchitectureTargetHandler) RejectTarget(w http.ResponseWriter, r *http.Request) {
	request, ok := h.decodeArchitectureTargetDecision(w, r, false)
	if !ok {
		return
	}
	proposal, err := h.Reject.Handle(r.Context(), architecturetargetuc.RejectInput{
		ProposalID:          chi.URLParam(r, "targetId"),
		ExpectedRevision:    request.ExpectedRevision,
		ExpectedFingerprint: strings.TrimSpace(request.ExpectedFingerprint),
		Actor:               strings.TrimSpace(request.Actor),
		Comment:             strings.TrimSpace(request.Comment),
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (h ArchitectureTargetHandler) RequestTargetChanges(w http.ResponseWriter, r *http.Request) {
	request, ok := h.decodeArchitectureTargetDecision(w, r, true)
	if !ok {
		return
	}
	proposal, err := h.RequestChanges.Handle(r.Context(), architecturetargetuc.RequestChangesInput{
		ProposalID:          chi.URLParam(r, "targetId"),
		ExpectedRevision:    request.ExpectedRevision,
		ExpectedFingerprint: strings.TrimSpace(request.ExpectedFingerprint),
		Actor:               strings.TrimSpace(request.Actor),
		Comment:             strings.TrimSpace(request.Comment),
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

// CreateTargetImplementationPlan prepares local work-item drafts from an
// exact approved TARGET. It obtains a fresh stored proposal before invoking
// S7; IntegrateApprovedTarget then independently rechecks approved status and
// immutable fingerprint. This endpoint has no publish or execution action.
func (h ArchitectureTargetHandler) CreateTargetImplementationPlan(w http.ResponseWriter, r *http.Request) {
	var request architectureTargetIntegrationRequest
	if err := decodeArchitectureTargetJSON(w, r, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := validateArchitectureTargetIntegrationRequest(request); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	proposal, err := h.Get.Handle(r.Context(), chi.URLParam(r, "targetId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	actor := strings.TrimSpace(request.Actor)
	result, err := h.Integrate.Handle(r.Context(), architecturetargetuc.IntegrationInput{
		Proposal:            proposal,
		ExpectedFingerprint: strings.TrimSpace(request.ExpectedFingerprint),
		Source:              domain.CommandSourceAPI,
		SourceUserID:        &actor,
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	// Keep the response deliberately explicit and read-only. In particular it
	// returns prepared issue drafts, not a publisher result or external URL.
	writeJSON(w, http.StatusCreated, map[string]any{
		"kind":                 result.Classification,
		"affected_project_ids": result.AffectedProjectIDs,
		"command":              result.Command,
		"plan":                 result.Plan,
		"issue_drafts":         result.IssueDrafts,
	})
}

// VerifyTarget compares a fresh immutable approved TARGET with a fresh
// injected CURRENT catalog. Verification is pure application policy: this
// handler cannot trigger a scan, write a manifest, or alter the proposal.
func (h ArchitectureTargetHandler) VerifyTarget(w http.ResponseWriter, r *http.Request) {
	expectedFingerprint, err := architectureTargetVerificationFingerprint(r)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	proposal, err := h.Get.Handle(r.Context(), chi.URLParam(r, "targetId"))
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	current, err := h.Current.Handle(r.Context())
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	report, err := h.Verify.Handle(architecturetargetuc.VerificationInput{
		Proposal:            proposal,
		ExpectedFingerprint: expectedFingerprint,
		Current:             current,
	})
	if err != nil {
		WriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h ArchitectureTargetHandler) decodeArchitectureTargetDecision(w http.ResponseWriter, r *http.Request, commentRequired bool) (architectureTargetDecisionRequest, bool) {
	var request architectureTargetDecisionRequest
	if err := decodeArchitectureTargetJSON(w, r, &request); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return architectureTargetDecisionRequest{}, false
	}
	if err := validateArchitectureTargetDecisionRequest(request, commentRequired); err != nil {
		WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return architectureTargetDecisionRequest{}, false
	}
	return request, true
}

func decodeArchitectureTargetJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxArchitectureTargetRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode request: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("decode request: multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode request trailing data: %w", err)
	}
	return nil
}

func validateCreateArchitectureTargetRequest(request createArchitectureTargetRequest) error {
	if !validArchitectureTargetRequestFingerprint(request.CurrentFingerprint) {
		return fmt.Errorf("current_fingerprint must be a SHA-256 hex value")
	}
	if len(strings.TrimSpace(request.IdempotencyKey)) == 0 || len(request.IdempotencyKey) > 255 {
		return fmt.Errorf("idempotency_key is required and must be at most 255 bytes")
	}
	return validateArchitectureTargetChanges(request.Changes)
}

func validateReviseArchitectureTargetRequest(request reviseArchitectureTargetRequest) error {
	if request.ExpectedRevision < 1 {
		return fmt.Errorf("expected_revision must be positive")
	}
	if !validArchitectureTargetRequestFingerprint(request.CurrentFingerprint) {
		return fmt.Errorf("current_fingerprint must be a SHA-256 hex value")
	}
	return validateArchitectureTargetChanges(request.Changes)
}

func validateSubmitArchitectureTargetRequest(request submitArchitectureTargetRequest) error {
	if request.ExpectedRevision < 1 {
		return fmt.Errorf("expected_revision must be positive")
	}
	if actor := strings.TrimSpace(request.Actor); actor == "" || len(actor) > 255 {
		return fmt.Errorf("actor is required and must be at most 255 bytes")
	}
	if len(request.Comment) > 4000 {
		return fmt.Errorf("comment must be at most 4000 bytes")
	}
	return nil
}

func validateArchitectureTargetDecisionRequest(request architectureTargetDecisionRequest, commentRequired bool) error {
	if request.ExpectedRevision < 1 {
		return fmt.Errorf("expected_revision must be positive")
	}
	if !validArchitectureTargetRequestFingerprint(request.ExpectedFingerprint) {
		return fmt.Errorf("expected_fingerprint must be a SHA-256 hex value")
	}
	if actor := strings.TrimSpace(request.Actor); actor == "" || len(actor) > 255 {
		return fmt.Errorf("actor is required and must be at most 255 bytes")
	}
	comment := strings.TrimSpace(request.Comment)
	if commentRequired && comment == "" {
		return fmt.Errorf("comment is required when requesting changes")
	}
	if len(request.Comment) > 4000 {
		return fmt.Errorf("comment must be at most 4000 bytes")
	}
	return nil
}

func validateArchitectureTargetIntegrationRequest(request architectureTargetIntegrationRequest) error {
	if !validArchitectureTargetRequestFingerprint(request.ExpectedFingerprint) {
		return fmt.Errorf("expected_fingerprint must be a SHA-256 hex value")
	}
	if actor := strings.TrimSpace(request.Actor); actor == "" || len(actor) > 255 {
		return fmt.Errorf("actor is required and must be at most 255 bytes")
	}
	if len(request.Comment) > 4000 {
		return fmt.Errorf("comment must be at most 4000 bytes")
	}
	return nil
}

func architectureTargetVerificationFingerprint(r *http.Request) (string, error) {
	query := r.URL.Query()
	for key := range query {
		if key != "expected_fingerprint" {
			return "", fmt.Errorf("unsupported query parameter %q", key)
		}
	}
	values := query["expected_fingerprint"]
	if len(values) != 1 {
		return "", fmt.Errorf("exactly one expected_fingerprint query parameter is required")
	}
	fingerprint := strings.TrimSpace(values[0])
	if !validArchitectureTargetRequestFingerprint(fingerprint) {
		return "", fmt.Errorf("expected_fingerprint must be a SHA-256 hex value")
	}
	return fingerprint, nil
}

func validateArchitectureTargetChanges(changes []domain.ArchitectureTargetChange) error {
	// Domain validation owns semantic validation. This transport limit bounds
	// JSON fan-out before that policy is invoked.
	if len(changes) == 0 || len(changes) > 128 {
		return fmt.Errorf("changes must contain between one and 128 items")
	}
	return nil
}

func validArchitectureTargetRequestFingerprint(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'f') && !(character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func architectureTargetStatuses(r *http.Request) ([]domain.ArchitectureTargetStatus, error) {
	for key := range r.URL.Query() {
		if key != "status" {
			return nil, fmt.Errorf("unsupported query parameter %q", key)
		}
	}
	values := r.URL.Query()["status"]
	if len(values) > 6 {
		return nil, fmt.Errorf("at most six status filters are allowed")
	}
	statuses := make([]domain.ArchitectureTargetStatus, 0, len(values))
	seen := make(map[domain.ArchitectureTargetStatus]struct{}, len(values))
	for _, value := range values {
		status := domain.ArchitectureTargetStatus(strings.TrimSpace(value))
		if !validArchitectureTargetRequestStatus(status) {
			return nil, fmt.Errorf("invalid target status %q", value)
		}
		if _, exists := seen[status]; exists {
			return nil, fmt.Errorf("duplicate target status %q", status)
		}
		seen[status] = struct{}{}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func validArchitectureTargetRequestStatus(status domain.ArchitectureTargetStatus) bool {
	switch status {
	case domain.ArchitectureTargetStatusDraft,
		domain.ArchitectureTargetStatusSubmitted,
		domain.ArchitectureTargetStatusApproved,
		domain.ArchitectureTargetStatusRejected,
		domain.ArchitectureTargetStatusChangesRequested,
		domain.ArchitectureTargetStatusSuperseded:
		return true
	default:
		return false
	}
}
