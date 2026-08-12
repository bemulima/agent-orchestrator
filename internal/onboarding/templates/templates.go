package templates

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const Version = "v1"

//go:embed v1/*.md v1/bundle.yaml
var bundle embed.FS

var requiredFiles = []string{
	"v1/agents-managed.md",
	"v1/backend-coder.md",
	"v1/bundle.yaml",
	"v1/coder.md",
	"v1/common-rules.md",
	"v1/migration-agent.md",
	"v1/reviewer.md",
}

func AgentsManagedBlock() string { return mustRead("v1/agents-managed.md") }
func CommonRules() string        { return mustRead("v1/common-rules.md") }
func Coder() string              { return mustRead("v1/coder.md") }
func Reviewer() string           { return mustRead("v1/reviewer.md") }
func MigrationAgent() string     { return mustRead("v1/migration-agent.md") }

func BackendCoder(verification string) string {
	return strings.ReplaceAll(mustRead("v1/backend-coder.md"), "{{verification}}", verification)
}

func Validate() error {
	for _, name := range requiredFiles {
		content, err := bundle.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read canonical template %s: %w", name, err)
		}
		if strings.TrimSpace(string(content)) == "" {
			return fmt.Errorf("canonical template %s is empty", name)
		}
	}
	commonRules := strings.ToLower(CommonRules())
	for _, token := range []string{"issue", "git", "bugfix", "feature", "refactor", "review", "contract"} {
		if !strings.Contains(commonRules, token) {
			return fmt.Errorf("common rules do not cover %q", token)
		}
	}
	return nil
}

func Checksum() string {
	names := append([]string(nil), requiredFiles...)
	sort.Strings(names)
	digest := sha256.New()
	for _, name := range names {
		content, err := bundle.ReadFile(name)
		if err != nil {
			panic(err)
		}
		_, _ = digest.Write([]byte(name))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(content)
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func mustRead(name string) string {
	content, err := bundle.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(content))
}
