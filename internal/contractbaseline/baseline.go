package contractbaseline

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bemulima/agent-orchestrator/internal/agentcontrol"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

func PlanFingerprint(plan domain.ContractPlan) (string, error) {
	plan.AffectedRoutes = sortedRouteRefs(plan.AffectedRoutes)
	for index := range plan.Boundaries {
		plan.Boundaries[index].Consumers = sortedRouteRefs(plan.Boundaries[index].Consumers)
	}
	sort.Slice(plan.Boundaries, func(i, j int) bool { return plan.Boundaries[i].Kind < plan.Boundaries[j].Kind })
	sort.Slice(plan.ChangesRequired, func(i, j int) bool {
		left, right := plan.ChangesRequired[i], plan.ChangesRequired[j]
		return left.Kind+left.Owner.ProjectID+left.Owner.RouteID+left.Consumer.ProjectID+left.Consumer.RouteID <
			right.Kind+right.Owner.ProjectID+right.Owner.RouteID+right.Consumer.ProjectID+right.Consumer.RouteID
	})
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode contract plan fingerprint: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

// BaselineID retains the original identity for the first freeze. A successor is
// bound to the retired predecessor, so retries converge on the same new ID.
func BaselineID(planID, taskID, projectID string, predecessorID ...string) string {
	parts := []string{planID, taskID, projectID}
	if len(predecessorID) > 0 && predecessorID[0] != "" {
		parts = append(parts, "replacement-v1", predecessorID[0])
	}
	hash := sha1.Sum([]byte(strings.Join(parts, "\x00")))
	bytes := hash[:16]
	bytes[6] = bytes[6]&0x0f | 0x50
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

func AggregateFilesSHA256(files []domain.ContractBaselineFile) string {
	ordered := append([]domain.ContractBaselineFile(nil), files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	hash := sha256.New()
	for _, file := range ordered {
		_, _ = hash.Write([]byte(file.Path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(file.SHA256))
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func VerifyBaseline(
	ctx context.Context,
	_ Materializer,
	root string,
	baseline domain.ContractBaseline,
	approvedPlanFingerprint string,
	plan domain.ContractPlan,
	profile agentcontrol.Profile,
	profileFingerprint, repositoryRevision string,
	now time.Time,
) domain.ContractBaseline {
	result := baseline
	result.UpdatedAt = now
	result.Validation = domain.ContractBaselineValidation{Passed: true, Reasons: []string{}}
	if strings.TrimSpace(approvedPlanFingerprint) == "" || baseline.ApprovedPlanFingerprint != approvedPlanFingerprint {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, "approved Plan fingerprint changed")
	}
	planFingerprint, err := PlanFingerprint(plan)
	if err != nil {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, err.Error())
	} else if baseline.ContractPlanFingerprint != planFingerprint {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, "contract plan fingerprint changed")
	}
	if baseline.ProfileFingerprint != profileFingerprint {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, "architecture profile fingerprint changed")
	}
	if baseline.RepositoryRevision != repositoryRevision {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, "repository base revision changed")
	}
	if baseline.AggregateSHA256 == "" || AggregateFilesSHA256(baseline.Files) != baseline.AggregateSHA256 {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, "contract baseline aggregate hash is missing or changed")
	}
	if !fullSHA(baseline.ContractBaselineCommit) || !baseline.ContractAgentPassed ||
		baseline.ContractAgentThreadID == "" || !fullSHA(baseline.ContractAgentResultSHA256) ||
		!baseline.MechanicalReviewPassed || !fullSHA(baseline.MechanicalReviewSHA256) ||
		!baseline.ContractVerificationPassed || strings.TrimSpace(baseline.ContractVerification) == "" ||
		!baseline.IndependentReviewPassed || baseline.IndependentReviewStatus != "PASS" ||
		strings.TrimSpace(baseline.IndependentReviewerID) == "" || !fullSHA(baseline.IndependentReviewSHA256) {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, "contract baseline lacks complete agent, verification, review, or commit evidence")
	}
	if validation := VerifyCommittedBaseline(ctx, root, baseline.RepositoryRevision, baseline.ContractBaselineCommit,
		profile, baseline.Files, baseline.MaterializedContracts); !validation.Passed {
		result.Validation.Passed = false
		result.Validation.Reasons = append(result.Validation.Reasons, validation.Reasons...)
	}
	if !result.Validation.Passed {
		result.ExecutionState = domain.ContractBaselineInvalidated
	}
	return result
}

func fullSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func sortedRouteRefs(values []domain.RouteReference) []domain.RouteReference {
	result := append([]domain.RouteReference(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].ProjectID != result[j].ProjectID {
			return result[i].ProjectID < result[j].ProjectID
		}
		return result[i].RouteID < result[j].RouteID
	})
	return result
}
