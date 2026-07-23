package domain

type SemanticFact struct {
	Category      string  `json:"category"`
	Name          string  `json:"name"`
	Value         string  `json:"value"`
	Confidence    float64 `json:"confidence"`
	SourcePath    string  `json:"source_path"`
	EvidenceQuote string  `json:"evidence_quote"`
	Explanation   string  `json:"explanation"`
}

type SemanticOpenQuestion struct {
	Question    string   `json:"question"`
	Reason      string   `json:"reason"`
	SourcePaths []string `json:"source_paths"`
}

type SemanticRejectedFact struct {
	Category   string `json:"category"`
	Name       string `json:"name"`
	SourcePath string `json:"source_path,omitempty"`
	Reason     string `json:"reason"`
}

type SemanticAnalysis struct {
	SchemaVersion int                    `json:"schema_version"`
	ProjectID     string                 `json:"project_id"`
	ProjectName   string                 `json:"project_name"`
	BaseCommit    string                 `json:"base_commit"`
	Summary       string                 `json:"summary"`
	Facts         []SemanticFact         `json:"facts"`
	RejectedFacts []SemanticRejectedFact `json:"rejected_facts,omitempty"`
	OpenQuestions []SemanticOpenQuestion `json:"open_questions"`
}

type SemanticProposalMetadata struct {
	AgentThreadID string           `json:"agent_thread_id"`
	ModelProfile  string           `json:"model_profile"`
	Analysis      SemanticAnalysis `json:"analysis"`
}
