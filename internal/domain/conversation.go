package domain

import "time"

type ConversationScope string

const (
	ConversationScopeWorkspace ConversationScope = "workspace"
	ConversationScopeProject   ConversationScope = "project"
	ConversationScopePlan      ConversationScope = "plan"
	ConversationScopeRun       ConversationScope = "run"
	ConversationScopeTask      ConversationScope = "task"
)

type Conversation struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	ScopeType     ConversationScope `json:"scope_type"`
	ScopeID       *string           `json:"scope_id,omitempty"`
	AgentThreadID *string           `json:"agent_thread_id,omitempty"`
	MessageCount  int               `json:"message_count"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type ConversationMessageRole string

const (
	ConversationMessageOwner     ConversationMessageRole = "owner"
	ConversationMessageAssistant ConversationMessageRole = "assistant"
	ConversationMessageSystem    ConversationMessageRole = "system"
)

type ConversationMessageStatus string

const (
	ConversationMessagePending   ConversationMessageStatus = "pending"
	ConversationMessageCompleted ConversationMessageStatus = "completed"
	ConversationMessageFailed    ConversationMessageStatus = "failed"
)

type ConversationMessage struct {
	ID             string                    `json:"id"`
	ConversationID string                    `json:"conversation_id"`
	Role           ConversationMessageRole   `json:"role"`
	Status         ConversationMessageStatus `json:"status"`
	Content        string                    `json:"content"`
	References     []ResourceReference       `json:"references"`
	Error          *string                   `json:"error,omitempty"`
	CreatedAt      time.Time                 `json:"created_at"`
	CompletedAt    *time.Time                `json:"completed_at,omitempty"`
}

type ResourceReference struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Label        string `json:"label"`
}

type ActionProposalStatus string

const (
	ActionProposalPending   ActionProposalStatus = "pending"
	ActionProposalConfirmed ActionProposalStatus = "confirmed"
	ActionProposalRejected  ActionProposalStatus = "rejected"
)

type ActionProposal struct {
	ID             string               `json:"id"`
	ConversationID string               `json:"conversation_id"`
	MessageID      string               `json:"message_id"`
	Action         string               `json:"action"`
	ResourceType   string               `json:"resource_type"`
	ResourceID     string               `json:"resource_id"`
	Title          string               `json:"title"`
	Description    string               `json:"description"`
	RiskLevel      RiskLevel            `json:"risk_level"`
	Fingerprint    *string              `json:"fingerprint,omitempty"`
	Status         ActionProposalStatus `json:"status"`
	CreatedAt      time.Time            `json:"created_at"`
	DecidedAt      *time.Time           `json:"decided_at,omitempty"`
}

type ConversationDetail struct {
	Conversation Conversation          `json:"conversation"`
	Messages     []ConversationMessage `json:"messages"`
	Proposals    []ActionProposal      `json:"proposals"`
}
