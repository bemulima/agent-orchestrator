package shardexecution

import (
	"context"
	"fmt"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// RemediationRequest identifies a separately authorized correction of a rejected
// integration. It is execution metadata; approved WorkPackages are immutable.
type RemediationRequest struct {
	IntegrationChecks       []string            `json:"integration_checks,omitempty"`
	ID                      string              `json:"id"`
	OwnerDecision           string              `json:"owner_decision"`
	PlanFingerprint         string              `json:"plan_fingerprint"`
	ContractPlanFingerprint string              `json:"contract_plan_fingerprint"`
	BaselineID              string              `json:"baseline_id"`
	BaselineCommit          string              `json:"baseline_commit"`
	PriorReviewerThread     string              `json:"prior_reviewer_thread"`
	Findings                map[string][]string `json:"findings"`
}

type ShardRemediation struct {
	RequestID   string   `json:"request_id"`
	PriorCommit string   `json:"prior_commit"`
	Findings    []string `json:"findings"`
}

func validateRemediation(request RemediationRequest, inputs approvedInputs, previous domain.ShardFanoutExecution) error {
	if strings.TrimSpace(request.ID) == "" || strings.TrimSpace(request.OwnerDecision) == "" || len(request.Findings) == 0 {
		return fmt.Errorf("reviewer remediation requires owner decision and findings: %w", domain.ErrApprovalNeeded)
	}
	if previous.State != "INTEGRATION_REJECTED" || previous.ReviewerVerdict != "REJECT" || request.PriorReviewerThread == "" || request.PriorReviewerThread != previous.ReviewerThreadID {
		return fmt.Errorf("remediation requires the current rejected independent review: %w", domain.ErrInvalidStatus)
	}
	for _, check := range request.IntegrationChecks {
		if check != "UTC_HTTP_USECASE" {
			return fmt.Errorf("unknown verifier integration regression: %w", domain.ErrValidation)
		}
	}
	baseline := inputs.baseline
	if request.PlanFingerprint != inputs.plan.Fingerprint || request.ContractPlanFingerprint != baseline.ContractPlanFingerprint || request.BaselineID != baseline.ID || request.BaselineCommit != baseline.ContractBaselineCommit || previous.ContractBaselineID != baseline.ID || previous.BaselineCommit != baseline.ContractBaselineCommit {
		return fmt.Errorf("remediation approval, ContractPlan or frozen baseline changed; REPLAN_REQUIRED: %w", domain.ErrConflict)
	}
	approved := map[string]domain.ArchitecturalShard{}
	for _, shard := range inputs.shards {
		if shard.Status == domain.ShardStatusPlanned && shard.Parallel {
			approved[shard.RouteID] = shard
		}
	}
	for route, findings := range request.Findings {
		shard, ok := approved[route]
		if !ok || len(findings) == 0 {
			return fmt.Errorf("remediation finding outside approved implementation routes: %w", domain.ErrConflict)
		}
		for _, finding := range findings {
			if strings.TrimSpace(finding) == "" {
				return fmt.Errorf("empty remediation finding: %w", domain.ErrValidation)
			}
		}
		found := false
		for _, attempt := range previous.Attempts {
			if attempt.ShardID == shard.ID && attempt.Status == domain.ShardAttemptVerified && attempt.FinishedAt != nil && attempt.BaselineCommit == baseline.ContractBaselineCommit && attempt.CommitSHA != "" {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("affected shard lacks completed verified predecessor: %w", domain.ErrConflict)
		}
	}
	return nil
}

func nextPreparedAttemptNumber(prepared PreparedShard, history []domain.ShardAttempt) (int, error) {
	if prepared.Remediation == nil {
		return nextShardAttemptNumber(prepared.WorkPackage, history)
	}
	value := prepared.WorkPackage
	remediation := prepared.Remediation
	if remediation.RequestID == "" || remediation.PriorCommit == "" || len(remediation.Findings) == 0 {
		return 0, fmt.Errorf("incomplete shard remediation: %w", domain.ErrApprovalNeeded)
	}
	next := 1
	predecessorFound := false
	for _, previous := range history {
		if previous.ShardID != value.ShardID {
			continue
		}
		if previous.AttemptNumber >= next {
			next = previous.AttemptNumber + 1
		}
		if previous.FinishedAt == nil {
			return 0, fmt.Errorf("unfinished shard attempt prevents remediation: %w", domain.ErrConflict)
		}
		if previous.BaselineCommit != value.ExecutionBase.Revision {
			continue
		}
		if !sameJSON(previous.WorkPackage, value) || (previous.Status != domain.ShardAttemptFailed && previous.Status != domain.ShardAttemptVerified) {
			return 0, fmt.Errorf("remediation requires unchanged package and completed predecessor: %w", domain.ErrConflict)
		}
		if previous.CommitSHA == remediation.PriorCommit && previous.Status == domain.ShardAttemptVerified {
			predecessorFound = true
		}
	}
	if !predecessorFound {
		return 0, fmt.Errorf("remediation verified predecessor disappeared: %w", domain.ErrConflict)
	}
	return next, nil
}

func (s Service) materializeRemediationPreimage(ctx context.Context, prepared PreparedShard, project domain.Project, history []domain.ShardAttempt) (*domain.ShardRemediationPreimage, error) {
	if prepared.Remediation == nil || (prepared.WorkPackage.Route != "backend.transport.http" && prepared.WorkPackage.Route != "backend.usecase") {
		return nil, nil
	}
	var prior *domain.ShardAttempt
	for index := range history {
		if history[index].CommitSHA == prepared.Remediation.PriorCommit && history[index].ShardID == prepared.WorkPackage.ShardID {
			prior = &history[index]
		}
	}
	if prior == nil || !sameJSON(prior.WorkPackage, prepared.WorkPackage) || prior.Status != domain.ShardAttemptVerified {
		return nil, fmt.Errorf("implementation reproduction lacks matching verified predecessor: %w", domain.ErrConflict)
	}
	paths := []string{}
	for _, path := range prior.ChangedFiles {
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 || !scopeAllowsFiles(prepared.WorkPackage, paths) {
		return nil, fmt.Errorf("implementation preimage is outside approved production scope: %w", domain.ErrWriteScope)
	}
	materializer, ok := s.Worktrees.(interface {
		MaterializeVerifiedSources(context.Context, domain.Project, domain.TaskWorkspace, domain.TaskWorkspace, string, string, []string, []string) (map[string]string, error)
	})
	if !ok {
		return nil, fmt.Errorf("managed isolation cannot reproduce verified sources: %w", domain.ErrInvalidStatus)
	}
	sources, err := materializer.MaterializeVerifiedSources(ctx, project, prior.Workspace, prepared.Workspace, prior.CommitSHA, prepared.WorkPackage.ExecutionBase.Revision, prior.ChangedFiles, paths)
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	for path, source := range sources {
		hashes[path] = contentHash([]byte(source))
	}
	return &domain.ShardRemediationPreimage{RequestID: prepared.Remediation.RequestID, PriorCommit: prior.CommitSHA, BaselineCommit: prepared.WorkPackage.ExecutionBase.Revision, Sources: sources, Hashes: hashes, RecordedAt: s.now()}, nil
}
