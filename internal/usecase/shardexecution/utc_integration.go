package shardexecution

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

const utcIntegrationRegression = `package http_test
import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "net/url"
 "testing"
 "time"
 "canary.local/availability/internal/domain"
 httptransport "canary.local/availability/internal/transport/http"
 "canary.local/availability/internal/usecase"
)
type verifierRepository struct { calls int; query domain.AvailabilityQuery }
func(r *verifierRepository) FindAvailableIntervals(_ context.Context, query domain.AvailabilityQuery)([]domain.AvailabilityInterval,error) {
 r.calls++;r.query=query
 return []domain.AvailabilityInterval{{Start:query.Start,End:query.End}},nil
}
type verifierApplicationSpy struct { calls int; request usecase.AvailabilityRequest }
func(a *verifierApplicationSpy) GetAvailability(_ context.Context, request usecase.AvailabilityRequest)(usecase.AvailabilityResult,error) {
 a.calls++;a.request=request
 return usecase.AvailabilityResult{Intervals:[]usecase.AvailabilityInterval{{Start:request.Start,End:request.End}}},nil
}
func TestVerifierHTTPDelegationUTC(t *testing.T) {
 for _,tc:=range []struct{name,start,end string;status,calls int}{
 {"Z delegates","2030-01-02T10:00:00Z","2030-01-02T12:00:00Z",http.StatusOK,1},
 {"zero offset delegates","2030-01-02T10:00:00+00:00","2030-01-02T12:00:00+00:00",http.StatusOK,1},
 {"positive start rejected before delegation","2030-01-02T10:00:00+03:00","2030-01-02T12:00:00Z",http.StatusBadRequest,0},
 {"negative start rejected before delegation","2030-01-02T10:00:00-05:00","2030-01-02T20:00:00Z",http.StatusBadRequest,0},
 {"positive end rejected before delegation","2030-01-02T10:00:00Z","2030-01-02T18:00:00+03:00",http.StatusBadRequest,0},
 {"negative end rejected before delegation","2030-01-02T10:00:00Z","2030-01-02T12:00:00-05:00",http.StatusBadRequest,0},
 {"malformed start rejected before delegation","invalid","2030-01-02T12:00:00Z",http.StatusBadRequest,0},
 {"malformed end rejected before delegation","2030-01-02T10:00:00Z","invalid",http.StatusBadRequest,0},
 } {t.Run(tc.name,func(t *testing.T){
 application:=&verifierApplicationSpy{}
 handler:=httptransport.NewAvailabilityHandler(application)
 values:=url.Values{"resource_id":{"verifier-resource"},"start":{tc.start},"end":{tc.end}}
 response:=httptest.NewRecorder()
 handler.ServeHTTP(response,httptest.NewRequest(http.MethodGet,"/availability?"+values.Encode(),nil))
 if response.Code!=tc.status {t.Errorf("semantic RED: HTTP before-delegation status=%d want=%d body=%s",response.Code,tc.status,response.Body.String())}
 if application.calls!=tc.calls {t.Errorf("semantic RED: non-zero offset is currently delegated: application calls=%d want=%d",application.calls,tc.calls)}
 if tc.status==http.StatusOK && application.calls==1 {
 start,_:=time.Parse(time.RFC3339,tc.start);end,_:=time.Parse(time.RFC3339,tc.end)
 if application.request.ResourceID!="verifier-resource" || !application.request.Start.Equal(start) || !application.request.End.Equal(end) {t.Errorf("semantic RED: HTTP delegated query mismatch: %#v",application.request)}
 }
 })}
}
func TestVerifierRealHTTPUsecaseUTC(t *testing.T) {
 // Fix Local at a distinct zero-offset location so numeric +00:00 cannot
 // accidentally become the time.UTC singleton on a particular host.
 priorLocal:=time.Local;time.Local=time.FixedZone("verifier-zero-local",0)
 t.Cleanup(func(){time.Local=priorLocal})
 for _,tc:=range []struct{name,start,end string;status,calls int}{
 {"Z accepted","2030-01-02T10:00:00Z","2030-01-02T12:00:00Z",http.StatusOK,1},
 {"zero offset accepted","2030-01-02T10:00:00+00:00","2030-01-02T12:00:00+00:00",http.StatusOK,1},
 {"positive offset rejected","2030-01-02T10:00:00+03:00","2030-01-02T12:00:00Z",http.StatusBadRequest,0},
 {"negative offset rejected","2030-01-02T10:00:00-05:00","2030-01-02T18:00:00Z",http.StatusBadRequest,0},
 {"equal range rejected","2030-01-02T10:00:00+00:00","2030-01-02T10:00:00+00:00",http.StatusBadRequest,0},
 {"reversed range rejected","2030-01-02T12:00:00Z","2030-01-02T10:00:00Z",http.StatusBadRequest,0},
 } {t.Run(tc.name,func(t *testing.T){
 repo:=&verifierRepository{}
 application:=usecase.NewAvailabilityUsecase(repo)
 handler:=httptransport.NewAvailabilityHandler(application)
 values:=url.Values{"resource_id":{"verifier-resource"},"start":{tc.start},"end":{tc.end}}
 response:=httptest.NewRecorder()
 handler.ServeHTTP(response,httptest.NewRequest(http.MethodGet,"/availability?"+values.Encode(),nil))
 if response.Code!=tc.status {t.Errorf("semantic RED: real HTTP/usecase status=%d want=%d body=%s",response.Code,tc.status,response.Body.String())}
 if repo.calls!=tc.calls {t.Errorf("semantic RED: real HTTP/usecase repository calls=%d want=%d",repo.calls,tc.calls)}
 if tc.status==http.StatusOK && response.Code==http.StatusOK {
 var result usecase.AvailabilityResult
 if err:=json.NewDecoder(response.Body).Decode(&result);err!=nil {t.Fatal(err)}
 start,_:=time.Parse(time.RFC3339,tc.start);end,_:=time.Parse(time.RFC3339,tc.end)
 if len(result.Intervals)!=1 || !result.Intervals[0].Start.Equal(start) || !result.Intervals[0].End.Equal(end) {t.Errorf("semantic RED: successful result mismatch: %#v",result)}
 if repo.query.ResourceID!="verifier-resource" || !repo.query.Start.Equal(start) || !repo.query.End.Equal(end) {t.Errorf("semantic RED: repository query mismatch: %#v",repo.query)}
 }
 })}
}
`

func requiresUTCIntegration(prepared PreparedFanout) bool {
	if prepared.Remediation == nil {
		return false
	}
	for _, check := range prepared.Remediation.IntegrationChecks {
		if check == "UTC_HTTP_USECASE" {
			return true
		}
	}
	return false
}

func (s Service) verifyUTCIntegration(ctx context.Context, workspace domain.TaskWorkspace) (domain.WorkspaceCheckResult, error) {
	reader, _ := s.Worktrees.(frozenArtifactReader)
	if reader == nil {
		return domain.WorkspaceCheckResult{}, domain.ErrInvalidStatus
	}
	mod, err := reader.ReadArtifact(ctx, workspace, "go.mod", 64<<10)
	if err != nil {
		return domain.WorkspaceCheckResult{}, err
	}
	module := ""
	for _, line := range strings.Split(string(mod), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			module = fields[1]
			break
		}
	}
	if module != "canary.local/availability" {
		return domain.WorkspaceCheckResult{}, fmt.Errorf("UTC canary verifier is outside approved availability fixture: %w", domain.ErrConflict)
	}
	overlay, ok := s.Worktrees.(interface {
		RunVerifierGoOverlay(context.Context, domain.TaskWorkspace, []byte) (domain.WorkspaceCheckResult, error)
	})
	if !ok {
		return domain.WorkspaceCheckResult{}, fmt.Errorf("managed verification overlay unavailable: %w", domain.ErrInvalidStatus)
	}
	return overlay.RunVerifierGoOverlay(ctx, workspace, []byte(utcIntegrationRegression))
}
