package config

import (
	"encoding/base64"
	"reflect"
	"testing"
)

func TestLoad_NormalizesAndValidatesRepositoryPaths(t *testing.T) {
	t.Setenv("HTTP_PORT", "8090")
	t.Setenv("DATABASE_URL", "postgres://example.invalid/orchestrator")
	t.Setenv("TEMPORAL_HOST_PORT", "temporal:7233")
	t.Setenv("REPOSITORY_ALLOWED_ROOTS", "/projects,/workspace/services/../services")
	t.Setenv("REPOSITORY_STORAGE_PATH", "/data/./repositories")
	t.Setenv("WORKTREE_STORAGE_PATH", "/data/worktrees")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.RepositoryAllowedRoots, []string{"/projects", "/workspace/services"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RepositoryAllowedRoots = %#v, want %#v", got, want)
	}
	if got, want := cfg.RepositoryStoragePath, "/data/repositories"; got != want {
		t.Fatalf("RepositoryStoragePath = %q, want %q", got, want)
	}
}

func TestLoad_RejectsRelativeRepositoryPath(t *testing.T) {
	t.Setenv("REPOSITORY_ALLOWED_ROOTS", "relative/projects")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want relative path error")
	}
}

func TestLoad_RejectsFilesystemRootAsAllowedRoot(t *testing.T) {
	t.Setenv("REPOSITORY_ALLOWED_ROOTS", "/")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want filesystem root error")
	}
}

func TestLoad_ValidatesGitLabConfigurationPair(t *testing.T) {
	t.Setenv("GITLAB_BASE_URL", "https://gitlab.example.test")
	t.Setenv("GITLAB_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted GitLab base URL without token")
	}

	t.Setenv("GITLAB_TOKEN", "secret")
	t.Setenv("GITLAB_CONTROL_PROJECT", "group/control")
	t.Setenv("GITLAB_BASE_URL", "https://user:password@gitlab.example.test")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted credentials in GitLab base URL")
	}
}

func TestLoad_ValidatesGitLabControlProjectAndWebhookSecret(t *testing.T) {
	t.Setenv("GITLAB_BASE_URL", "https://gitlab.example.test")
	t.Setenv("GITLAB_TOKEN", "secret")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() rejected Stage 3 GitLab configuration without control project: %v", err)
	}
	t.Setenv("GITLAB_CONTROL_PROJECT", "../unsafe")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an unsafe GitLab control project")
	}
	t.Setenv("GITLAB_CONTROL_PROJECT", "group/control")
	t.Setenv("GITLAB_WEBHOOK_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a short GitLab webhook secret")
	}
	t.Setenv("GITLAB_WEBHOOK_SECRET", "0123456789abcdef")
	cfg, err := Load()
	if err != nil || cfg.GitLabControlProject != "group/control" {
		t.Fatalf("Load() = %#v, %v", cfg, err)
	}
}

func TestLoad_ValidatesGitLabSigningToken(t *testing.T) {
	t.Setenv("GITLAB_BASE_URL", "https://gitlab.example.test")
	t.Setenv("GITLAB_TOKEN", "secret")
	t.Setenv("GITLAB_CONTROL_PROJECT", "group/control")
	t.Setenv("GITLAB_WEBHOOK_SIGNING_TOKEN", "invalid")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an invalid GitLab signing token")
	}
	token := "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	t.Setenv("GITLAB_WEBHOOK_SIGNING_TOKEN", token)
	cfg, err := Load()
	if err != nil || cfg.GitLabWebhookSigningToken != token || !cfg.SafeSummary().GitLabWebhookSigned {
		t.Fatalf("Load() signing token = %#v, %v", cfg.SafeSummary(), err)
	}
}

func TestConfig_ModelAndReasoningUseComplexityProfiles(t *testing.T) {
	cfg := Config{
		CodexModelFast:     "configured-fast",
		CodexModelStandard: "configured-standard",
		CodexModelDeep:     "configured-deep",
		CodexModelReview:   "configured-review",
		CodexReasoningFast: "low", CodexReasoningStandard: "medium",
		CodexReasoningDeep: "high", CodexReasoningReview: "xhigh",
	}

	tests := map[string]string{
		ModelProfileFast:     "configured-fast",
		ModelProfileStandard: "configured-standard",
		ModelProfileDeep:     "configured-deep",
		ModelProfileReview:   "configured-review",
	}
	reasoning := map[string]string{
		ModelProfileFast: "low", ModelProfileStandard: "medium", ModelProfileDeep: "high", ModelProfileReview: "xhigh",
	}
	for profile, want := range tests {
		got, err := cfg.Model(profile)
		if err != nil {
			t.Fatalf("Model(%q) error = %v", profile, err)
		}
		if got != want {
			t.Fatalf("Model(%q) = %q, want %q", profile, got, want)
		}
		gotEffort, err := cfg.ReasoningEffort(profile)
		if err != nil || gotEffort != reasoning[profile] {
			t.Fatalf("ReasoningEffort(%q) = %q, %v", profile, gotEffort, err)
		}
	}
	if _, err := cfg.Model("unknown"); err == nil {
		t.Fatal("Model(unknown) error = nil")
	}
	if _, err := cfg.ReasoningEffort("unknown"); err == nil {
		t.Fatal("ReasoningEffort(unknown) error = nil")
	}
}

func TestLoadUsesChatGPTCodexModelDefaults(t *testing.T) {
	t.Setenv("REPOSITORY_ALLOWED_ROOTS", "/projects")
	t.Setenv("REPOSITORY_STORAGE_PATH", "/data/repositories")
	t.Setenv("WORKTREE_STORAGE_PATH", "/data/worktrees")
	t.Setenv("CODEX_MODEL_FAST", "")
	t.Setenv("CODEX_MODEL_STANDARD", "")
	t.Setenv("CODEX_MODEL_DEEP", "")
	t.Setenv("CODEX_MODEL_REVIEW", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CodexModelFast != "gpt-5.6-terra" || cfg.CodexModelDeep != "gpt-5.6" ||
		cfg.CodexReasoningFast != "low" || cfg.CodexReasoningDeep != "high" {
		t.Fatalf("unexpected Codex defaults: %#v", cfg.SafeSummary())
	}
}

func TestLoadUsesFakeWorkItemsByDefaultAndGatesRealGitHub(t *testing.T) {
	t.Setenv("WORK_ITEM_GATEWAY", "")
	cfg, err := Load()
	if err != nil || cfg.WorkItemGateway != "fake" {
		t.Fatalf("default work-item gateway = %q, %v", cfg.WorkItemGateway, err)
	}
	t.Setenv("WORK_ITEM_GATEWAY", "github")
	t.Setenv("GITHUB_DRY_RUN", "false")
	t.Setenv("GITHUB_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted real GitHub publication without a token")
	}
}

func TestSafeSummary_DoesNotExposeSensitiveValues(t *testing.T) {
	cfg := Config{
		DatabaseURL:            "postgres://user:secret@db/orchestrator",
		GitLabToken:            "gitlab-secret",
		GitLabBaseURL:          "https://gitlab.example.test",
		GitLabWebhookSecret:    "webhook-secret-123",
		TelegramBotToken:       "telegram-secret",
		TelegramAllowedUserIDs: []int64{12345},
		TelegramAllowedChatIDs: []int64{67890},
		TelegramWebhookSecret:  "telegram-webhook-secret",
		CodexModelFast:         "private-model-alias",
		RepositoryAllowedRoots: []string{"/projects"},
	}

	summary := cfg.SafeSummary()
	if !summary.DatabaseConfigured || !summary.GitLabConfigured || !summary.TelegramConfigured {
		t.Fatalf("SafeSummary() configuration flags = %#v", summary)
	}
	if !summary.GitLabWebhookConfigured {
		t.Fatal("SafeSummary() did not report configured GitLab webhook")
	}
	if summary.TelegramAllowedUserCount != 1 {
		t.Fatalf("TelegramAllowedUserCount = %d, want 1", summary.TelegramAllowedUserCount)
	}
	if got, want := summary.ConfiguredModelProfiles, []string{ModelProfileFast}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ConfiguredModelProfiles = %#v, want %#v", got, want)
	}
}

func TestLoad_ValidatesTelegramAllowlistsAndWebhook(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:abcdefghijklmnopqrstuvwxyzABCDE")
	t.Setenv("TELEGRAM_ALLOWED_USER_IDS", "101,202")
	t.Setenv("TELEGRAM_ALLOWED_CHAT_IDS", "-303,404")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() polling config error = %v", err)
	}
	if cfg.SafeSummary().TelegramMode != "polling" || cfg.SafeSummary().TelegramAllowedChatCount != 2 {
		t.Fatalf("Telegram polling summary = %#v", cfg.SafeSummary())
	}
	t.Setenv("TELEGRAM_WEBHOOK_URL", "https://orchestrator.example.test/api/v1/integrations/telegram/webhook")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "valid_webhook_secret_123")
	cfg, err = Load()
	if err != nil || cfg.SafeSummary().TelegramMode != "webhook" {
		t.Fatalf("Load() webhook config = %#v, %v", cfg.SafeSummary(), err)
	}
}

func TestLoad_RejectsUnsafeTelegramConfiguration(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "123456:abcdefghijklmnopqrstuvwxyzABCDE")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a bot token without allowlists")
	}
	t.Setenv("TELEGRAM_ALLOWED_USER_IDS", "101,101")
	t.Setenv("TELEGRAM_ALLOWED_CHAT_IDS", "-303")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted duplicate Telegram users")
	}
	t.Setenv("TELEGRAM_ALLOWED_USER_IDS", "101")
	t.Setenv("TELEGRAM_WEBHOOK_URL", "http://orchestrator.example.test/webhook")
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "short")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an unsafe Telegram webhook")
	}
}
