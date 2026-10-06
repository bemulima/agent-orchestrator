package contextretrieval_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/bemulima/agent-orchestrator/internal/contextretrieval/evaluation"
)

func TestCommittedRetrievalGold(t *testing.T) {
	report, err := evaluation.EvaluateDirectory(context.Background(), filepath.Join("..", "..", "..", "test", "fixtures", "context-retrieval", "gold"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Fixtures    int                `json:"fixture_count"`
		Scenarios   int                `json:"scenario_count"`
		Adversarial int                `json:"adversarial_count"`
		Metrics     evaluation.Metrics `json:"metrics"`
	}{report.FixtureCount, report.ScenarioCount, report.AdversarialCount, report.Metrics})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(encoded))
	if report.FixtureCount < 40 || report.AdversarialCount < 10 {
		t.Fatalf("insufficient independent scenarios: fixtures=%d adversarial=%d", report.FixtureCount, report.AdversarialCount)
	}
	if !report.GatesPassed {
		for _, failure := range report.Failures {
			t.Error(failure)
		}
	}
}
