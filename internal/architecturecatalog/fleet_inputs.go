package architecturecatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bemulima/agent-orchestrator/internal/domain"
)

// ParseFleetInputs accepts a normalized full-fleet owner lock. Local object-store
// paths are deliberately excluded from the portable semantic digest.
func ParseFleetInputs(raw []byte) (domain.ArchitectureFleetInputs, string, error) {
	var result domain.ArchitectureFleetInputs
	if err := strictFleetJSON(json.NewDecoder(bytes.NewReader(raw)), 0); err != nil {
		return result, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, "", err
	}
	if result.SchemaVersion != domain.ArchitectureFleetInputsSchemaV1 || len(result.Repositories) != 42 {
		return result, "", fmt.Errorf("versioned exact 42 repository fleet required: %w", domain.ErrValidation)
	}
	services := map[string]bool{}
	identities := map[string]bool{}
	validate := func(repo domain.ArchitectureFleetRepository, previous string, requireProfile bool) error {
		if !graphRepositoryID.MatchString(repo.RepositoryID) || repo.SourceIdentity != "git:github.com/"+repo.RepositoryID || repo.RemoteURL != "https://github.com/"+repo.RepositoryID+".git" || !graphGitSHA.MatchString(repo.CommitSHA) || (requireProfile && strings.TrimSpace(repo.Profile) == "") || strings.TrimSpace(repo.ServiceID) == "" || services[repo.ServiceID] || identities[repo.SourceIdentity] || (repo.RepositoryRole != domain.RepositoryRoleService && repo.RepositoryRole != domain.RepositoryRoleFrontend && repo.RepositoryRole != domain.RepositoryRoleInfrastructure && repo.RepositoryRole != domain.RepositoryRolePolicy && repo.RepositoryRole != domain.RepositoryRoleContent && repo.RepositoryRole != domain.RepositoryRoleDocumentation && repo.RepositoryRole != domain.RepositoryRoleArchive) || len(repo.Declarations) == 0 || (previous != "" && previous >= repo.SourceIdentity) {
			return fmt.Errorf("invalid normalized fleet owner: %w", domain.ErrValidation)
		}
		services[repo.ServiceID] = true
		identities[repo.SourceIdentity] = true
		for j, declaration := range repo.Declarations {
			pin := domain.ArchitectureGraphPin{SourceIdentity: repo.SourceIdentity, CommitSHA: repo.CommitSHA, Path: declaration.Path, BlobOID: declaration.BlobOID, ContentSHA256: declaration.ContentSHA256}
			if !ValidGraphPin(pin) || forbiddenFleetPath(declaration.Path) || (j > 0 && repo.Declarations[j-1].Path >= declaration.Path) {
				return fmt.Errorf("invalid sorted declaration pin: %w", domain.ErrValidation)
			}
		}
		return nil
	}
	previous := ""
	for _, repo := range result.Repositories {
		if err := validate(repo, previous, true); err != nil {
			return result, "", err
		}
		previous = repo.SourceIdentity
	}
	previous = ""
	for _, owner := range result.ExternalOwners {
		if owner.Classification != domain.ArchitectureFleetExternalOwnerClassification || (owner.Profile != nil && strings.TrimSpace(*owner.Profile) == "") {
			return result, "", fmt.Errorf("classified pinned external owner required: %w", domain.ErrValidation)
		}
		if err := validate(owner.ArchitectureFleetRepository, previous, false); err != nil {
			return result, "", err
		}
		previous = owner.SourceIdentity
	}
	canonical, err := CanonicalGraphJSON(result)
	if err != nil {
		return result, "", err
	}
	sum := sha256.Sum256(canonical)
	return result, hex.EncodeToString(sum[:]), nil
}
func forbiddenFleetPath(file string) bool {
	for _, component := range strings.Split(file, "/") {
		if component == "test-results" || component == ".git" || component == ".env" || strings.HasPrefix(component, ".env.") {
			return true
		}
	}
	return false
}

// Exact key spellings and duplicate decoded keys are checked at every depth.
func strictFleetJSON(d *json.Decoder, depth int) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	switch token {
	case nil:
		return fmt.Errorf("fleet fields must not be null: %w", domain.ErrValidation)
	case json.Delim('{'):
		allowed := map[int]string{0: "schema_version repositories external_owners", 2: "repository_id source_identity remote_url commit_sha profile service_id repository_role declarations classification", 4: "path blob_oid content_sha256"}
		seen := map[string]bool{}
		for d.More() {
			t, e := d.Token()
			k, ok := t.(string)
			if e != nil || !ok || seen[k] || !strings.Contains(" "+allowed[depth]+" ", " "+k+" ") {
				return fmt.Errorf("duplicate or unknown fleet field: %w", domain.ErrValidation)
			}
			seen[k] = true
			if e = strictFleetJSON(d, depth+1); e != nil {
				return e
			}
		}
		if _, err = d.Token(); err != nil {
			return err
		}
	case json.Delim('['):
		for d.More() {
			if err = strictFleetJSON(d, depth+1); err != nil {
				return err
			}
		}
		if _, err = d.Token(); err != nil {
			return err
		}
	}
	if depth == 0 {
		if _, err = d.Token(); err != io.EOF {
			return fmt.Errorf("trailing fleet input: %w", domain.ErrValidation)
		}
	}
	return nil
}
