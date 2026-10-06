package discovery

import (
	"context"
	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// InventoryEvent contains sanitized paths and counters, never file contents.
type InventoryEvent struct {
	Kind, Path string
	Bytes      int64
}

func (s Scanner) observe(kind, path string, bytes int64) {
	if s.config.ObserveInventory != nil {
		s.config.ObserveInventory(InventoryEvent{Kind: kind, Path: path, Bytes: bytes})
	}
}

// InventoryFile is the immutable input shared by retrieval and discovery detectors.
type InventoryFile struct {
	Path    string
	Content []byte
}

// Inventory reuses the bounded discovery walker without running project detectors.
func (s Scanner) Inventory(ctx context.Context, root string) ([]InventoryFile, domain.InventorySummary, error) {
	files, summary, err := s.inventory(ctx, root)
	out := make([]InventoryFile, 0, len(files))
	for _, f := range files {
		out = append(out, InventoryFile{Path: f.path, Content: f.content})
	}
	return out, summary, err
}

// OperationsFromInventory reuses mounted HTTP and NATS/startup AST detectors.
// It is syntax evidence, not execution or a type-aware caller graph.
func OperationsFromInventory(files []InventoryFile) ([]domain.DiscoveredOperation, []domain.Evidence) {
	state := detectorState{collector: newCollector(), filesByPath: map[string][]byte{}, operationIndexes: map[string]struct{}{}}
	for _, f := range files {
		state.filesByPath[f.Path] = f.Content
	}
	scanner := NewScanner(Config{})
	scanner.extractMountedGoHTTPRoutes(&state)
	scanner.extractNonHTTPOperations(&state)
	sortOperations(state.operations)
	state.collector.sort()
	return state.operations, state.collector.conflicts
}
