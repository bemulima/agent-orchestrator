package temporal

import (
	"testing"

	"go.temporal.io/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestLogger_WritesStructuredFields(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	logger := NewLogger(zap.New(core))
	var _ log.Logger = logger

	logger.Info("temporal event", "workflow_id", "workflow-1", "attempt", 2)

	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	context := entries[0].ContextMap()
	if context["workflow_id"] != "workflow-1" || context["attempt"] != int64(2) {
		t.Fatalf("structured context = %#v", context)
	}
}
