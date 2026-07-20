package domain

import (
	"encoding/json"
	"time"
)

type ProjectStatus string

const (
	ProjectStatusConnected ProjectStatus = "connected"
	ProjectStatusScanning  ProjectStatus = "scanning"
	ProjectStatusAnalyzed  ProjectStatus = "analyzed"
	ProjectStatusFailed    ProjectStatus = "failed"
)

// Project is a repository managed by the orchestrator.
type Project struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Status          ProjectStatus `json:"status"`
	LocalPath       *string       `json:"local_path,omitempty"`
	GitURL          *string       `json:"git_url,omitempty"`
	DefaultBranch   string        `json:"default_branch"`
	GitLabProjectID *int64        `json:"gitlab_project_id,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
}

type ServiceKind string

const (
	ServiceKindBackendService      ServiceKind = "backend_service"
	ServiceKindFrontendApplication ServiceKind = "frontend_application"
	ServiceKindGateway             ServiceKind = "gateway"
	ServiceKindInfrastructure      ServiceKind = "infrastructure"
	ServiceKindBackgroundWorker    ServiceKind = "background_worker"
	ServiceKindSharedLibrary       ServiceKind = "shared_library"
	ServiceKindAIService           ServiceKind = "ai_service"
	ServiceKindStorageService      ServiceKind = "storage_service"
	ServiceKindUnknown             ServiceKind = "unknown"
)

type ServiceSnapshot struct {
	ID           string          `json:"id"`
	ProjectID    string          `json:"project_id"`
	Version      int             `json:"version"`
	CommitSHA    string          `json:"commit_sha"`
	ServiceKind  ServiceKind     `json:"service_kind"`
	Language     string          `json:"language"`
	Framework    string          `json:"framework"`
	Purpose      string          `json:"purpose"`
	Confidence   float64         `json:"confidence"`
	DiscoveredAt time.Time       `json:"discovered_at"`
	RawReport    json.RawMessage `json:"raw_report"`
	Status       string          `json:"status"`
}

type ServiceCapability struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Source      string  `json:"source"`
}

type ServiceOwnership struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"project_id"`
	ResourceType string  `json:"resource_type"`
	ResourceName string  `json:"resource_name"`
	Confidence   float64 `json:"confidence"`
	Source       string  `json:"source"`
}

type RelationType string

const (
	RelationDependsOn            RelationType = "depends_on"
	RelationExposes              RelationType = "exposes"
	RelationConsumes             RelationType = "consumes"
	RelationPublishes            RelationType = "publishes"
	RelationSubscribes           RelationType = "subscribes"
	RelationRoutesTo             RelationType = "routes_to"
	RelationAuthenticatesThrough RelationType = "authenticates_through"
	RelationStoresIn             RelationType = "stores_in"
	RelationDeploys              RelationType = "deploys"
	RelationOwns                 RelationType = "owns"
)

type ServiceRelation struct {
	ID              string       `json:"id"`
	SourceProjectID string       `json:"source_project_id"`
	TargetProjectID string       `json:"target_project_id"`
	RelationType    RelationType `json:"relation_type"`
	ContractCode    *string      `json:"contract_code,omitempty"`
	Confidence      float64      `json:"confidence"`
	Source          string       `json:"source"`
}

type ContractType string

const (
	ContractTypeHTTP        ContractType = "http"
	ContractTypeEvent       ContractType = "event"
	ContractTypeDatabase    ContractType = "database"
	ContractTypeGraphQL     ContractType = "graphql"
	ContractTypeGRPC        ContractType = "grpc"
	ContractTypeFile        ContractType = "file"
	ContractTypeEnvironment ContractType = "environment"
)

type Contract struct {
	ID           string          `json:"id"`
	ProjectID    string          `json:"project_id"`
	Code         string          `json:"code"`
	Type         ContractType    `json:"type"`
	Version      string          `json:"version"`
	Direction    string          `json:"direction"`
	Definition   json.RawMessage `json:"definition"`
	SourcePath   string          `json:"source_path"`
	Checksum     string          `json:"checksum"`
	DiscoveredAt time.Time       `json:"discovered_at"`
}
