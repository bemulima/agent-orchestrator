package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

type architectureCurrentFake struct{ value domain.ArchitectureCurrent }

func (f architectureCurrentFake) Handle(context.Context) (domain.ArchitectureCurrent, error) {
	return f.value, nil
}

type architectureServiceFake struct{}

func (architectureServiceFake) Handle(context.Context, string) (domain.ArchitectureServiceDetail, error) {
	return domain.ArchitectureServiceDetail{Service: domain.ArchitectureService{ProjectID: "service"}}, nil
}

type architectureContractsFake struct{}

func (architectureContractsFake) Handle(context.Context, string) ([]domain.ArchitectureContract, error) {
	return []domain.ArchitectureContract{}, nil
}

func TestArchitectureCurrentRouteMarksCurrentProjection(t *testing.T) {
	handler := ArchitectureHandler{Current: architectureCurrentFake{value: domain.ArchitectureCurrent{Mode: domain.ArchitectureModeCurrent, Services: []domain.ArchitectureService{}, Relations: []domain.ArchitectureRelation{}, Contracts: []domain.ArchitectureContract{}}}, Service: architectureServiceFake{}, Contracts: architectureContractsFake{}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/architecture/current", nil)
	response := httptest.NewRecorder()
	handler.GetCurrent(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Body.String(); !strings.Contains(got, "\"mode\":\"CURRENT\"") {
		t.Fatalf("CURRENT metadata missing: %s", got)
	}
}
