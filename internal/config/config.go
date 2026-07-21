package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/kelseyhightower/envconfig"
)

const (
	ModelProfileFast     = "fast"
	ModelProfileStandard = "standard"
	ModelProfileDeep     = "deep"
	ModelProfileReview   = "review"
)

// Config holds environment configuration for the orchestrator.
type Config struct {
	HTTPPort        string `envconfig:"HTTP_PORT" default:"8080" validate:"required,numeric"`
	DatabaseURL     string `envconfig:"DATABASE_URL" default:"postgres://postgres:postgres@localhost:5432/course_dev_orchestrator?sslmode=disable" validate:"required"`
	ShutdownTimeout int    `envconfig:"SHUTDOWN_TIMEOUT" default:"10" validate:"min=1,max=120"`

	TemporalHostPort  string `envconfig:"TEMPORAL_HOST_PORT" default:"localhost:7233" validate:"required,hostname_port"`
	TemporalNamespace string `envconfig:"TEMPORAL_NAMESPACE" default:"default" validate:"required"`
	TemporalTaskQueue string `envconfig:"TEMPORAL_TASK_QUEUE" default:"course-dev-orchestrator" validate:"required"`

	RepositoryAllowedRoots []string `envconfig:"REPOSITORY_ALLOWED_ROOTS" default:"/projects" validate:"required,min=1,dive,required"`
	RepositoryStoragePath  string   `envconfig:"REPOSITORY_STORAGE_PATH" default:"/data/repositories" validate:"required"`
	WorktreeStoragePath    string   `envconfig:"WORKTREE_STORAGE_PATH" default:"/data/worktrees" validate:"required"`
	DiscoveryMaxFiles      int      `envconfig:"DISCOVERY_MAX_FILES" default:"10000" validate:"min=1,max=100000"`
	DiscoveryMaxFileBytes  int64    `envconfig:"DISCOVERY_MAX_FILE_BYTES" default:"1048576" validate:"min=1024,max=10485760"`
	DiscoveryMaxTotalBytes int64    `envconfig:"DISCOVERY_MAX_TOTAL_BYTES" default:"20971520" validate:"min=1024,max=104857600"`
	DiscoveryMaxDepth      int      `envconfig:"DISCOVERY_MAX_DEPTH" default:"24" validate:"min=1,max=100"`

	MaxTaskAttempts      int `envconfig:"MAX_TASK_ATTEMPTS" default:"3" validate:"min=1,max=3"`
	MaxReviewAttempts    int `envconfig:"MAX_REVIEW_ATTEMPTS" default:"2" validate:"min=1,max=2"`
	MaxReplans           int `envconfig:"MAX_REPLANS" default:"2" validate:"min=0,max=2"`
	MaxParallelTasks     int `envconfig:"MAX_PARALLEL_TASKS" default:"3" validate:"min=1,max=3"`
	MaxRequiredTaskDepth int `envconfig:"MAX_REQUIRED_TASK_DEPTH" default:"3" validate:"min=1,max=10"`

	GitLabBaseURL       string `envconfig:"GITLAB_BASE_URL"`
	GitLabToken         string `envconfig:"GITLAB_TOKEN"`
	GitLabWebhookSecret string `envconfig:"GITLAB_WEBHOOK_SECRET"`
	GitLabDryRun        bool   `envconfig:"GITLAB_DRY_RUN" default:"true"`

	TelegramBotToken       string  `envconfig:"TELEGRAM_BOT_TOKEN"`
	TelegramAllowedUserIDs []int64 `envconfig:"TELEGRAM_ALLOWED_USER_IDS"`
	TelegramAllowedChatIDs []int64 `envconfig:"TELEGRAM_ALLOWED_CHAT_IDS"`
	TelegramWebhookURL     string  `envconfig:"TELEGRAM_WEBHOOK_URL"`
	TelegramWebhookSecret  string  `envconfig:"TELEGRAM_WEBHOOK_SECRET"`

	CodexRunnerCommand string `envconfig:"CODEX_RUNNER_COMMAND" default:"node runner/dist/index.js" validate:"required"`
	CodexModelFast     string `envconfig:"CODEX_MODEL_FAST"`
	CodexModelStandard string `envconfig:"CODEX_MODEL_STANDARD"`
	CodexModelDeep     string `envconfig:"CODEX_MODEL_DEEP"`
	CodexModelReview   string `envconfig:"CODEX_MODEL_REVIEW"`
}

// Load reads, normalizes, and validates environment configuration.
func Load() (Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return Config{}, err
	}

	for i, root := range cfg.RepositoryAllowedRoots {
		cfg.RepositoryAllowedRoots[i] = filepath.Clean(strings.TrimSpace(root))
	}
	cfg.RepositoryStoragePath = filepath.Clean(strings.TrimSpace(cfg.RepositoryStoragePath))
	cfg.WorktreeStoragePath = filepath.Clean(strings.TrimSpace(cfg.WorktreeStoragePath))

	if err := validator.New().Struct(cfg); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	for _, path := range append(append([]string{}, cfg.RepositoryAllowedRoots...), cfg.RepositoryStoragePath, cfg.WorktreeStoragePath) {
		if !filepath.IsAbs(path) {
			return Config{}, fmt.Errorf("repository path must be absolute: %q", path)
		}
	}
	seenRoots := make(map[string]struct{}, len(cfg.RepositoryAllowedRoots))
	for _, root := range cfg.RepositoryAllowedRoots {
		if root == string(filepath.Separator) {
			return Config{}, fmt.Errorf("repository allowed root cannot be the filesystem root")
		}
		if _, exists := seenRoots[root]; exists {
			return Config{}, fmt.Errorf("duplicate repository allowed root: %q", root)
		}
		seenRoots[root] = struct{}{}
	}
	if cfg.RepositoryStoragePath == cfg.WorktreeStoragePath {
		return Config{}, fmt.Errorf("repository and worktree storage paths must be different")
	}
	return cfg, nil
}

// Model returns the configured model name for a profile. An empty value means
// the runner should use its externally configured default.
func (c Config) Model(profile string) (string, error) {
	switch profile {
	case ModelProfileFast:
		return c.CodexModelFast, nil
	case ModelProfileStandard:
		return c.CodexModelStandard, nil
	case ModelProfileDeep:
		return c.CodexModelDeep, nil
	case ModelProfileReview:
		return c.CodexModelReview, nil
	default:
		return "", fmt.Errorf("unknown model profile %q", profile)
	}
}

// Summary is a secret-free configuration view for diagnostics.
type Summary struct {
	HTTPPort                 string   `json:"http_port"`
	DatabaseConfigured       bool     `json:"database_configured"`
	TemporalHostPort         string   `json:"temporal_host_port"`
	TemporalNamespace        string   `json:"temporal_namespace"`
	TemporalTaskQueue        string   `json:"temporal_task_queue"`
	RepositoryAllowedRoots   []string `json:"repository_allowed_roots"`
	RepositoryStoragePath    string   `json:"repository_storage_path"`
	WorktreeStoragePath      string   `json:"worktree_storage_path"`
	DiscoveryMaxFiles        int      `json:"discovery_max_files"`
	DiscoveryMaxFileBytes    int64    `json:"discovery_max_file_bytes"`
	DiscoveryMaxTotalBytes   int64    `json:"discovery_max_total_bytes"`
	DiscoveryMaxDepth        int      `json:"discovery_max_depth"`
	MaxTaskAttempts          int      `json:"max_task_attempts"`
	MaxReviewAttempts        int      `json:"max_review_attempts"`
	MaxReplans               int      `json:"max_replans"`
	MaxParallelTasks         int      `json:"max_parallel_tasks"`
	MaxRequiredTaskDepth     int      `json:"max_required_task_depth"`
	GitLabConfigured         bool     `json:"gitlab_configured"`
	GitLabDryRun             bool     `json:"gitlab_dry_run"`
	TelegramConfigured       bool     `json:"telegram_configured"`
	TelegramAllowedUserCount int      `json:"telegram_allowed_user_count"`
	ConfiguredModelProfiles  []string `json:"configured_model_profiles"`
}

// SafeSummary omits all credentials and connection strings.
func (c Config) SafeSummary() Summary {
	profiles := make([]string, 0, 4)
	for _, profile := range []string{ModelProfileFast, ModelProfileStandard, ModelProfileDeep, ModelProfileReview} {
		model, _ := c.Model(profile)
		if model != "" {
			profiles = append(profiles, profile)
		}
	}
	return Summary{
		HTTPPort:                 c.HTTPPort,
		DatabaseConfigured:       c.DatabaseURL != "",
		TemporalHostPort:         c.TemporalHostPort,
		TemporalNamespace:        c.TemporalNamespace,
		TemporalTaskQueue:        c.TemporalTaskQueue,
		RepositoryAllowedRoots:   append([]string(nil), c.RepositoryAllowedRoots...),
		RepositoryStoragePath:    c.RepositoryStoragePath,
		WorktreeStoragePath:      c.WorktreeStoragePath,
		DiscoveryMaxFiles:        c.DiscoveryMaxFiles,
		DiscoveryMaxFileBytes:    c.DiscoveryMaxFileBytes,
		DiscoveryMaxTotalBytes:   c.DiscoveryMaxTotalBytes,
		DiscoveryMaxDepth:        c.DiscoveryMaxDepth,
		MaxTaskAttempts:          c.MaxTaskAttempts,
		MaxReviewAttempts:        c.MaxReviewAttempts,
		MaxReplans:               c.MaxReplans,
		MaxParallelTasks:         c.MaxParallelTasks,
		MaxRequiredTaskDepth:     c.MaxRequiredTaskDepth,
		GitLabConfigured:         c.GitLabBaseURL != "" && c.GitLabToken != "",
		GitLabDryRun:             c.GitLabDryRun,
		TelegramConfigured:       c.TelegramBotToken != "",
		TelegramAllowedUserCount: len(c.TelegramAllowedUserIDs),
		ConfiguredModelProfiles:  profiles,
	}
}
