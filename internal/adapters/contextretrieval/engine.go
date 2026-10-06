package contextretrieval

import core "github.com/bemulima/agent-orchestrator/internal/contextretrieval"

// NewEngine is the local, offline composition of read-only deterministic ports.
func NewEngine() *core.Engine {
	return &core.Engine{Loader: FilesystemLoader{}, Resolvers: map[string]core.Resolver{
		"exact": ExactResolver{}, "go": GoResolver{}, "metadata": MetadataResolver{},
		"architecture": ArchitectureGraphResolver{}, "contract": ContractResolver{}, "tests": TestResolver{},
	}}
}
