package architecturecatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/bemulima/agent-orchestrator/internal/domain"
	"strings"
	"testing"
)

func normalizedTestFleet() domain.ArchitectureFleetInputs {
	result := domain.ArchitectureFleetInputs{SchemaVersion: domain.ArchitectureFleetInputsSchemaV1}
	for i := 0; i < 42; i++ {
		id := fmt.Sprintf("example/service-%02d", i)
		result.Repositories = append(result.Repositories, domain.ArchitectureFleetRepository{RepositoryID: id, SourceIdentity: "git:github.com/" + id, RemoteURL: "https://github.com/" + id + ".git", CommitSHA: strings.Repeat("a", 40), Profile: "GO_SERVICE", ServiceID: fmt.Sprintf("service-%02d", i), RepositoryRole: domain.RepositoryRoleService, Declarations: []domain.ArchitectureFleetDeclaration{{Path: ".ai/architecture/service.yaml", BlobOID: strings.Repeat("b", 40), ContentSHA256: strings.Repeat("c", 64)}}})
	}
	return result
}
func TestFleetInputsStrictNormalizedLock(t *testing.T) {
	fleet := normalizedTestFleet()
	raw, _ := json.Marshal(fleet)
	_, digest, err := ParseFleetInputs(raw)
	if err != nil || len(digest) != 64 {
		t.Fatalf("valid lock: %v", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	if _, prettyDigest, err := ParseFleetInputs(pretty.Bytes()); err != nil || prettyDigest != digest {
		t.Fatal("input whitespace changed fleet digest")
	}
	for _, change := range []func(*domain.ArchitectureFleetInputs){func(f *domain.ArchitectureFleetInputs) { f.Repositories = f.Repositories[:41] }, func(f *domain.ArchitectureFleetInputs) { f.Repositories[0].CommitSHA = "main" }, func(f *domain.ArchitectureFleetInputs) {
		f.Repositories[0].Declarations[0].Path = "test-results/secret.yaml"
	}, func(f *domain.ArchitectureFleetInputs) {
		f.Repositories[0].Declarations = append(f.Repositories[0].Declarations, f.Repositories[0].Declarations[0])
	}, func(f *domain.ArchitectureFleetInputs) {
		f.Repositories[1].SourceIdentity = f.Repositories[0].SourceIdentity
	}} {
		f := normalizedTestFleet()
		change(&f)
		b, _ := json.Marshal(f)
		if _, _, err := ParseFleetInputs(b); err == nil {
			t.Fatal("invalid lock accepted")
		}
	}
	for _, b := range []string{strings.Replace(string(raw), `"schema_version":`, `"schema_version":"architecture-fleet-inputs.v1","schema_version":`, 1), strings.Replace(string(raw), `"commit_sha":`, `"Commit_SHA":`, 1), string(raw) + " {}"} {
		if _, _, err := ParseFleetInputs([]byte(b)); err == nil {
			t.Fatal("ambiguous lock accepted")
		}
	}
}

func classifiedTestExternalOwner() domain.ArchitectureFleetExternalOwner {
	repo := normalizedTestFleet().Repositories[0]
	repo.RepositoryID = "example/external-owner"
	repo.SourceIdentity = "git:github.com/" + repo.RepositoryID
	repo.RemoteURL = "https://github.com/" + repo.RepositoryID + ".git"
	repo.ServiceID = "external-owner"
	repo.Profile = ""
	return domain.ArchitectureFleetExternalOwner{ArchitectureFleetRepository: repo, Classification: domain.ArchitectureFleetExternalOwnerClassification}
}
func TestFleetInputsClassifiedExternalOwners(t *testing.T) {
	base := normalizedTestFleet()
	raw, _ := json.Marshal(base)
	_, baseDigest, _ := ParseFleetInputs(raw)
	nullExternal := bytes.Replace(raw, []byte(`"repositories":`), []byte(`"external_owners":null,"repositories":`), 1)
	if _, _, err := ParseFleetInputs(nullExternal); err == nil {
		t.Fatal("null external owner cohort accepted")
	}
	base.ExternalOwners = []domain.ArchitectureFleetExternalOwner{classifiedTestExternalOwner()}
	raw, _ = json.Marshal(base)
	if bytes.Contains(raw, []byte(`"profile":""`)) {
		t.Fatal("absent external profile invented")
	}
	withNullProfile := bytes.Replace(raw, []byte(`"classification":"PINNED_EXTERNAL_OWNER_INPUT"`), []byte(`"profile":null,"classification":"PINNED_EXTERNAL_OWNER_INPUT"`), 1)
	if _, _, err := ParseFleetInputs(withNullProfile); err == nil {
		t.Fatal("explicit null external profile accepted")
	}
	parsed, digest, err := ParseFleetInputs(raw)
	if err != nil || parsed.ExternalOwners[0].Profile != nil || digest == baseDigest {
		t.Fatalf("external digest/profile: %v", err)
	}
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, raw, "", "  ")
	if _, got, err := ParseFleetInputs(pretty.Bytes()); err != nil || got != digest {
		t.Fatal("external digest nondeterministic")
	}
	cases := map[string]func(*domain.ArchitectureFleetInputs){
		"classification":       func(f *domain.ArchitectureFleetInputs) { f.ExternalOwners[0].Classification = "" },
		"wrong-classification": func(f *domain.ArchitectureFleetInputs) { f.ExternalOwners[0].Classification = "external" },
		"mutable-commit":       func(f *domain.ArchitectureFleetInputs) { f.ExternalOwners[0].CommitSHA = "main" },
		"empty-profile":        func(f *domain.ArchitectureFleetInputs) { s := ""; f.ExternalOwners[0].Profile = &s },
		"repository-overlap": func(f *domain.ArchitectureFleetInputs) {
			f.ExternalOwners[0].ArchitectureFleetRepository = f.Repositories[0]
		},
		"service-overlap": func(f *domain.ArchitectureFleetInputs) { f.ExternalOwners[0].ServiceID = f.Repositories[0].ServiceID },
		"duplicate-external": func(f *domain.ArchitectureFleetInputs) {
			f.ExternalOwners = append(f.ExternalOwners, f.ExternalOwners[0])
		},
		"forbidden-path": func(f *domain.ArchitectureFleetInputs) {
			f.ExternalOwners[0].Declarations[0].Path = "test-results/private.yaml"
		},
		"missing-declarations":         func(f *domain.ArchitectureFleetInputs) { f.ExternalOwners[0].Declarations = nil },
		"fleet-profile-still-required": func(f *domain.ArchitectureFleetInputs) { f.Repositories[0].Profile = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := normalizedTestFleet()
			f.ExternalOwners = []domain.ArchitectureFleetExternalOwner{classifiedTestExternalOwner()}
			change(&f)
			b, _ := json.Marshal(f)
			if _, _, err := ParseFleetInputs(b); err == nil {
				t.Fatal("invalid external owner accepted")
			}
		})
	}
}
